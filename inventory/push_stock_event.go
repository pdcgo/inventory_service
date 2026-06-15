package inventory

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"gorm.io/gorm"
)

// PushStockEvent implements [inventory_ifaceconnect.InventoryServiceHandler].
//
// It is the RPC counterpart of the Pub/Sub push handler: it applies the warehouse
// StockEvent to inventory state in one transaction via ProcessStockEvent.
func (s *inventoryServiceImpl) PushStockEvent(
	ctx context.Context,
	req *connect.Request[inventory_iface.PushStockEventRequest],
) (*connect.Response[inventory_iface.PushStockEventResponse], error) {
	event := req.Msg.GetEvent()
	if event == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("event is required"))
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return ProcessStockEvent(tx, event)
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&inventory_iface.PushStockEventResponse{}), nil
}
