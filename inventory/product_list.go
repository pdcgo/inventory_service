package inventory

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	common "github.com/pdcgo/schema/services/common/v1"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"gorm.io/gorm"
)

// ProductList implements [inventory_ifaceconnect.InventoryServiceHandler].
//
// Flexible list (see docs/proto-guideline.md) over the products tracked in a warehouse
// (those with a StockState row): resolves the ordered, paginated product ids for the
// filter+sort, then fills a map<id, item> per requested data type.
func (s *inventoryServiceImpl) ProductList(
	ctx context.Context,
	req *connect.Request[inventory_iface.ProductListRequest],
) (*connect.Response[inventory_iface.ProductListResponse], error) {
	pay := req.Msg
	if pay.GetFilter() == nil || pay.GetFilter().GetPage() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("filter.page is required"))
	}
	db := s.db.WithContext(ctx)

	ids, err := productListIDs(db, pay.GetFilter(), pay.GetSort())
	if err != nil {
		return nil, err
	}

	resp := &inventory_iface.ProductListResponse{
		Data: make([]*inventory_iface.ProductData, 0, len(pay.GetDataTypes())),
		Ids:  ids,
	}
	for _, dt := range pay.GetDataTypes() {
		data, err := fetchProductData(db, dt, ids, pay.GetFilter().GetWarehouseId())
		if err != nil {
			return nil, err
		}
		if data != nil {
			resp.Data = append(resp.Data, data)
		}
	}

	return connect.NewResponse(resp), nil
}

// productListIDs returns the ordered, paginated product ids for the filter + sort.
func productListIDs(db *gorm.DB, filter *inventory_iface.ProductListFilter, sort *inventory_iface.ProductListSort) ([]uint64, error) {
	q := db.
		Table("stock_states ss").
		Joins("JOIN products p ON p.id = ss.product_id").
		Where("ss.warehouse_id = ? AND p.deleted = false", filter.GetWarehouseId())
	if filter.GetTeamId() > 0 {
		q = q.Where("p.team_id = ?", filter.GetTeamId())
	}
	if search := filter.GetSearch(); search != "" {
		q = q.Where("p.name ILIKE ?", "%"+search+"%")
	}

	dir := "ASC"
	if sort.GetSortType() == common.SortType_SORT_TYPE_DESC {
		dir = "DESC"
	}
	col := "p.name"
	if sv, ok := sort.GetS().(*inventory_iface.ProductListSort_Stock); ok {
		if sv.Stock == inventory_iface.ProductStockSort_PRODUCT_STOCK_SORT_STOCK_AMOUNT {
			col = "ss.stock_ready_amount"
		} else {
			col = "ss.stock_ready"
		}
	}

	limit, offset := rackPageLimitOffset(filter.GetPage()) // shared helper (rack_list.go)
	var ids []uint64
	err := q.
		Order(col + " " + dir).
		Order("ss.product_id " + dir). // stable tiebreak
		Limit(limit).
		Offset(offset).
		Pluck("ss.product_id", &ids).
		Error
	return ids, err
}

