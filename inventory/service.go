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

// ProductBatchList implements [inventory_ifaceconnect.InventoryServiceHandler].
func (s *inventoryServiceImpl) ProductBatchList(context.Context, *connect.Request[inventory_iface.ProductBatchListRequest]) (*connect.Response[inventory_iface.ProductBatchListResponse], error) {
	panic("unimplemented")
}

// ProductPlacementList implements [inventory_ifaceconnect.InventoryServiceHandler].
func (s *inventoryServiceImpl) ProductPlacementList(context.Context, *connect.Request[inventory_iface.ProductPlacementListRequest]) (*connect.Response[inventory_iface.ProductPlacementListResponse], error) {
	panic("unimplemented")
}

// ProductPlacementLog implements [inventory_ifaceconnect.InventoryServiceHandler].
func (s *inventoryServiceImpl) ProductPlacementLog(context.Context, *connect.Request[inventory_iface.ProductPlacementLogRequest]) (*connect.Response[inventory_iface.ProductPlacementLogResponse], error) {
	panic("unimplemented")
}

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
