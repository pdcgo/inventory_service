package inventory

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	common "github.com/pdcgo/schema/services/common/v1"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// RackList implements [inventory_ifaceconnect.InventoryServiceHandler].
//
// Flexible list (see docs/proto-guideline.md): it first resolves the ordered, paginated rack
// ids for the filter+sort, then fills a map<id, item> for each requested data type. Excludes
// soft-deleted racks.
func (s *inventoryServiceImpl) RackList(
	ctx context.Context,
	req *connect.Request[inventory_iface.RackListRequest],
) (*connect.Response[inventory_iface.RackListResponse], error) {
	pay := req.Msg
	if pay.GetFilter() == nil || pay.GetFilter().GetPage() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("filter.page is required"))
	}
	db := s.db.WithContext(ctx)

	ids, err := rackListIDs(db, pay.GetFilter(), pay.GetSort())
	if err != nil {
		return nil, err
	}

	resp := &inventory_iface.RackListResponse{
		Data: make([]*inventory_iface.RackData, 0, len(pay.GetDataTypes())),
		Ids:  ids,
	}
	for _, dt := range pay.GetDataTypes() {
		data, err := fetchRackData(db, dt, ids)
		if err != nil {
			return nil, err
		}
		if data != nil {
			resp.Data = append(resp.Data, data)
		}
	}

	return connect.NewResponse(resp), nil
}

// rackListIDs returns the ordered, paginated rack ids for the filter + sort.
func rackListIDs(db *gorm.DB, filter *inventory_iface.RackListFilter, sort *inventory_iface.RackListSort) ([]uint64, error) {
	q := db.Table("racks r").Where("r.deleted = false")
	if filter.GetWarehouseId() > 0 {
		q = q.Where("r.warehouse_id = ?", filter.GetWarehouseId())
	}
	if search := filter.GetSearch(); search != "" {
		q = q.Where("r.name ILIKE ?", "%"+search+"%")
	}
	if filter.GetTeamId() > 0 {
		// racks that currently hold the team's stock (products owned by team_id).
		q = q.Where(`EXISTS (
			SELECT 1 FROM stock_placements sp
			JOIN products p ON p.id = sp.product_id
			WHERE sp.rack_id = r.id AND sp.count > 0 AND p.team_id = ?
		)`, filter.GetTeamId())
	}
	if filter.GetProductId() > 0 {
		// racks that currently hold this product.
		q = q.Where(`EXISTS (
			SELECT 1 FROM stock_placements sp
			WHERE sp.rack_id = r.id AND sp.product_id = ? AND sp.count > 0
		)`, filter.GetProductId())
	}

	dir := "ASC"
	if sort.GetSortType() == common.SortType_SORT_TYPE_DESC {
		dir = "DESC"
	}
	col := "r.name"
	stockSort := false
	switch sv := sort.GetS().(type) {
	case *inventory_iface.RackListSort_General:
		if sv.General == inventory_iface.RackGeneralSort_RACK_GENERAL_SORT_CREATED {
			col = "r.created_at"
		}
	case *inventory_iface.RackListSort_Stock:
		stockSort = true
		if sv.Stock == inventory_iface.RackStockSort_RACK_STOCK_SORT_PRODUCT_COUNT {
			col = "count(distinct sp.product_id)"
		} else {
			col = "coalesce(sum(sp.count),0)"
		}
	}
	if stockSort {
		q = q.Joins("LEFT JOIN stock_placements sp ON sp.rack_id = r.id").Group("r.id")
	}

	limit, offset := rackPageLimitOffset(filter.GetPage())
	var ids []uint64
	err := q.
		Order(col + " " + dir).
		Order("r.id " + dir). // stable tiebreak
		Limit(limit).
		Offset(offset).
		Pluck("r.id", &ids).
		Error
	return ids, err
}

func rackPageLimitOffset(p *common.PageFilter) (int, int) {
	if p == nil || p.GetLimit() <= 0 {
		return 100, 0
	}
	offset := (p.GetPage() - 1) * p.GetLimit()
	if offset < 0 {
		offset = 0
	}
	return int(p.GetLimit()), int(offset)
}

// fetchRackData builds one RackData (the oneof) for a data type as a map keyed by rack id.
func fetchRackData(db *gorm.DB, dt inventory_iface.RackListDataType, ids []uint64) (*inventory_iface.RackData, error) {
	switch dt {
	case inventory_iface.RackListDataType_RACK_LIST_DATA_TYPE_GENERAL:
		out := map[uint64]*inventory_iface.RackGeneralItem{}
		if len(ids) > 0 {
			var rows []struct {
				ID        uint64
				Name      string
				CreatedAt time.Time
			}
			if err := db.Table("racks").Select("id, name, created_at").Where("id IN ?", ids).Scan(&rows).Error; err != nil {
				return nil, err
			}
			for _, r := range rows {
				out[r.ID] = &inventory_iface.RackGeneralItem{Id: r.ID, Name: r.Name, CreatedAt: timestamppb.New(r.CreatedAt)}
			}
		}
		return &inventory_iface.RackData{Data: &inventory_iface.RackData_General{General: &inventory_iface.RackGeneralData{Data: out}}}, nil

	case inventory_iface.RackListDataType_RACK_LIST_DATA_TYPE_STOCK:
		out := map[uint64]*inventory_iface.RackStockItem{}
		if len(ids) > 0 {
			var rows []struct {
				RackID       uint64
				StockCount   int64
				ProductCount int64
			}
			if err := db.Table("stock_placements").
				Select("rack_id, coalesce(sum(count),0) as stock_count, count(distinct product_id) filter (where count > 0) as product_count").
				Where("rack_id IN ?", ids).
				Group("rack_id").
				Scan(&rows).Error; err != nil {
				return nil, err
			}
			for _, r := range rows {
				out[r.RackID] = &inventory_iface.RackStockItem{StockCount: r.StockCount, ProductCount: r.ProductCount}
			}
		}
		return &inventory_iface.RackData{Data: &inventory_iface.RackData_Stock{Stock: &inventory_iface.RackStockData{Data: out}}}, nil

	case inventory_iface.RackListDataType_RACK_LIST_DATA_TYPE_WAREHOUSE:
		out := map[uint64]*inventory_iface.RackWarehouseItem{}
		if len(ids) > 0 {
			var rows []struct {
				ID            uint64
				WarehouseID   uint64
				WarehouseName string
			}
			if err := db.Table("racks r").
				Joins("LEFT JOIN warehouses w ON w.id = r.warehouse_id").
				Select("r.id as id, r.warehouse_id as warehouse_id, w.name as warehouse_name").
				Where("r.id IN ?", ids).
				Scan(&rows).Error; err != nil {
				return nil, err
			}
			for _, r := range rows {
				out[r.ID] = &inventory_iface.RackWarehouseItem{WarehouseId: r.WarehouseID, WarehouseName: r.WarehouseName}
			}
		}
		return &inventory_iface.RackData{Data: &inventory_iface.RackData_Warehouse{Warehouse: &inventory_iface.RackWarehouseData{Data: out}}}, nil
	}
	return nil, nil
}
