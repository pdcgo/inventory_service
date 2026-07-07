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

// TransferAccept implements [inventory_ifaceconnect.InventoryServiceHandler]. The
// destination receives the goods: it applies the IN leg (stock enters the destination
// warehouse) and mints the StockBatch there at the transfer's derived prices, then
// flips the document to accepted. Re-accepting is a no-op.
func (s *inventoryServiceImpl) TransferAccept(
	ctx context.Context,
	req *connect.Request[inventory_iface.TransferAcceptRequest],
) (*connect.Response[inventory_iface.TransferAcceptResponse], error) {
	pay := req.Msg
	if pay.GetTransferId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("transfer_id is required"))
	}

	err := s.db.
		WithContext(ctx).
		Transaction(func(tx *gorm.DB) error {
			q := tx.Where("id = ?", pay.GetTransferId())
			if pay.GetWarehouseId() > 0 {
				q = q.Where("to_warehouse_id = ?", pay.GetWarehouseId())
			}
			var transfer inventory_models.InventoryTransfer
			err := q.First(&transfer).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return connect.NewError(connect.CodeNotFound, errors.New("transfer not found"))
				}
				return err
			}
			if transfer.Status == inventory_models.TransferAccepted {
				return nil // idempotent
			}
			if transfer.Status == inventory_models.TransferCanceled {
				return connect.NewError(connect.CodeFailedPrecondition, errors.New("transfer is canceled"))
			}

			items, err := loadTransferItems(tx, transfer.ID)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				return connect.NewError(connect.CodeFailedPrecondition, errors.New("transfer has no items"))
			}

			// Placements (REQUIRED): every received unit must be placed on a live rack
			// of the destination warehouse — placed == count per product. Validated
			// before any stock mutation so a bad accept rolls back untouched.
			received := map[uint64]int64{}
			for _, it := range items {
				received[it.GetProductId()] += it.GetCount()
			}
			placeDeltas := []inventory_mutations.PlacementDelta{}
			placedPerProduct := map[uint64]int64{}
			rackIDs := []uint64{}
			for _, pl := range pay.GetPlacements().GetItems() {
				if pl.GetProductId() == 0 || pl.GetRackId() == 0 || pl.GetCount() <= 0 {
					return connect.NewError(connect.CodeInvalidArgument, errors.New("each placement needs product_id, rack_id and count > 0"))
				}
				if _, ok := received[pl.GetProductId()]; !ok {
					return connect.NewError(connect.CodeInvalidArgument, errors.New("placement product is not part of the transfer"))
				}
				placedPerProduct[pl.GetProductId()] += pl.GetCount()
				rackIDs = append(rackIDs, pl.GetRackId())
				placeDeltas = append(placeDeltas, inventory_mutations.PlacementDelta{
					ProductID: pl.GetProductId(),
					RackID:    pl.GetRackId(),
					Delta:     pl.GetCount(),
				})
			}

			if len(rackIDs) > 0 {
				var rackCount int64
				err = tx.
					Model(&inventory_models.Rack{}).
					Where("id IN ? AND warehouse_id = ? AND deleted = false", rackIDs, transfer.ToWarehouseID).
					Distinct("id").
					Count(&rackCount).
					Error
				if err != nil {
					return err
				}
				if rackCount != int64(len(uniqueIDs(rackIDs))) {
					return connect.NewError(connect.CodeInvalidArgument, errors.New("placement rack is not a live rack of the destination warehouse"))
				}
			}

			for pid, count := range received {
				if placedPerProduct[pid] != count {
					return connect.NewError(connect.CodeInvalidArgument, errors.New("every received unit must be placed on a rack (placed == count per product)"))
				}
			}

			now := time.Now()

			// IN leg: stock enters the destination at the derived prices.
			inTxID, err := applyTransferLeg(tx, inventory_models.InvTxTransferIn,
				transfer.TeamID, transfer.ToWarehouseID, items, 1, now)
			if err != nil {
				return err
			}

			// The destination gains a batch for the incoming goods.
			err = mintTransactionBatches(tx, inTxID, transfer.ToWarehouseID, items, now)
			if err != nil {
				return err
			}

			// Place the received goods into the destination racks.
			err = inventory_mutations.ApplyExplicitPlacements(tx, inTxID, transfer.ToWarehouseID,
				inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER, placeDeltas, now)
			if err != nil {
				return err
			}

			return tx.
				Model(&inventory_models.InventoryTransfer{}).
				Where("id = ?", transfer.ID).
				Updates(map[string]interface{}{
					"status":            inventory_models.TransferAccepted,
					"in_transaction_id": inTxID,
					"accepted_at":       now,
					"updated_at":        now,
				}).
				Error
		})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&inventory_iface.TransferAcceptResponse{}), nil
}

// loadTransferItems reads the transfer's item rows as the shared TransactionItem
// shape so the leg/batch helpers can be reused as-is.
func loadTransferItems(tx *gorm.DB, transferID uint64) ([]*inventory_iface.TransactionItem, error) {
	var rows []inventory_models.InventoryTransferItem
	err := tx.
		Where("transfer_id = ?", transferID).
		Order("id ASC").
		Find(&rows).
		Error
	if err != nil {
		return nil, err
	}
	items := make([]*inventory_iface.TransactionItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, &inventory_iface.TransactionItem{
			ProductId: r.ProductID,
			Count:     r.Count,
			Price:     r.Price,
		})
	}
	return items, nil
}
