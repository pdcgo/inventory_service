package inventory

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	common "github.com/pdcgo/schema/services/common/v1"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// RestockLogList implements [inventory_ifaceconnect.InventoryServiceHandler]. It
// returns the restock document's audit trail (created/edited/accepted/problem/
// canceled), chronological, as a flat paginated list (ProductPlacementLog style).
func (s *inventoryServiceImpl) RestockLogList(
	ctx context.Context,
	req *connect.Request[inventory_iface.RestockLogListRequest],
) (*connect.Response[inventory_iface.RestockLogListResponse], error) {
	pay := req.Msg
	if pay.GetRestockId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("restock_id is required"))
	}
	if pay.GetPage() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("page is required"))
	}
	db := s.db.WithContext(ctx)

	// The restock must exist (and match the optional warehouse scope).
	q := db.Where("id = ?", pay.GetRestockId())
	if pay.GetWarehouseId() > 0 {
		q = q.Where("warehouse_id = ?", pay.GetWarehouseId())
	}
	var restock inventory_models.InventoryRestock
	err := q.First(&restock).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("restock not found"))
		}
		return nil, err
	}

	var total int64
	err = db.
		Model(&inventory_models.InventoryRestockLog{}).
		Where("restock_id = ?", restock.ID).
		Count(&total).
		Error
	if err != nil {
		return nil, err
	}

	limit, offset := rackPageLimitOffset(pay.GetPage())
	var rows []inventory_models.InventoryRestockLog
	err = db.
		Where("restock_id = ?", restock.ID).
		Order("id ASC"). // chronological (Timeline order)
		Limit(limit).
		Offset(offset).
		Find(&rows).
		Error
	if err != nil {
		return nil, err
	}

	totalPage := (total + int64(limit) - 1) / int64(limit)
	if totalPage < 1 {
		totalPage = 1
	}
	resp := &inventory_iface.RestockLogListResponse{
		Logs: make([]*inventory_iface.RestockLogItem, 0, len(rows)),
		PageInfo: &common.PageInfo{
			CurrentPage: pay.GetPage().GetPage(),
			TotalPage:   totalPage,
			TotalItems:  total,
		},
	}
	for _, r := range rows {
		resp.Logs = append(resp.Logs, &inventory_iface.RestockLogItem{
			Action:      restockLogActionToProto(r.Action),
			Note:        r.Note,
			CreatedById: r.CreatedByID,
			CreatedAt:   timestamppb.New(r.CreatedAt),
		})
	}

	return connect.NewResponse(resp), nil
}

// restockLogActionToProto maps the stored action string to the proto enum.
func restockLogActionToProto(s string) inventory_iface.RestockLogAction {
	switch s {
	case "created":
		return inventory_iface.RestockLogAction_RESTOCK_LOG_ACTION_CREATED
	case "edited":
		return inventory_iface.RestockLogAction_RESTOCK_LOG_ACTION_EDITED
	case "accepted":
		return inventory_iface.RestockLogAction_RESTOCK_LOG_ACTION_ACCEPTED
	case "problem":
		return inventory_iface.RestockLogAction_RESTOCK_LOG_ACTION_PROBLEM
	case "canceled":
		return inventory_iface.RestockLogAction_RESTOCK_LOG_ACTION_CANCELED
	default:
		return inventory_iface.RestockLogAction_RESTOCK_LOG_ACTION_UNSPECIFIED
	}
}
