package inventory

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
)

// RackDelete implements [inventory_ifaceconnect.InventoryServiceHandler].
//
// Soft-deletes a rack (sets deleted=true), scoped by warehouse_id. A missing/already-deleted/
// wrong-warehouse rack is NotFound.
func (s *inventoryServiceImpl) RackDelete(
	ctx context.Context,
	req *connect.Request[inventory_iface.RackDeleteRequest],
) (*connect.Response[inventory_iface.RackDeleteResponse], error) {
	pay := req.Msg
	if pay.GetId() == 0 || pay.GetWarehouseId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id and warehouse_id are required"))
	}

	res := s.db.WithContext(ctx).
		Model(&inventory_models.Rack{}).
		Where("id = ? AND warehouse_id = ? AND deleted = false", pay.GetId(), pay.GetWarehouseId()).
		Update("deleted", true)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("rack not found"))
	}

	return connect.NewResponse(&inventory_iface.RackDeleteResponse{}), nil
}
