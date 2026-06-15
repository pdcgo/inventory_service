package main

import (
	"testing"

	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestSyncLegacy(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "sync legacy stock",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(
					&db_models.InvertoryHistory{},
					&inventory_models.StockState{},
					&inventory_models.StockBatchLog{},
				))

				// product 5 in warehouse 9, two variants → same StockState grain.
				sku1, err := db_models.NewSkuID(&db_models.SkuData{WarehouseID: 9, TeamID: 1, ProductID: 5, VariantID: 1})
				assert.NoError(t, err)
				sku2, err := db_models.NewSkuID(&db_models.SkuData{WarehouseID: 9, TeamID: 1, ProductID: 5, VariantID: 2})
				assert.NoError(t, err)

				outboundTx := uint(999)
				// invertory_histories.count is stored negative for on-hand stock;
				// unit value = price + ext_price = 10.
				assert.NoError(t, db.Create(&[]db_models.InvertoryHistory{
					// variant 1: on-hand 5 + 3 = 8 (value 80)
					{SkuID: sku1, WarehouseID: 9, TeamID: 1, Count: -5, Price: 8, ExtPrice: 2},
					{SkuID: sku1, WarehouseID: 9, TeamID: 1, Count: -3, Price: 8, ExtPrice: 2},
					// outbound row (tx_id set) must be ignored by the tx_id IS NULL filter
					{SkuID: sku1, WarehouseID: 9, TeamID: 1, Count: 2, Price: 8, ExtPrice: 2, TxID: &outboundTx},
					// variant 2: on-hand 2 (value 20) — aggregates onto product 5 / wh 9
					{SkuID: sku2, WarehouseID: 9, TeamID: 1, Count: -2, Price: 8, ExtPrice: 2},
				}).Error)

				// StockState currently lags the legacy total (3 / 30 vs 10 / 100).
				assert.NoError(t, db.Create(&inventory_models.StockState{
					ProductID: 5, WarehouseID: 9, StockReady: 3, StockReadyAmount: 30,
				}).Error)

				assert.NoError(t, NewSyncLegacyFunc(db)(t.Context(), nil))

				// StockState reconciled to the aggregated legacy on-hand.
				var st inventory_models.StockState
				assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ?", uint64(5), uint64(9)).First(&st).Error)
				assert.Equal(t, int64(10), st.StockReady)
				assert.Equal(t, float64(100), st.StockReadyAmount)

				// exactly one adjustment log, carrying the signed diff.
				logs := []*inventory_models.StockBatchLog{}
				assert.NoError(t, db.Where("product_id = ?", uint64(5)).Find(&logs).Error)
				assert.Len(t, logs, 1)
				assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ADJUSTMENT, logs[0].ChangeType)
				assert.Equal(t, int64(7), logs[0].Change)        // 10 - 3
				assert.Equal(t, int64(10), logs[0].BalanceCount) // legacy total
				assert.Equal(t, float64(100), logs[0].BalanceAmount)
				assert.Equal(t, float64(10), logs[0].Price) // 70 / 7

				t.Run("second run is a no-op once in sync", func(t *testing.T) {
					assert.NoError(t, NewSyncLegacyFunc(db)(t.Context(), nil))
					var n int64
					assert.NoError(t, db.Model(&inventory_models.StockBatchLog{}).Count(&n).Error)
					assert.Equal(t, int64(1), n) // no new adjustment
				})
			})
		},
	)
}
