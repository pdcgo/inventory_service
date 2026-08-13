package inventory_test

import (
	"log"
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

func TestProcessStockEventInboundChange(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "process stock event inbound change",
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

				created := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
				arrived := time.Date(2026, 6, 5, 15, 30, 0, 0, time.UTC)

				seed := func(txID uint64, productID uint, txType db_models.InvTxType, arrivedAt *time.Time) {
					sku, err := db_models.NewSkuID(&db_models.SkuData{
						WarehouseID: 9, TeamID: 1, ProductID: productID, VariantID: 1,
					})
					assert.NoError(t, err)

					assert.NoError(t, db.Create(&db_models.InvTransaction{
						ID: uint(txID), TeamID: 1, WarehouseID: 9, CreateByID: 7,
						Type: txType, Status: db_models.InvTxCompleted,
						Created: created, Arrived: arrivedAt,
					}).Error)
					assert.NoError(t, db.Create(&db_models.InvTxItem{
						ID: uint(txID) * 10, InvTransactionID: uint(txID), SkuID: sku,
						Count: 4, Price: 10, Total: 40,
					}).Error)
					assert.NoError(t, db.Create(&skuRow{ID: sku, ProductID: productID, WarehouseID: 9}).Error)
				}

				svc := inventory.NewInventoryService(db)

				push := func(event *warehouse_iface.StockEvent) error {
					_, err := svc.PushStockEvent(t.Context(), connect.NewRequest(&inventory_iface.PushStockEventRequest{
						Event: event,
					}))
					return err
				}

				logOf := func(txID uint64) inventory_models.StockBatchLog {
					var log inventory_models.StockBatchLog
					err := db.
						Where("transaction_id = ?", txID).
						First(&log).
						Error
					assert.NoError(t, err)
					return log
				}

				logAt := func(txID uint64) string {
					return logOf(txID).CreatedAt.UTC().Format(time.RFC3339)
				}

				t.Run("saat terima restock cek log sesuai arrived", func(t *testing.T) {
					seed(100, 5, db_models.InvTxRestock, &arrived)

					err := push(&warehouse_iface.StockEvent{
						Data: &warehouse_iface.StockEvent_RestockAccepted{
							RestockAccepted: &warehouse_iface.RestockAccepted{TransactionId: 100},
						},
					})
					assert.NoError(t, err)
					assert.Equal(t, arrived.Format(time.RFC3339), logAt(100))
				})

				t.Run("saat terima return cek log sesuai arrived", func(t *testing.T) {
					seed(101, 6, db_models.InvTxReturn, &arrived)

					err := push(&warehouse_iface.StockEvent{
						Data: &warehouse_iface.StockEvent_ReturnAccepted{
							ReturnAccepted: &warehouse_iface.ReturnAccepted{TransactionId: 101},
						},
					})
					assert.NoError(t, err)
					assert.Equal(t, arrived.Format(time.RFC3339), logAt(101))
				})

				// ambil now dari inventory_mutations.NewProcessStockBatchLog, ketika tanggal kosong ambil now
				t.Run("cek log restock atau return tanpa arrived ambil now", func(t *testing.T) {
					seed(102, 7, db_models.InvTxRestock, nil)

					before := time.Now().UTC().Add(-time.Second)
					err := push(&warehouse_iface.StockEvent{
						Data: &warehouse_iface.StockEvent_RestockAccepted{
							RestockAccepted: &warehouse_iface.RestockAccepted{TransactionId: 102},
						},
					})
					assert.NoError(t, err)

					stamped := logOf(102).CreatedAt.UTC()
					log.Println(created.Format(time.RFC3339), stamped.Format(time.RFC3339))
					assert.NotEqual(t, created.Format(time.RFC3339), stamped.Format(time.RFC3339))
					assert.True(t, stamped.After(before), "expected the insert time, got %s", stamped)
				})

				// status yang tidak termasuk pada isInboundChange masih ambil dari created, misal: order accepted
				t.Run("order accepted masih ambil dari created", func(t *testing.T) {
					seed(103, 8, db_models.InvTxOrder, &arrived)

					err := push(&warehouse_iface.StockEvent{
						Data: &warehouse_iface.StockEvent_OrderAccepted{
							OrderAccepted: &warehouse_iface.OrderAccepted{TransactionId: 103},
						},
					})
					assert.NoError(t, err)
					assert.Equal(t, created.Format(time.RFC3339), logAt(103))
				})
			})
		},
	)
}
