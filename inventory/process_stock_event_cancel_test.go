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

// TestProcessStockEventOrderCanceled covers the reconstruction-based order cancel:
// it reverses the transaction's net from the existing stock batch log, restores
// rack placement, and is idempotent when the cancel event is delivered twice.
func TestProcessStockEventOrderCanceled(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "process stock event order canceled",
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
					&invItemProblem{},
					&skuRow{},
					&inventory_models.StockState{},
					&inventory_models.StockBatchLog{},
					&inventory_models.StockPlacement{},
					&inventory_models.StockPlacementLog{},
				))

				at := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
				sku, err := db_models.NewSkuID(&db_models.SkuData{WarehouseID: 9, TeamID: 1, ProductID: 5, VariantID: 1})
				assert.NoError(t, err)
				assert.NoError(t, db.Create(&skuRow{ID: sku, ProductID: 5, WarehouseID: 9}).Error)

				// the order being canceled: 3 units of product 5 @ 10.
				assert.NoError(t, db.Create(&db_models.InvTransaction{
					ID: 100, TeamID: 1, WarehouseID: 9, CreateByID: 7,
					Type: db_models.InvTxOrder, Status: db_models.InvTxCompleted, Created: at,
				}).Error)
				assert.NoError(t, db.Create(&db_models.InvTxItem{
					ID: 2000, InvTransactionID: 100, SkuID: sku, Count: 3, Price: 10, Total: 30,
				}).Error)
				// rack row so the placement processor finds the rack to move.
				txid := uint(100)
				assert.NoError(t, db.Create(&db_models.InvertoryHistory{
					TxID: &txid, SkuID: sku, WarehouseID: 9, RackID: 41, Count: 3, Created: at,
				}).Error)

				svc := inventory.NewInventoryService(db)

				cancel := func() (*connect.Response[inventory_iface.PushStockEventResponse], error) {
					return svc.PushStockEvent(t.Context(), connect.NewRequest(&inventory_iface.PushStockEventRequest{
						Event: &warehouse_iface.StockEvent{
							Data: &warehouse_iface.StockEvent_OrderCanceled{
								OrderCanceled: &warehouse_iface.OrderCanceled{TransactionId: 100},
							},
						},
					}))
				}
				stockReady := func() inventory_models.StockState {
					var st inventory_models.StockState
					assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ?", uint64(5), uint64(9)).First(&st).Error)
					return st
				}
				rack41 := func() inventory_models.StockPlacement {
					var pl inventory_models.StockPlacement
					assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ? AND rack_id = ?", uint64(5), uint64(9), uint64(41)).First(&pl).Error)
					return pl
				}

				// accept the order first: stock and rack both go to -3.
				_, err = svc.PushStockEvent(t.Context(), connect.NewRequest(&inventory_iface.PushStockEventRequest{
					Event: &warehouse_iface.StockEvent{
						Data: &warehouse_iface.StockEvent_OrderAccepted{
							OrderAccepted: &warehouse_iface.OrderAccepted{TransactionId: 100},
						},
					},
				}))
				assert.NoError(t, err)
				assert.Equal(t, int64(-3), stockReady().StockReady)
				assert.Equal(t, int64(-3), rack41().Count)

				t.Run("first cancel reverses stock and restores the rack", func(t *testing.T) {
					_, err := cancel()
					assert.NoError(t, err)

					st := stockReady()
					assert.Equal(t, int64(0), st.StockReady)
					assert.Equal(t, float64(0), st.StockReadyAmount)
					assert.Equal(t, int64(0), rack41().Count)

					// the order-created log plus the reversing cancel log.
					var logs []*inventory_models.StockBatchLog
					assert.NoError(t, db.Where("transaction_id = ?", uint64(100)).Order("id asc").Find(&logs).Error)
					assert.Len(t, logs, 2)
					last := logs[1]
					assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_CANCELED, last.ChangeType)
					assert.Equal(t, int64(3), last.Change)
					assert.Equal(t, int64(0), last.BalanceCount)
				})

				t.Run("duplicate cancel is an idempotent no-op", func(t *testing.T) {
					count := func(model interface{}) int64 {
						var n int64
						assert.NoError(t, db.Model(model).Where("transaction_id = ?", uint64(100)).Count(&n).Error)
						return n
					}
					batchBefore := count(&inventory_models.StockBatchLog{})
					placeBefore := count(&inventory_models.StockPlacementLog{})

					_, err := cancel()
					assert.NoError(t, err)

					// nothing moved, nothing logged.
					assert.Equal(t, int64(0), stockReady().StockReady)
					assert.Equal(t, int64(0), rack41().Count)
					assert.Equal(t, batchBefore, count(&inventory_models.StockBatchLog{}))
					assert.Equal(t, placeBefore, count(&inventory_models.StockPlacementLog{}))
				})

				t.Run("cancel without a prior batch log errors", func(t *testing.T) {
					_, err := svc.PushStockEvent(t.Context(), connect.NewRequest(&inventory_iface.PushStockEventRequest{
						Event: &warehouse_iface.StockEvent{
							Data: &warehouse_iface.StockEvent_OrderCanceled{
								OrderCanceled: &warehouse_iface.OrderCanceled{TransactionId: 999},
							},
						},
					}))
					assert.Error(t, err)
				})
			})
		},
	)
}

