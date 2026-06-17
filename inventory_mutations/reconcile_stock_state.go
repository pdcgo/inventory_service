package inventory_mutations

import (
	"log/slog"
	"math"
	"time"

	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
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

	// slog.Info("state_stock",
	// 	"product_id", st.ProductID,
	// 	"warehouse_id", st.WarehouseID,
	// 	"true_count", st.StockReady,
	// 	"true_amount", st.StockReadyAmount,
	// )

	diffCount := targetCount - st.StockReady
	diffAmount := targetAmount - st.StockReadyAmount
	if diffCount == 0 && math.Abs(diffAmount) < 1e-6 {
		return nil
	}

	slog.Warn("fixing diff",
		"product_id", st.ProductID,
		"warehouse_id", st.WarehouseID,
		"true_count", diffCount,
		"true_amount", diffAmount,
	)

	process := NewProcessStockBatchLog(tx)
	_, err = process(&inventory_iface.StockChange{
		At:            timestamppb.New(now),
		WarehouseId:   warehouseID,
		UserId:        1,
		TransactionId: 0,
		Changes: []*inventory_iface.ChangeItem{
			{
				ProductId:    productID,
				ChangeCount:  diffCount,
				ChangeAmount: diffAmount,
			},
		},
		Change: &inventory_iface.StockChange_Adjustment{
			Adjustment: &inventory_iface.Adjustment{},
		},
	})
	if err != nil {
		return err
	}

	return nil
}
