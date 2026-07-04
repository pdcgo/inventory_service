package inventory

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"gorm.io/gorm/clause"
)

// ProductConfigUpdate implements [inventory_ifaceconnect.InventoryServiceHandler].
//
// Upserts the (product, warehouse) config (one row per pair).
func (s *inventoryServiceImpl) ProductConfigUpdate(
	ctx context.Context,
	req *connect.Request[inventory_iface.ProductConfigUpdateRequest],
) (*connect.Response[inventory_iface.ProductConfigUpdateResponse], error) {
	pay := req.Msg
	if pay.GetProductId() == 0 || pay.GetWarehouseId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("product_id and warehouse_id are required"))
	}
	if pay.GetQueueType() == inventory_iface.QueueType_QUEUE_TYPE_UNSPECIFIED {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("queue_type is required"))
	}
	if pay.GetPlacementPicking() == inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_UNSPECIFIED {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("placement_picking is required"))
	}

	now := time.Now()
	cfg := inventory_models.ProductConfig{
		ProductID:        pay.GetProductId(),
		WarehouseID:      pay.GetWarehouseId(),
		QueueType:        pay.GetQueueType(),
		PlacementPicking: pay.GetPlacementPicking(),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	err := s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "product_id"}, {Name: "warehouse_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"queue_type", "placement_picking", "updated_at"}),
		}).
		Create(&cfg).Error
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&inventory_iface.ProductConfigUpdateResponse{}), nil
}
