package inventory_service

import (
	"context"

	"connectrpc.com/connect"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/schema/services/inventory_iface/v1/inventory_ifaceconnect"
)

type inventoryServiceImpl struct{}

func NewInventoryService() *inventoryServiceImpl {
	return &inventoryServiceImpl{}
}

// Compile-time assertion that the impl satisfies the generated handler.
var _ inventory_ifaceconnect.InventoryServiceHandler = (*inventoryServiceImpl)(nil)

// Hello implements [inventory_ifaceconnect.InventoryServiceHandler].
func (s *inventoryServiceImpl) Hello(
	ctx context.Context,
	req *connect.Request[inventory_iface.HelloRequest],
) (*connect.Response[inventory_iface.HelloResponse], error) {
	return connect.NewResponse(&inventory_iface.HelloResponse{}), nil
}
