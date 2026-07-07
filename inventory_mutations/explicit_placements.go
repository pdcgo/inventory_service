package inventory_mutations

import (
	"time"

	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"gorm.io/gorm"
)

// PlacementDelta is one explicit per-rack stock movement (signed). Note is optional
// free text carried onto the placement log (e.g. an opname reason); "" writes none.
type PlacementDelta struct {
	ProductID uint64
	RackID    uint64
	Delta     int64
	Note      string
}

// ApplyExplicitPlacements applies request-supplied per-rack movements for a
// transaction: each delta locks (or creates) the StockPlacement row, applies the
// signed change, and writes a StockPlacementLog — mirroring the loop body of
// NewProcessStockPlacementLog, which cannot be used here because it re-derives
// racks from the legacy invertory_histories instead of taking explicit input.
func ApplyExplicitPlacements(
	tx *gorm.DB,
	txID, warehouseID uint64,
	changeType inventory_iface.StockChangeType,
	deltas []PlacementDelta,
	at time.Time,
) error {
	for _, d := range deltas {
		if d.ProductID == 0 || d.RackID == 0 || d.Delta == 0 {
			continue
		}

		pl, err := lockOrCreateStockPlacement(tx, d.ProductID, warehouseID, d.RackID, at)
		if err != nil {
			return err
		}

		pl.Count += d.Delta
		err = tx.
			Model(&inventory_models.StockPlacement{}).
			Where("id = ?", pl.ID).
			Updates(map[string]interface{}{
				"count":      pl.Count,
				"updated_at": at,
			}).
			Error
		if err != nil {
			return err
		}

		log := inventory_models.StockPlacementLog{
			ProductID:     d.ProductID,
			WarehouseID:   warehouseID,
			RackID:        d.RackID,
			TransactionID: txID,
			ChangeType:    changeType,
			Change:        d.Delta,
			BalanceCount:  pl.Count,
			Note:          d.Note,
			CreatedAt:     at,
		}
		err = tx.Create(&log).Error
		if err != nil {
			return err
		}
	}
	return nil
}

// ReverseTransactionPlacements nets the transaction's StockPlacementLog per
// (product, rack) and applies the reversing deltas, bringing its placement effect
// to zero. A net of zero (never placed, or already reversed) is a no-op — so a
// second reversal is idempotent.
func ReverseTransactionPlacements(
	tx *gorm.DB,
	txID uint64,
	changeType inventory_iface.StockChangeType,
	at time.Time,
) error {
	var rows []struct {
		ProductID   uint64
		WarehouseID uint64
		RackID      uint64
		Net         int64
	}
	err := tx.
		Model(&inventory_models.StockPlacementLog{}).
		Select("product_id, warehouse_id, rack_id, COALESCE(SUM(change), 0) as net").
		Where("transaction_id = ?", txID).
		Group("product_id, warehouse_id, rack_id").
		Scan(&rows).
		Error
	if err != nil {
		return err
	}

	for _, r := range rows {
		if r.Net == 0 {
			continue // already reversed
		}
		err = ApplyExplicitPlacements(tx, txID, r.WarehouseID, changeType,
			[]PlacementDelta{{ProductID: r.ProductID, RackID: r.RackID, Delta: -r.Net}}, at)
		if err != nil {
			return err
		}
	}
	return nil
}
