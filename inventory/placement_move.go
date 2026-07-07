package inventory

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	"github.com/pdcgo/inventory_service/inventory_mutations"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"gorm.io/gorm"
)

// PlacementMove implements [inventory_ifaceconnect.InventoryServiceHandler].
//
// Intra-warehouse rack-to-rack stock move (multi-item, partial counts). Net-zero
// for the warehouse: StockState/StockBatch are untouched; each item becomes a
// -count/+count MOVE placement-log pair carrying the request note.
func (s *inventoryServiceImpl) PlacementMove(
	ctx context.Context,
	req *connect.Request[inventory_iface.PlacementMoveRequest],
) (*connect.Response[inventory_iface.PlacementMoveResponse], error) {
	pay := req.Msg

	if pay.GetWarehouseId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("warehouse_id is required"))
	}
	if len(pay.GetPlacements()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("placements are required"))
	}

	type moveKey struct {
		productID  uint64
		fromRackID uint64
		toRackID   uint64
	}
	seen := map[moveKey]bool{}
	rackIDs := []uint64{}
	deltas := []inventory_mutations.PlacementDelta{}
	for _, item := range pay.GetPlacements() {
		if item.GetProductId() == 0 || item.GetFromRackId() == 0 || item.GetToRackId() == 0 || item.GetCount() <= 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("each placement needs product_id, from_rack_id, to_rack_id and count > 0"))
		}
		if item.GetFromRackId() == item.GetToRackId() {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("from and to rack must differ"))
		}

		key := moveKey{item.GetProductId(), item.GetFromRackId(), item.GetToRackId()}
		if seen[key] {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("duplicate placement item"))
		}
		seen[key] = true

		rackIDs = append(rackIDs, item.GetFromRackId(), item.GetToRackId())
		deltas = append(deltas,
			inventory_mutations.PlacementDelta{
				ProductID: item.GetProductId(),
				RackID:    item.GetFromRackId(),
				Delta:     -item.GetCount(),
			},
			inventory_mutations.PlacementDelta{
				ProductID: item.GetProductId(),
				RackID:    item.GetToRackId(),
				Delta:     item.GetCount(),
			},
		)
	}

	now := time.Now()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// every from/to rack must be a live rack of the warehouse.
		var rackCount int64
		err := tx.
			Model(&inventory_models.Rack{}).
			Where("id IN ? AND warehouse_id = ? AND deleted = false", rackIDs, pay.GetWarehouseId()).
			Distinct("id").
			Count(&rackCount).
			Error
		if err != nil {
			return err
		}
		if rackCount != int64(len(uniqueIDs(rackIDs))) {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("placement rack is not a live rack of the warehouse"))
		}

		err = inventory_mutations.ApplyPlacementMove(tx, pay.GetWarehouseId(), deltas, pay.GetNote(), now)
		if err != nil {
			var insErr *inventory_mutations.ErrInsufficientPlacement
			if errors.As(err, &insErr) {
				return connect.NewError(connect.CodeFailedPrecondition, err)
			}
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&inventory_iface.PlacementMoveResponse{}), nil
}
