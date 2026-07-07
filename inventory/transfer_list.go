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

// TransferList implements [inventory_ifaceconnect.InventoryServiceHandler].
//
// Flexible list (see docs/proto-guideline.md): it first resolves the ordered,
// paginated transfer ids for the filter+sort, then fills a map<id, item> for each
// requested data type. filter.warehouse_id matches EITHER side (from or to).
func (s *inventoryServiceImpl) TransferList(
	ctx context.Context,
	req *connect.Request[inventory_iface.TransferListRequest],
) (*connect.Response[inventory_iface.TransferListResponse], error) {
	pay := req.Msg
	if pay.GetFilter() == nil || pay.GetFilter().GetPage() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("filter.page is required"))
	}
	db := s.db.WithContext(ctx)

	ids, err := transferListIDs(db, pay.GetFilter(), pay.GetSort())
	if err != nil {
		return nil, err
	}

	resp := &inventory_iface.TransferListResponse{
		Data: make([]*inventory_iface.TransferData, 0, len(pay.GetDataTypes())),
		Ids:  ids,
	}
	for _, dt := range pay.GetDataTypes() {
		data, err := fetchTransferData(db, dt, ids)
		if err != nil {
			return nil, err
		}
		if data != nil {
			resp.Data = append(resp.Data, data)
		}
	}

	return connect.NewResponse(resp), nil
}

// transferListIDs returns the ordered, paginated transfer ids for the filter + sort.
func transferListIDs(db *gorm.DB, filter *inventory_iface.TransferListFilter, sort *inventory_iface.TransferListSort) ([]uint64, error) {
	q := db.Table("inventory_transfers t")
	if filter.GetWarehouseId() > 0 {
		q = q.Where("(t.from_warehouse_id = ? OR t.to_warehouse_id = ?)", filter.GetWarehouseId(), filter.GetWarehouseId())
	}
	if filter.GetFromWarehouseId() > 0 {
		q = q.Where("t.from_warehouse_id = ?", filter.GetFromWarehouseId())
	}
	if filter.GetToWarehouseId() > 0 {
		q = q.Where("t.to_warehouse_id = ?", filter.GetToWarehouseId())
	}
	if filter.GetTeamId() > 0 {
		q = q.Where("t.team_id = ?", filter.GetTeamId())
	}
	if filter.GetStatus() != inventory_iface.TransferStatus_TRANSFER_STATUS_UNSPECIFIED {
		q = q.Where("t.status = ?", string(transferStatusFromProto(filter.GetStatus())))
	}

	dir := "ASC"
	if sort.GetSortType() == common.SortType_SORT_TYPE_DESC {
		dir = "DESC"
	}
	col := "t.created_at"
	totalSort := false
	if sv, ok := sort.GetS().(*inventory_iface.TransferListSort_Total); ok {
		totalSort = true
		if sv.Total == inventory_iface.TransferTotalSort_TRANSFER_TOTAL_SORT_ITEM_COUNT {
			col = "coalesce(sum(ti.count),0)"
		} else {
			col = "coalesce(sum(ti.count * ti.price),0)"
		}
	}
	if totalSort {
		q = q.Joins("LEFT JOIN inventory_transfer_items ti ON ti.transfer_id = t.id").Group("t.id")
	}

	limit, offset := rackPageLimitOffset(filter.GetPage())
	var ids []uint64
	err := q.
		Order(col+" "+dir).
		Order("t.id "+dir). // stable tiebreak
		Limit(limit).
		Offset(offset).
		Pluck("t.id", &ids).
		Error
	return ids, err
}

