package inventory

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"gorm.io/gorm"
)

// ProductConfig implements [inventory_ifaceconnect.InventoryServiceHandler].
//
// Returns the (product, warehouse) config, or the defaults (FIFO price queue + smaller-
// quantity placement picking) with configured=false when no config is stored.
func (s *inventoryServiceImpl) ProductConfig(
	ctx context.Context,
	req *connect.Request[inventory_iface.ProductConfigRequest],
) (*connect.Response[inventory_iface.ProductConfigResponse], error) {
	pay := req.Msg
	if pay.GetProductId() == 0 || pay.GetWarehouseId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("product_id and warehouse_id are required"))
	}

	// default config
	resp := &inventory_iface.ProductConfigResponse{
		ProductId:        pay.GetProductId(),
		WarehouseId:      pay.GetWarehouseId(),
		QueueType:        inventory_iface.QueueType_QUEUE_TYPE_FIFO,
		PlacementPicking: inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_SMALLER,
		Configured:       false,
	}

	var cfg inventory_models.ProductConfig
	err := s.db.WithContext(ctx).
		Where("product_id = ? AND warehouse_id = ?", pay.GetProductId(), pay.GetWarehouseId()).
		First(&cfg).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return connect.NewResponse(resp), nil // defaults
		}
		return nil, err
	}

	resp.QueueType = cfg.QueueType
	resp.PlacementPicking = cfg.PlacementPicking
	resp.Configured = true
	return connect.NewResponse(resp), nil
}
