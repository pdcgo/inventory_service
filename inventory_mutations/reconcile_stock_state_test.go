package inventory_mutations_test

import (
	"testing"
	"time"

	"github.com/pdcgo/inventory_service/inventory_models"
	"github.com/pdcgo/inventory_service/inventory_mutations"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestReconcileStockState(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "reconcile stock state",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(
					&inventory_models.StockState{},
					&inventory_models.StockBatchLog{},
				))

				at := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)

				// product 1 already has 10/100; product 2 has no state yet.
				assert.NoError(t, db.Create(&inventory_models.StockState{
					ProductID: 1, WarehouseID: 9, StockReady: 10, StockReadyAmount: 100, CreatedAt: at, UpdatedAt: at,
				}).Error)

				stateOf := func(productID uint64) inventory_models.StockState {
					var s inventory_models.StockState
					assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ?", productID, uint64(9)).First(&s).Error)
					return s
				}
				logCount := func() int64 {
					var n int64
					assert.NoError(t, db.Model(&inventory_models.StockBatchLog{}).Count(&n).Error)
					return n
				}

				t.Run("updates an existing state down to target", func(t *testing.T) {
					assert.NoError(t, inventory_mutations.ReconcileStockState(db, 1, 9, 7, 70, at))

					s := stateOf(1)
					assert.Equal(t, int64(7), s.StockReady)
					assert.Equal(t, float64(70), s.StockReadyAmount)

					var l inventory_models.StockBatchLog
					assert.NoError(t, db.Where("product_id = ?", uint64(1)).First(&l).Error)
					assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ADJUSTMENT, l.ChangeType)
					assert.Equal(t, int64(-3), l.Change) // 7 - 10
					assert.Equal(t, int64(7), l.BalanceCount)
					assert.Equal(t, float64(70), l.BalanceAmount)
					assert.Equal(t, float64(10), l.Price) // -30 / -3
				})

				t.Run("creates an absent state at target", func(t *testing.T) {
					assert.NoError(t, inventory_mutations.ReconcileStockState(db, 2, 9, 5, 50, at))

					s := stateOf(2)
					assert.Equal(t, int64(5), s.StockReady)
					assert.Equal(t, float64(50), s.StockReadyAmount)

					var l inventory_models.StockBatchLog
					assert.NoError(t, db.Where("product_id = ?", uint64(2)).First(&l).Error)
					assert.Equal(t, int64(5), l.Change)
					assert.Equal(t, int64(5), l.BalanceCount)
				})

				t.Run("no-op when already at target", func(t *testing.T) {
					before := logCount()
					assert.NoError(t, inventory_mutations.ReconcileStockState(db, 1, 9, 7, 70, at))
					assert.Equal(t, int64(7), stateOf(1).StockReady)
					assert.Equal(t, before, logCount()) // no new log
				})
			})
		},
	)
}