// fetchProductData builds one ProductData (the oneof) for a data type as a map keyed by product id.
func fetchProductData(db *gorm.DB, dt inventory_iface.ProductListDataType, ids []uint64, warehouseID uint64) (*inventory_iface.ProductData, error) {
	switch dt {
	case inventory_iface.ProductListDataType_PRODUCT_LIST_DATA_TYPE_GENERAL:
		out := map[uint64]*inventory_iface.ProductGeneralItem{}
		if len(ids) > 0 {
			var rows []struct {
				ID   uint64
				Name string
			}
			if err := db.Table("products").Select("id, name").Where("id IN ?", ids).Scan(&rows).Error; err != nil {
				return nil, err
			}
			for _, r := range rows {
				out[r.ID] = &inventory_iface.ProductGeneralItem{Id: r.ID, Name: r.Name}
			}
		}
		return &inventory_iface.ProductData{Data: &inventory_iface.ProductData_General{General: &inventory_iface.ProductGeneralData{Data: out}}}, nil

	case inventory_iface.ProductListDataType_PRODUCT_LIST_DATA_TYPE_STOCK:
		out := map[uint64]*inventory_iface.ProductStockItem{}
		item := func(id uint64) *inventory_iface.ProductStockItem {
			if out[id] == nil {
				out[id] = &inventory_iface.ProductStockItem{}
			}
			return out[id]
		}
		if len(ids) > 0 {
			var ss []struct {
				ProductID        uint64
				StockReady       int64
				StockReadyAmount float64
			}
			if err := db.Table("stock_states").Select("product_id, stock_ready, stock_ready_amount").
				Where("warehouse_id = ? AND product_id IN ?", warehouseID, ids).Scan(&ss).Error; err != nil {
				return nil, err
			}
			for _, r := range ss {
				it := item(r.ProductID)
				it.StockReady = r.StockReady
				it.StockReadyAmount = r.StockReadyAmount
			}
			var batches []struct {
				ProductID  uint64
				BatchCount int64
			}
			if err := db.Table("stock_batches").Select("product_id, count(*) as batch_count").
				Where("warehouse_id = ? AND product_id IN ? AND end_count > 0", warehouseID, ids).
				Group("product_id").Scan(&batches).Error; err != nil {
				return nil, err
			}
			for _, r := range batches {
				item(r.ProductID).BatchCount = r.BatchCount
			}
			var racks []struct {
				ProductID uint64
				RackCount int64
			}
			if err := db.Table("stock_placements").Select("product_id, count(distinct rack_id) as rack_count").
				Where("warehouse_id = ? AND product_id IN ? AND count > 0", warehouseID, ids).
				Group("product_id").Scan(&racks).Error; err != nil {
				return nil, err
			}
			for _, r := range racks {
				item(r.ProductID).RackCount = r.RackCount
			}
		}
		return &inventory_iface.ProductData{Data: &inventory_iface.ProductData_Stock{Stock: &inventory_iface.ProductStockData{Data: out}}}, nil

	case inventory_iface.ProductListDataType_PRODUCT_LIST_DATA_TYPE_TEAM:
		out := map[uint64]*inventory_iface.ProductTeamItem{}
		if len(ids) > 0 {
			var rows []struct {
				ID       uint64
				TeamID   uint64
				TeamName string
			}
			if err := db.Table("products p").
				Joins("LEFT JOIN teams t ON t.id = p.team_id").
				Select("p.id as id, p.team_id as team_id, t.name as team_name").
				Where("p.id IN ?", ids).Scan(&rows).Error; err != nil {
				return nil, err
			}
			for _, r := range rows {
				out[r.ID] = &inventory_iface.ProductTeamItem{TeamId: r.TeamID, TeamName: r.TeamName}
			}
		}
		return &inventory_iface.ProductData{Data: &inventory_iface.ProductData_Team{Team: &inventory_iface.ProductTeamData{Data: out}}}, nil

	case inventory_iface.ProductListDataType_PRODUCT_LIST_DATA_TYPE_CONFIG:
		out := map[uint64]*inventory_iface.ProductConfigItem{}
		if len(ids) > 0 {
			var rows []inventory_models.ProductConfig
			if err := db.Where("warehouse_id = ? AND product_id IN ?", warehouseID, ids).Find(&rows).Error; err != nil {
				return nil, err
			}
			byProduct := map[uint64]inventory_models.ProductConfig{}
			for _, r := range rows {
				byProduct[r.ProductID] = r
			}
			for _, id := range ids {
				if c, ok := byProduct[id]; ok {
					out[id] = &inventory_iface.ProductConfigItem{QueueType: c.QueueType, PlacementPicking: c.PlacementPicking, Configured: true}
				} else {
					out[id] = &inventory_iface.ProductConfigItem{
						QueueType:        inventory_iface.QueueType_QUEUE_TYPE_FIFO,
						PlacementPicking: inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_SMALLER,
						Configured:       false,
					}
				}
			}
		}
		return &inventory_iface.ProductData{Data: &inventory_iface.ProductData_Config{Config: &inventory_iface.ProductConfigData{Data: out}}}, nil
	}
	return nil, nil
}
