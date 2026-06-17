package inventory_mutations

import (
	"log/slog"
	"time"

	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
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

	deltaCount := targetCount - pl.Count
	if deltaCount == 0 {
		return nil
	}

	slog.Warn("fixing diff",
		"product_id", productID,
		"warehouse_id", warehouseID,
		"delta_count", deltaCount,
	)

	process := NewProcessStockPlacementLog(tx)
	_, err = process(&inventory_iface.StockChange{
		At:          timestamppb.New(now),
		WarehouseId: warehouseID,
		UserId:      1,
		Changes: []*inventory_iface.ChangeItem{
			{
				ProductId:   productID,
				ChangeCount: deltaCount,
			},
		},
		Change: &inventory_iface.StockChange_Adjustment{},
	})

	return err
}
