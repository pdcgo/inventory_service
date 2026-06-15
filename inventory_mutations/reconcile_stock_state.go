package inventory_mutations

import (
	"math"
	"time"

	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"gorm.io/gorm"
)

// ReconcileStockState drives the (product, warehouse) StockState to
// (targetCount, targetAmount), applying the difference as a
// STOCK_CHANGE_TYPE_ADJUSTMENT and writing a StockBatchLog. It is a no-op (no row
// written) when already at target. The diff is computed from the FOR-UPDATE-locked
// row, so it is correct under concurrency and safe to re-run. Used by the legacy
// reconcile — a synthetic adjustment with no source transaction, so
// TransactionID/UserID stay 0.
func ReconcileStockState(tx *gorm.DB, productID, warehouseID uint64, targetCount int64, targetAmount float64, now time.Time) error {
	st, err := lockOrCreateStockState(tx, productID, warehouseID, now)
	if err != nil {
		return err
	}

	diffCount := targetCount - st.StockReady
	diffAmount := targetAmount - st.StockReadyAmount
	if diffCount == 0 && math.Abs(diffAmount) < 1e-6 {
		return nil
	}

	st.StockReady = targetCount
	st.StockReadyAmount = targetAmount
	if err := tx.Model(&inventory_models.StockState{}).
		Where("id = ?", st.ID).
		Updates(map[string]interface{}{
			"stock_ready":        st.StockReady,
			"stock_ready_amount": st.StockReadyAmount,
			"updated_at":         now,
		}).Error; err != nil {
		return err
	}

	var unitPrice float64
	if diffCount != 0 {
		unitPrice = diffAmount / float64(diffCount)
	}
	log := inventory_models.StockBatchLog{
		ProductID:     productID,
		WarehouseID:   warehouseID,
		ChangeType:    inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ADJUSTMENT,
		Change:        diffCount,
		Price:         unitPrice,
		BalanceCount:  st.StockReady,
		BalanceAmount: st.StockReadyAmount,
		CreatedAt:     now,
	}
	return tx.Create(&log).Error
}
