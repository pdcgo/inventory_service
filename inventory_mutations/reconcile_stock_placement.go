package inventory_mutations

import (
	"time"

	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"gorm.io/gorm"
)

// ReconcileStockPlacement drives the (product, warehouse, rack) placement to
// targetCount, applying the difference as a STOCK_CHANGE_TYPE_ADJUSTMENT and
// writing a StockPlacementLog. It is a no-op (no row written) when the placement
// is already at target. Used by the legacy reconcile — a synthetic adjustment
// with no source transaction, so TransactionID/UserID stay 0.
func ReconcileStockPlacement(tx *gorm.DB, productID, warehouseID, rackID uint64, targetCount int64, now time.Time) error {
	pl, err := lockOrCreateStockPlacement(tx, productID, warehouseID, rackID, now)
	if err != nil {
		return err
	}

	delta := targetCount - pl.Count
	if delta == 0 {
		return nil
	}

	pl.Count = targetCount
	if err := tx.Model(&inventory_models.StockPlacement{}).
		Where("id = ?", pl.ID).
		Updates(map[string]interface{}{
			"count":      pl.Count,
			"updated_at": now,
		}).Error; err != nil {
		return err
	}

	log := inventory_models.StockPlacementLog{
		ProductID:    productID,
		WarehouseID:  warehouseID,
		RackID:       rackID,
		ChangeType:   inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ADJUSTMENT,
		Change:       delta,
		BalanceCount: pl.Count,
		CreatedAt:    now,
	}
	return tx.Create(&log).Error
}