// fetchTransferData builds one TransferData (the oneof) for a data type as a map
// keyed by transfer id.
func fetchTransferData(db *gorm.DB, dt inventory_iface.TransferListDataType, ids []uint64) (*inventory_iface.TransferData, error) {
	switch dt {
	case inventory_iface.TransferListDataType_TRANSFER_LIST_DATA_TYPE_GENERAL:
		out := map[uint64]*inventory_iface.TransferGeneralItem{}
		if len(ids) > 0 {
			var rows []struct {
				ID              uint64
				FromWarehouseID uint64
				FromName        string
				ToWarehouseID   uint64
				ToName          string
				Status          string
				CreatedAt       time.Time
				AcceptedAt      *time.Time
			}
			err := db.
				Table("inventory_transfers t").
				Joins("LEFT JOIN warehouses wf ON wf.id = t.from_warehouse_id").
				Joins("LEFT JOIN warehouses wt ON wt.id = t.to_warehouse_id").
				Select("t.id as id, t.from_warehouse_id as from_warehouse_id, coalesce(wf.name, '') as from_name, t.to_warehouse_id as to_warehouse_id, coalesce(wt.name, '') as to_name, t.status as status, t.created_at as created_at, t.accepted_at as accepted_at").
				Where("t.id IN ?", ids).
				Scan(&rows).
				Error
			if err != nil {
				return nil, err
			}
			for _, r := range rows {
				item := &inventory_iface.TransferGeneralItem{
					Id:                r.ID,
					FromWarehouseId:   r.FromWarehouseID,
					FromWarehouseName: r.FromName,
					ToWarehouseId:     r.ToWarehouseID,
					ToWarehouseName:   r.ToName,
					Status:            transferStatusToProto(inventory_models.TransferStatus(r.Status)),
					CreatedAt:         timestamppb.New(r.CreatedAt),
				}
				if r.AcceptedAt != nil {
					item.AcceptedAt = timestamppb.New(*r.AcceptedAt)
				}
				out[r.ID] = item
			}
		}
		return &inventory_iface.TransferData{Data: &inventory_iface.TransferData_General{General: &inventory_iface.TransferGeneralData{Data: out}}}, nil

	case inventory_iface.TransferListDataType_TRANSFER_LIST_DATA_TYPE_TOTAL:
		out := map[uint64]*inventory_iface.TransferTotalItem{}
		if len(ids) > 0 {
			var rows []struct {
				TransferID uint64
				ItemCount  int64
				Amount     float64
			}
			err := db.
				Table("inventory_transfer_items").
				Select("transfer_id, coalesce(sum(count),0) as item_count, coalesce(sum(count * price),0) as amount").
				Where("transfer_id IN ?", ids).
				Group("transfer_id").
				Scan(&rows).
				Error
			if err != nil {
				return nil, err
			}
			for _, r := range rows {
				out[r.TransferID] = &inventory_iface.TransferTotalItem{ItemCount: r.ItemCount, Amount: r.Amount}
			}
		}
		return &inventory_iface.TransferData{Data: &inventory_iface.TransferData_Total{Total: &inventory_iface.TransferTotalData{Data: out}}}, nil
	}
	return nil, nil
}

// transferStatusToProto / transferStatusFromProto map the stored status string to
// the proto enum and back.
func transferStatusToProto(s inventory_models.TransferStatus) inventory_iface.TransferStatus {
	switch s {
	case inventory_models.TransferPending:
		return inventory_iface.TransferStatus_TRANSFER_STATUS_PENDING
	case inventory_models.TransferAccepted:
		return inventory_iface.TransferStatus_TRANSFER_STATUS_ACCEPTED
	case inventory_models.TransferCanceled:
		return inventory_iface.TransferStatus_TRANSFER_STATUS_CANCELED
	default:
		return inventory_iface.TransferStatus_TRANSFER_STATUS_UNSPECIFIED
	}
}

func transferStatusFromProto(s inventory_iface.TransferStatus) inventory_models.TransferStatus {
	switch s {
	case inventory_iface.TransferStatus_TRANSFER_STATUS_PENDING:
		return inventory_models.TransferPending
	case inventory_iface.TransferStatus_TRANSFER_STATUS_ACCEPTED:
		return inventory_models.TransferAccepted
	case inventory_iface.TransferStatus_TRANSFER_STATUS_CANCELED:
		return inventory_models.TransferCanceled
	default:
		return ""
	}
}
