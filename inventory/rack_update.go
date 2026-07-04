package inventory

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
)

// RackUpdate implements [inventory_ifaceconnect.InventoryServiceHandler].
//
// Renames a rack, scoped by warehouse_id. A missing/deleted/wrong-warehouse rack is NotFound.
func (s *inventoryServiceImpl) RackUpdate(
	ctx context.Context,
	req *connect.Request[inventory_iface.RackUpdateRequest],
) (*connect.Response[inventory_iface.RackUpdateResponse], error) {
	pay := req.Msg
	if pay.GetId() == 0 || pay.GetWarehouseId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id and warehouse_id are required"))
	}
	if pay.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}

	res := s.db.WithContext(ctx).
		Model(&inventory_models.Rack{}).
		Where("id = ? AND warehouse_id = ? AND deleted = false", pay.GetId(), pay.GetWarehouseId()).
		Update("name", pay.GetName())
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("rack not found"))
	}

	return connect.NewResponse(&inventory_iface.RackUpdateResponse{}), nil
}
