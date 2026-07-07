package inventory_mutations

import (
	"fmt"
	"sort"
	"time"

	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"gorm.io/gorm"
)

// ErrInsufficientPlacement reports the first move leg that would drive a rack's
// placement balance negative.
type ErrInsufficientPlacement struct {
	ProductID uint64
	RackID    uint64
	Have      int64
	Need      int64
}

func (e *ErrInsufficientPlacement) Error() string {
	return fmt.Sprintf("insufficient stock on rack %d for product %d: have %d, need %d",
		e.RackID, e.ProductID, e.Have, e.Need)
}

// ApplyPlacementMove applies the legs of an intra-warehouse rack-to-rack move.
// Each leg locks (or creates) the StockPlacement row, applies the signed delta —
// guarding outgoing legs against a negative balance — and writes a MOVE
// StockPlacementLog carrying note. Legs are lock-ordered by (rack_id, product_id)
// ascending (stable) so concurrent moves cannot deadlock each other, and the
// leading rack_id key follows the ascending-rack convention of
// NewProcessStockPlacementLog (which orders by rack_id only, without the product
// tiebreak). Chain moves (A→B then B→C in one request) therefore depend on request
// order for the guard. TransactionID stays 0 — a move mints no InventoryTransaction
// (net-zero for the warehouse).
func ApplyPlacementMove(
	tx *gorm.DB,
	warehouseID uint64,
	deltas []PlacementDelta,
	note string,
	at time.Time,
) error {
	sort.SliceStable(deltas, func(i, j int) bool {
		if deltas[i].RackID != deltas[j].RackID {
			return deltas[i].RackID < deltas[j].RackID
		}
		return deltas[i].ProductID < deltas[j].ProductID
	})

	for _, d := range deltas {
		if d.ProductID == 0 || d.RackID == 0 || d.Delta == 0 {
			continue
		}

		pl, err := lockOrCreateStockPlacement(tx, d.ProductID, warehouseID, d.RackID, at)
		if err != nil {
			return err
		}

		if d.Delta < 0 && pl.Count+d.Delta < 0 {
			return &ErrInsufficientPlacement{
				ProductID: d.ProductID,
				RackID:    d.RackID,
				Have:      pl.Count,
				Need:      -d.Delta,
			}
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
			ProductID:    d.ProductID,
			WarehouseID:  warehouseID,
			RackID:       d.RackID,
			ChangeType:   inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_MOVE,
			Change:       d.Delta,
			BalanceCount: pl.Count,
			Note:         note,
			CreatedAt:    at,
		}
		err = tx.Create(&log).Error
		if err != nil {
			return err
		}
	}
	return nil
}
