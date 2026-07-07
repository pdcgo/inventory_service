package inventory

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"gorm.io/gorm"
)

// TransferCancel implements [inventory_ifaceconnect.InventoryServiceHandler]. It
// cancels an in-transit (pending) transfer: the OUT leg is reversed so the stock
// returns to the source warehouse and the document flips to canceled. An accepted
// transfer cannot be canceled (send a new transfer in the opposite direction
// instead), matching the legacy semantics where a cancel never touches the IN leg.
// A second cancel is a no-op.
func (s *inventoryServiceImpl) TransferCancel(
	ctx context.Context,
	req *connect.Request[inventory_iface.TransferCancelRequest],
) (*connect.Response[inventory_iface.TransferCancelResponse], error) {
	pay := req.Msg
	if pay.GetTransferId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("transfer_id is required"))
	}

	err := s.db.
		WithContext(ctx).
		Transaction(func(tx *gorm.DB) error {
			q := tx.Where("id = ?", pay.GetTransferId())
			if pay.GetWarehouseId() > 0 {
				q = q.Where("from_warehouse_id = ?", pay.GetWarehouseId())
			}
			var transfer inventory_models.InventoryTransfer
			err := q.First(&transfer).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return connect.NewError(connect.CodeNotFound, errors.New("transfer not found"))
				}
				return err
			}
			if transfer.Status == inventory_models.TransferCanceled {
				return nil // idempotent
			}
			if transfer.Status == inventory_models.TransferAccepted {
				return connect.NewError(connect.CodeFailedPrecondition, errors.New("an accepted transfer cannot be canceled"))
			}

			// Reverse the OUT leg: the source gets its stock back (Transfer reason,
			// same as the leg was logged under, so the reversal nets it to zero).
			err = reconstructCancel(tx, transfer.OutTransactionID,
				"transfer %d not applied",
				func() *inventory_iface.StockChange {
					return &inventory_iface.StockChange{
						Change: &inventory_iface.StockChange_Transfer{Transfer: &inventory_iface.Transfer{}},
					}
				})
			if err != nil {
				return err
			}

			now := time.Now()
			err = tx.
				Model(&inventory_models.InventoryTransaction{}).
				Where("id = ?", transfer.OutTransactionID).
				Updates(map[string]interface{}{
					"status":      inventory_models.InvTxCanceled,
					"canceled_at": now,
				}).
				Error
			if err != nil {
				return err
			}

			return tx.
				Model(&inventory_models.InventoryTransfer{}).
				Where("id = ?", transfer.ID).
				Updates(map[string]interface{}{
					"status":      inventory_models.TransferCanceled,
					"canceled_at": now,
					"updated_at":  now,
				}).
				Error
		})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&inventory_iface.TransferCancelResponse{}), nil
}
