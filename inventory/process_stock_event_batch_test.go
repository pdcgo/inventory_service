package inventory_test

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory"
	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	warehouse_iface "github.com/pdcgo/schema/services/warehouse_iface/v1"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

// TestProcessStockEventBatch covers StockBatch creation on a stock-entering event: a
// RestockAccepted records one StockBatch per (product, warehouse) from the inbound
// invertory_histories rows, keyed by batch_code = inbound tx id. It is idempotent and
// the StartCount reflects the original inbound quantity (already-shipped rows, whose
// tx_id != in_tx_id, are not netted out).
func TestProcessStockEventBatch(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "process stock event batch",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(
					&db_models.InvTransaction{},
					&db_models.InvTxItem{},
					&db_models.RestockCost{},
					&db_models.InvertoryHistory{},
					&db_models.WarehouseTransfer{},
					&invItemProblem{},
					&skuRow{},
					&inventory_models.StockState{},
					&inventory_models.StockBatch{},
					&inventory_models.StockBatchLog{},
					&inventory_models.StockPlacement{},
					&inventory_models.StockPlacementLog{},
				))

				at := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
				sku, err := db_models.NewSkuID(&db_models.SkuData{WarehouseID: 9, TeamID: 1, ProductID: 5, VariantID: 1})
				assert.NoError(t, err)

				// the restock (inbound) transaction + its item.
				assert.NoError(t, db.Create(&db_models.InvTransaction{
					ID: 100, TeamID: 1, WarehouseID: 9, CreateByID: 7,
					Type: db_models.InvTxRestock, Status: db_models.InvTxCompleted, Created: at,
				}).Error)
				assert.NoError(t, db.Create(&db_models.InvTxItem{
					ID: 2000, InvTransactionID: 100, SkuID: sku, Count: 5, Price: 10, Total: 50,
				}).Error)

				// sku master so the batch query's `join skus` resolves product_id.
				assert.NoError(t, db.Create(&skuRow{ID: sku, ProductID: 5, WarehouseID: 9}).Error)

				// inbound placement rows (tx_id = in_tx_id = 100), split across two racks,
				// landed unit cost 12 (price 10 + ext 2).
				inTx := uint(100)
				orderTx := uint(200)
				assert.NoError(t, db.Create(&[]db_models.InvertoryHistory{
					{TxID: &inTx, InTxID: &inTx, SkuID: sku, WarehouseID: 9, RackID: 41, Count: 3, Price: 10, ExtPrice: 2, Created: at},
					{TxID: &inTx, InTxID: &inTx, SkuID: sku, WarehouseID: 9, RackID: 42, Count: 2, Price: 10, ExtPrice: 2, Created: at},
					// later consumption from this batch: in_tx_id = 100 but tx_id = 200 (an order),
					// so it must NOT be counted toward StartCount.
					{TxID: &orderTx, InTxID: &inTx, SkuID: sku, WarehouseID: 9, RackID: 41, Count: -1, Price: 10, ExtPrice: 2, Created: at},
				}).Error)

				svc := inventory.NewInventoryService(db)
				push := func() error {
					_, err := svc.PushStockEvent(t.Context(), connect.NewRequest(&inventory_iface.PushStockEventRequest{
						Event: &warehouse_iface.StockEvent{
							Data: &warehouse_iface.StockEvent_RestockAccepted{
								RestockAccepted: &warehouse_iface.RestockAccepted{TransactionId: 100},
							},
						},
					}))
					return err
				}
				batchCount := func() int64 {
					var n int64
					assert.NoError(t, db.Model(&inventory_models.StockBatch{}).Where("inbound_id = ?", uint64(100)).Count(&n).Error)
					return n
				}

				t.Run("restock mints one batch from the inbound placement rows", func(t *testing.T) {
					assert.NoError(t, push())

					var b inventory_models.StockBatch
					assert.NoError(t, db.Where("inbound_id = ? AND product_id = ?", uint64(100), uint64(5)).First(&b).Error)
					assert.Equal(t, uint64(9), b.WarehouseID)
					assert.Equal(t, "100", b.BatchCode)
					assert.Equal(t, int64(5), b.StartCount) // 3 + 2; the -1 consumption row is excluded
					assert.Equal(t, int64(5), b.EndCount)
					assert.Equal(t, float64(12), b.Price) // landed 10 + 2, count-weighted
					assert.Equal(t, int64(1), batchCount())
				})

				t.Run("redelivery / RPC retry does not duplicate", func(t *testing.T) {
					assert.NoError(t, push())
					assert.Equal(t, int64(1), batchCount())
				})
			})
		},
	)
}