// TestProcessStockEventTransferCanceled covers the reconstruction-based transfer
// cancel: it reverses the outbound leg's net from the existing stock batch log,
// restores the source racks, and is idempotent when the cancel is delivered twice.
func TestProcessStockEventTransferCanceled(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "process stock event transfer canceled",
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
					&inventory_models.StockBatchLog{},
					&inventory_models.StockPlacement{},
					&inventory_models.StockPlacementLog{},
				))

				at := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
				outTxID := uint(300)
				skuSrc, err := db_models.NewSkuID(&db_models.SkuData{WarehouseID: 9, TeamID: 1, ProductID: 20, VariantID: 1})
				assert.NoError(t, err)
				assert.NoError(t, db.Create(&skuRow{ID: skuSrc, ProductID: 20, WarehouseID: 9}).Error)

				// outbound leg of the transfer: 5 units of product 20 @ 4 leaving wh 9.
				assert.NoError(t, db.Create(&db_models.InvTransaction{
					ID: outTxID, TeamID: 1, WarehouseID: 9, CreateByID: 7,
					Type: db_models.InvTxTransferOut, Status: db_models.InvTxCompleted, Created: at,
				}).Error)
				assert.NoError(t, db.Create(&db_models.InvTxItem{
					ID: 4000, InvTransactionID: outTxID, SkuID: skuSrc, Count: 5, Price: 4, Total: 20,
				}).Error)
				assert.NoError(t, db.Create(&db_models.InvertoryHistory{
					TxID: &outTxID, SkuID: skuSrc, WarehouseID: 9, RackID: 41, Count: 5, Created: at,
				}).Error)
				assert.NoError(t, db.Create(&db_models.WarehouseTransfer{
					ID: 50, OutboundTxID: outTxID, InboundTxID: 301, FromWarehouseID: 9, ToWarehouseID: 10, TeamID: 1,
				}).Error)

				svc := inventory.NewInventoryService(db)

				cancel := func(transferID uint64) (*connect.Response[inventory_iface.PushStockEventResponse], error) {
					return svc.PushStockEvent(t.Context(), connect.NewRequest(&inventory_iface.PushStockEventRequest{
						Event: &warehouse_iface.StockEvent{
							Data: &warehouse_iface.StockEvent_TransferWarehouseCanceled{
								TransferWarehouseCanceled: &warehouse_iface.TransferWarehouseCanceled{TransferId: transferID},
							},
						},
					}))
				}
				srcState := func() inventory_models.StockState {
					var st inventory_models.StockState
					assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ?", uint64(20), uint64(9)).First(&st).Error)
					return st
				}
				rack41 := func() inventory_models.StockPlacement {
					var pl inventory_models.StockPlacement
					assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ? AND rack_id = ?", uint64(20), uint64(9), uint64(41)).First(&pl).Error)
					return pl
				}

				// create the transfer (OUT leg): source stock and rack both go to -5.
				_, err = svc.PushStockEvent(t.Context(), connect.NewRequest(&inventory_iface.PushStockEventRequest{
					Event: &warehouse_iface.StockEvent{
						Data: &warehouse_iface.StockEvent_TransferWarehouseCreated{
							TransferWarehouseCreated: &warehouse_iface.TransferWarehouseCreated{TransferId: 50},
						},
					},
				}))
				assert.NoError(t, err)
				assert.Equal(t, int64(-5), srcState().StockReady)
				assert.Equal(t, int64(-5), rack41().Count)

				t.Run("first cancel restores the source stock and rack", func(t *testing.T) {
					_, err := cancel(50)
					assert.NoError(t, err)

					st := srcState()
					assert.Equal(t, int64(0), st.StockReady)
					assert.Equal(t, float64(0), st.StockReadyAmount)
					assert.Equal(t, int64(0), rack41().Count)

					// the outbound transfer log plus the reversing cancel log (both TRANSFER).
					var logs []*inventory_models.StockBatchLog
					assert.NoError(t, db.Where("transaction_id = ?", uint64(outTxID)).Order("id asc").Find(&logs).Error)
					assert.Len(t, logs, 2)
					last := logs[1]
					assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER, last.ChangeType)
					assert.Equal(t, int64(5), last.Change)
					assert.Equal(t, int64(0), last.BalanceCount)
				})

				t.Run("duplicate cancel is an idempotent no-op", func(t *testing.T) {
					count := func(model interface{}) int64 {
						var n int64
						assert.NoError(t, db.Model(model).Where("transaction_id = ?", uint64(outTxID)).Count(&n).Error)
						return n
					}
					batchBefore := count(&inventory_models.StockBatchLog{})
					placeBefore := count(&inventory_models.StockPlacementLog{})

					_, err := cancel(50)
					assert.NoError(t, err)

					assert.Equal(t, int64(0), srcState().StockReady)
					assert.Equal(t, int64(0), rack41().Count)
					assert.Equal(t, batchBefore, count(&inventory_models.StockBatchLog{}))
					assert.Equal(t, placeBefore, count(&inventory_models.StockPlacementLog{}))
				})

				t.Run("cancel of a transfer never applied errors", func(t *testing.T) {
					// transfer 60's outbound tx 600 was never applied -> no batch log.
					assert.NoError(t, db.Create(&db_models.WarehouseTransfer{
						ID: 60, OutboundTxID: 600, InboundTxID: 601, FromWarehouseID: 9, ToWarehouseID: 10, TeamID: 1,
					}).Error)
					_, err := cancel(60)
					assert.Error(t, err)
				})
			})
		},
	)
}
