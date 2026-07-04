package inventory

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
)

// RackCreate implements [inventory_ifaceconnect.InventoryServiceHandler].
//
// Creates a rack in the given warehouse.
func (s *inventoryServiceImpl) RackCreate(
	ctx context.Context,
	req *connect.Request[inventory_iface.RackCreateRequest],
) (*connect.Response[inventory_iface.RackCreateResponse], error) {
	pay := req.Msg
	if pay.GetWarehouseId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("warehouse_id is required"))
	}
	if pay.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}

	rack := inventory_models.Rack{
		WarehouseID: pay.GetWarehouseId(),
		Name:        pay.GetName(),
	}
	if err := s.
		db.
		WithContext(ctx).
		Save(&rack).
		Error; err != nil {
		return nil, err
	}

	return connect.NewResponse(&inventory_iface.RackCreateResponse{Id: uint64(rack.ID)}), nil
}
