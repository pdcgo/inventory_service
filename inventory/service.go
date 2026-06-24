package inventory

import (
	"context"

	"connectrpc.com/connect"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/schema/services/inventory_iface/v1/inventory_ifaceconnect"
	"gorm.io/gorm"
)

type inventoryServiceImpl struct {
	db *gorm.DB
}

// ProductBatchList, ProductPlacementList, and ProductPlacementLog are implemented
// in product_batch_list.go / product_placement_list.go / product_placement_log.go.

// StockMovement implements [inventory_ifaceconnect.InventoryServiceHandler].
// func (s *inventoryServiceImpl) StockMovement(context.Context, *connect.Request[inventory_iface.StockMovementRequest]) (*connect.Response[inventory_iface.StockMovementResponse], error) {
// 	panic("unimplemented")
// }

// Order implements [inventory_ifaceconnect.InventoryServiceHandler].
func (s *inventoryServiceImpl) Order(context.Context, *connect.Request[inventory_iface.OrderRequest]) (*connect.Response[inventory_iface.OrderResponse], error) {
	panic("unimplemented")
}

func NewInventoryService(db *gorm.DB) *inventoryServiceImpl {
	return &inventoryServiceImpl{db: db}
}

// Compile-time assertion that the impl satisfies the generated handler.
var _ inventory_ifaceconnect.InventoryServiceHandler = (*inventoryServiceImpl)(nil)
