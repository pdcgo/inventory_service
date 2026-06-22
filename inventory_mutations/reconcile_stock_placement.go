package inventory_mutations

import (
	"log/slog"
	"time"

	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"gorm.io/gorm"
)

// ReconcileStockPlacement drives the (product, warehouse, rack) placement to
// targetCount, applying the difference directly as a STOCK_CHANGE_TYPE_ADJUSTMENT
// and writing a StockPlacementLog. It is a no-op (no row written) when already at
// target. The diff is computed from the FOR-UPDATE-locked row, so it is correct
// under concurrency and safe to re-run. Used by the legacy reconcile — a synthetic
// adjustment with no source transaction, so TransactionID/UserID stay 0.
//
// Unlike the live push path it does not go through NewProcessStockPlacementLog
// (which re-derives per-rack moves from invertory_histories by tx_id); the rack and
// delta are known here, so they are applied straight to the row.
func ReconcileStockPlacement(tx *gorm.DB, productID, warehouseID, rackID uint64, targetCount int64, now time.Time) error {
	pl, err := lockOrCreateStockPlacement(tx, productID, warehouseID, rackID, now)
	if err != nil {
		return err
	}

	deltaCount := targetCount - pl.Count
	if deltaCount == 0 {
		return nil
	}

	slog.Warn("fixing diff",
		"product_id", productID,
		"warehouse_id", warehouseID,
		"rack_id", rackID,
		"delta_count", deltaCount,
	)

	pl.Count = targetCount
	if err := tx.Model(&inventory_models.StockPlacement{}).
		Where("id = ?", pl.ID).
		Updates(map[string]interface{}{
			"count":      pl.Count,
			"updated_at": now,
		}).Error; err != nil {
		return err
	}

	return tx.Create(&inventory_models.StockPlacementLog{
		ProductID:    productID,
		WarehouseID:  warehouseID,
		RackID:       rackID,
		ChangeType:   inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ADJUSTMENT,
		Change:       deltaCount,
		BalanceCount: pl.Count,
		CreatedAt:    now,
	}).Error
}
