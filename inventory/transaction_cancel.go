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

// TransactionCancel implements [inventory_ifaceconnect.InventoryServiceHandler]. It
// reverses a transaction created by TransactionCreate: it flips the transaction to
// canceled, reverses its StockState effect (writing a reversing StockBatchLog via
// reconstructCancel), and voids any StockBatch it minted. Idempotent — a second
// cancel is a no-op.
func (s *inventoryServiceImpl) TransactionCancel(
	ctx context.Context,
	req *connect.Request[inventory_iface.TransactionCancelRequest],
) (*connect.Response[inventory_iface.TransactionCancelResponse], error) {
	pay := req.Msg
	if pay.GetTransactionId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("transaction_id is required"))
	}

	err := s.db.
		WithContext(ctx).
		Transaction(func(tx *gorm.DB) error {
			q := tx.Where("id = ?", pay.GetTransactionId())
			if pay.GetWarehouseId() > 0 {
				q = q.Where("warehouse_id = ?", pay.GetWarehouseId())
			}
			var trx inventory_models.InventoryTransaction
			err := q.First(&trx).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return connect.NewError(connect.CodeNotFound, errors.New("transaction not found"))
				}
				return err
			}
			if trx.Status == inventory_models.InvTxCanceled {
				return nil // idempotent
			}

			// Reverse StockState + write reversing StockBatchLog. reconstructCancel's
			// placement step re-derives racks from invertory_histories by tx_id, of
			// which an inventory-owned transaction has none — so it no-ops.
			err = reconstructCancel(tx, pay.GetTransactionId(),
				"transaction %d not applied",
				func() *inventory_iface.StockChange {
					return &inventory_iface.StockChange{
						Change: &inventory_iface.StockChange_Adjustment{Adjustment: &inventory_iface.Adjustment{}},
					}
				})
			if err != nil {
				return err
			}

			// Void the batch(es) minted for an inbound transaction.
			err = tx.
				Where("inbound_id = ?", pay.GetTransactionId()).
				Delete(&inventory_models.StockBatch{}).
				Error
			if err != nil {
				return err
			}

			now := time.Now()
			err = tx.
				Model(&inventory_models.InventoryTransaction{}).
				Where("id = ?", trx.ID).
				Updates(map[string]interface{}{
					"status":      inventory_models.InvTxCanceled,
					"canceled_at": now,
				}).
				Error
			if err != nil {
				return err
			}
			return nil
		})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&inventory_iface.TransactionCancelResponse{}), nil
}
