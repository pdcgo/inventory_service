package inventory

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	common "github.com/pdcgo/schema/services/common/v1"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// RestockList implements [inventory_ifaceconnect.InventoryServiceHandler].
//
// Flexible list (see docs/proto-guideline.md): it first resolves the ordered,
// paginated restock ids for the filter+sort, then fills a map<id, item> for each
// requested data type.
func (s *inventoryServiceImpl) RestockList(
	ctx context.Context,
	req *connect.Request[inventory_iface.RestockListRequest],
) (*connect.Response[inventory_iface.RestockListResponse], error) {
	pay := req.Msg
	if pay.GetFilter() == nil || pay.GetFilter().GetPage() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("filter.page is required"))
	}
	db := s.db.WithContext(ctx)

	ids, err := restockListIDs(db, pay.GetFilter(), pay.GetSort())
	if err != nil {
		return nil, err
	}

	resp := &inventory_iface.RestockListResponse{
		Data: make([]*inventory_iface.RestockData, 0, len(pay.GetDataTypes())),
		Ids:  ids,
	}
	for _, dt := range pay.GetDataTypes() {
		data, err := fetchRestockData(db, dt, ids)
		if err != nil {
			return nil, err
		}
		if data != nil {
			resp.Data = append(resp.Data, data)
		}
	}

	return connect.NewResponse(resp), nil
}

// restockListIDs returns the ordered, paginated restock ids for the filter + sort.
func restockListIDs(db *gorm.DB, filter *inventory_iface.RestockListFilter, sort *inventory_iface.RestockListSort) ([]uint64, error) {
	q := db.Table("inventory_restocks r")
	if filter.GetWarehouseId() > 0 {
		q = q.Where("r.warehouse_id = ?", filter.GetWarehouseId())
	}
	if filter.GetTeamId() > 0 {
		q = q.Where("r.team_id = ?", filter.GetTeamId())
	}
	if filter.GetStatus() != inventory_iface.RestockStatus_RESTOCK_STATUS_UNSPECIFIED {
		q = q.Where("r.status = ?", string(restockStatusFromProto(filter.GetStatus())))
	}
	if search := filter.GetSearch(); search != "" {
		like := "%" + search + "%"
		q = q.Where("(r.receipt ILIKE ? OR r.supplier ILIKE ?)", like, like)
	}

	dir := "ASC"
	if sort.GetSortType() == common.SortType_SORT_TYPE_DESC {
		dir = "DESC"
	}
	col := "r.created_at"
	totalSort := false
	switch sv := sort.GetS().(type) {
	case *inventory_iface.RestockListSort_General:
		if sv.General == inventory_iface.RestockGeneralSort_RESTOCK_GENERAL_SORT_RECEIPT {
			col = "r.receipt"
		}
	case *inventory_iface.RestockListSort_Total:
		totalSort = true
		if sv.Total == inventory_iface.RestockTotalSort_RESTOCK_TOTAL_SORT_ITEM_COUNT {
			col = "coalesce(sum(ri.count),0)"
		} else {
			col = "coalesce(sum(ri.count * ri.price),0)"
		}
	}
	if totalSort {
		q = q.Joins("LEFT JOIN inventory_restock_items ri ON ri.restock_id = r.id").Group("r.id")
	}

	limit, offset := rackPageLimitOffset(filter.GetPage())
	var ids []uint64
	err := q.
		Order(col+" "+dir).
		Order("r.id "+dir). // stable tiebreak
		Limit(limit).
		Offset(offset).
		Pluck("r.id", &ids).
		Error
	return ids, err
}

// fetchRestockData builds one RestockData (the oneof) for a data type as a map keyed
// by restock id.
func fetchRestockData(db *gorm.DB, dt inventory_iface.RestockListDataType, ids []uint64) (*inventory_iface.RestockData, error) {
	switch dt {
	case inventory_iface.RestockListDataType_RESTOCK_LIST_DATA_TYPE_GENERAL:
		out := map[uint64]*inventory_iface.RestockGeneralItem{}
		if len(ids) > 0 {
			var rows []struct {
				ID         uint64
				Receipt    string
				Supplier   string
				Status     string
				CreatedAt  time.Time
				AcceptedAt *time.Time
			}
			err := db.
				Table("inventory_restocks").
				Select("id, receipt, supplier, status, created_at, accepted_at").
				Where("id IN ?", ids).
				Scan(&rows).
				Error
			if err != nil {
				return nil, err
			}
			for _, r := range rows {
				item := &inventory_iface.RestockGeneralItem{
					Id:        r.ID,
					Receipt:   r.Receipt,
					Supplier:  r.Supplier,
					Status:    restockStatusToProto(inventory_models.RestockStatus(r.Status)),
					CreatedAt: timestamppb.New(r.CreatedAt),
				}
				if r.AcceptedAt != nil {
					item.AcceptedAt = timestamppb.New(*r.AcceptedAt)
				}
				out[r.ID] = item
			}
		}
		return &inventory_iface.RestockData{Data: &inventory_iface.RestockData_General{General: &inventory_iface.RestockGeneralData{Data: out}}}, nil

	case inventory_iface.RestockListDataType_RESTOCK_LIST_DATA_TYPE_TOTAL:
		out := map[uint64]*inventory_iface.RestockTotalItem{}
		if len(ids) > 0 {
			var rows []struct {
				RestockID uint64
				ItemCount int64
				Amount    float64
			}
			err := db.
				Table("inventory_restock_items").
				Select("restock_id, coalesce(sum(count),0) as item_count, coalesce(sum(count * price),0) as amount").
				Where("restock_id IN ?", ids).
				Group("restock_id").
				Scan(&rows).
				Error
			if err != nil {
				return nil, err
			}
			for _, r := range rows {
				out[r.RestockID] = &inventory_iface.RestockTotalItem{ItemCount: r.ItemCount, Amount: r.Amount}
			}
		}
		return &inventory_iface.RestockData{Data: &inventory_iface.RestockData_Total{Total: &inventory_iface.RestockTotalData{Data: out}}}, nil
	}
	return nil, nil
}

// restockStatusToProto / restockStatusFromProto map the stored status string to the
// proto enum and back.
func restockStatusToProto(s inventory_models.RestockStatus) inventory_iface.RestockStatus {
	switch s {
	case inventory_models.RestockPending:
		return inventory_iface.RestockStatus_RESTOCK_STATUS_PENDING
	case inventory_models.RestockAccepted:
		return inventory_iface.RestockStatus_RESTOCK_STATUS_ACCEPTED
	case inventory_models.RestockProblem:
		return inventory_iface.RestockStatus_RESTOCK_STATUS_PROBLEM
	case inventory_models.RestockCanceled:
		return inventory_iface.RestockStatus_RESTOCK_STATUS_CANCELED
	default:
		return inventory_iface.RestockStatus_RESTOCK_STATUS_UNSPECIFIED
	}
}

func restockStatusFromProto(s inventory_iface.RestockStatus) inventory_models.RestockStatus {
	switch s {
	case inventory_iface.RestockStatus_RESTOCK_STATUS_PENDING:
		return inventory_models.RestockPending
	case inventory_iface.RestockStatus_RESTOCK_STATUS_ACCEPTED:
		return inventory_models.RestockAccepted
	case inventory_iface.RestockStatus_RESTOCK_STATUS_PROBLEM:
		return inventory_models.RestockProblem
	case inventory_iface.RestockStatus_RESTOCK_STATUS_CANCELED:
		return inventory_models.RestockCanceled
	default:
		return ""
	}
}
