package inventory_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory"
	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

const txWarehouse uint64 = 9

func TestTransaction(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "inventory transaction",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(
					&inventory_models.InventoryTransaction{},
					&inventory_models.InventoryTransactionItem{},
					&inventory_models.StockState{},
					&inventory_models.StockBatch{},
					&inventory_models.StockBatchLog{},
					// The ORDER kind auto-picks racks per ProductConfig.
					&inventory_models.StockPlacement{},
					&inventory_models.StockPlacementLog{},
					&inventory_models.ProductConfig{},
					// TransactionCancel reuses reconstructCancel, whose placement step
					// reads invertory_histories (left join skus). Owned transactions have
					// no such rows, so it's a no-op — but the tables must exist.
					&db_models.InvertoryHistory{},
					&skuRow{},
				))

				svc := inventory.NewInventoryService(db)
				ctx := context.Background()

				stockOf := func(productID uint64) int64 {
					var st inventory_models.StockState
					res := db.
						Where("product_id = ? AND warehouse_id = ?", productID, txWarehouse).
						Limit(1).
						Find(&st)
					assert.NoError(t, res.Error)
					return st.StockReady
				}
				batchCount := func(txID uint64) int64 {
					var n int64
					assert.NoError(t, db.Model(&inventory_models.StockBatch{}).Where("inbound_id = ?", txID).Count(&n).Error)
					return n
				}
				create := func(req *inventory_iface.TransactionCreateRequest) uint64 {
					res, err := svc.TransactionCreate(ctx, connect.NewRequest(req))
					assert.NoError(t, err)
					return res.Msg.GetTransactionId()
				}
				items := func(pid uint64, count int64) []*inventory_iface.TransactionItem {
					return []*inventory_iface.TransactionItem{{ProductId: pid, Count: count, Price: 5}}
				}
				placementCount := func(productID, rackID uint64) int64 {
					var pl inventory_models.StockPlacement
					res := db.
						Where("product_id = ? AND warehouse_id = ? AND rack_id = ?", productID, txWarehouse, rackID).
						Limit(1).
						Find(&pl)
					assert.NoError(t, res.Error)
					return pl.Count
				}
				seedRestock := func(pid uint64, count int64) {
					create(&inventory_iface.TransactionCreateRequest{
						WarehouseId: txWarehouse,
						Tx:          &inventory_iface.TransactionCreateRequest_Restock{Restock: &inventory_iface.TransactionRestock{Items: items(pid, count)}},
					})
				}

				t.Run("order decrements at avg cost, no batch, racks auto-picked", func(t *testing.T) {
					// Seed: 10 units @ 5 (avg cost 5), spread on two racks 3/7.
					seedRestock(1, 10)
					assert.NoError(t, db.Create(&[]inventory_models.StockPlacement{
						{ProductID: 1, WarehouseID: txWarehouse, RackID: 1, Count: 3},
						{ProductID: 1, WarehouseID: txWarehouse, RackID: 2, Count: 7},
					}).Error)

					res, err := svc.TransactionCreate(ctx, connect.NewRequest(&inventory_iface.TransactionCreateRequest{
						WarehouseId: txWarehouse,
						// Caller price 999 must be IGNORED for orders (avg cost wins).
						Tx: &inventory_iface.TransactionCreateRequest_Order{Order: &inventory_iface.TransactionOrder{
							Items: []*inventory_iface.TransactionItem{{ProductId: 1, Count: 5, Price: 999}},
						}},
					}))
					assert.NoError(t, err)
					id := res.Msg.GetTransactionId()

					assert.Equal(t, int64(5), stockOf(1))
					assert.Equal(t, int64(0), batchCount(id))

					// Response carries the cost valuation (avg cost 5, not 999).
					assert.Len(t, res.Msg.Items, 1)
					assert.InDelta(t, 5, res.Msg.Items[0].UnitCost, 0.001)
					assert.InDelta(t, 25, res.Msg.Items[0].TotalCost, 0.001)

					// StockState amount decremented at cost: 50 - 25 = 25.
					var st inventory_models.StockState
					assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ?", uint64(1), txWarehouse).First(&st).Error)
					assert.InDelta(t, 25, st.StockReadyAmount, 0.001)

					// SMALLER picking (default): rack 1 (3) drained first, spill 2 to rack 2.
					assert.Equal(t, int64(0), placementCount(1, 1))
					assert.Equal(t, int64(5), placementCount(1, 2))
					var logs []inventory_models.StockPlacementLog
					assert.NoError(t, db.Where("transaction_id = ?", id).Order("rack_id ASC").Find(&logs).Error)
					assert.Len(t, logs, 2)
					assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_CREATED, logs[0].ChangeType)
					assert.Equal(t, int64(-3), logs[0].Change)
					assert.Equal(t, int64(-2), logs[1].Change)
				})

				t.Run("order with insufficient stock hard-rejects", func(t *testing.T) {
					_, err := svc.TransactionCreate(ctx, connect.NewRequest(&inventory_iface.TransactionCreateRequest{
						WarehouseId: txWarehouse,
						Tx: &inventory_iface.TransactionCreateRequest_Order{Order: &inventory_iface.TransactionOrder{
							Items: items(1, 100),
						}},
					}))
					assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
					assert.Equal(t, int64(5), stockOf(1)) // untouched

					// Unknown product (no stock state at all) also rejects.
					_, err = svc.TransactionCreate(ctx, connect.NewRequest(&inventory_iface.TransactionCreateRequest{
						WarehouseId: txWarehouse,
						Tx: &inventory_iface.TransactionCreateRequest_Order{Order: &inventory_iface.TransactionOrder{
							Items: items(999, 1),
						}},
					}))
					assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
				})

				t.Run("order BIGGER picking + placement-shortfall drift", func(t *testing.T) {
					// BIGGER config: rack with the larger count drains first.
					seedRestock(21, 10)
					assert.NoError(t, db.Create(&inventory_models.ProductConfig{
						ProductID: 21, WarehouseID: txWarehouse,
						QueueType:        inventory_iface.QueueType_QUEUE_TYPE_FIFO,
						PlacementPicking: inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_BIGGER,
					}).Error)
					assert.NoError(t, db.Create(&[]inventory_models.StockPlacement{
						{ProductID: 21, WarehouseID: txWarehouse, RackID: 1, Count: 3},
						{ProductID: 21, WarehouseID: txWarehouse, RackID: 2, Count: 7},
					}).Error)
					_, err := svc.TransactionCreate(ctx, connect.NewRequest(&inventory_iface.TransactionCreateRequest{
						WarehouseId: txWarehouse,
						Tx: &inventory_iface.TransactionCreateRequest_Order{Order: &inventory_iface.TransactionOrder{
							Items: items(21, 5),
						}},
					}))
					assert.NoError(t, err)
					assert.Equal(t, int64(3), placementCount(21, 1)) // untouched
					assert.Equal(t, int64(2), placementCount(21, 2)) // bigger rack drained

					// Drift: state suffices (10) but racks only hold 2 → consume 2, no error.
					seedRestock(22, 10)
					assert.NoError(t, db.Create(&inventory_models.StockPlacement{
						ProductID: 22, WarehouseID: txWarehouse, RackID: 1, Count: 2,
					}).Error)
					_, err = svc.TransactionCreate(ctx, connect.NewRequest(&inventory_iface.TransactionCreateRequest{
						WarehouseId: txWarehouse,
						Tx: &inventory_iface.TransactionCreateRequest_Order{Order: &inventory_iface.TransactionOrder{
							Items: items(22, 5),
						}},
					}))
					assert.NoError(t, err)
					assert.Equal(t, int64(5), stockOf(22))           // state fully decremented
					assert.Equal(t, int64(0), placementCount(22, 1)) // racks drained to zero, drift 3
				})

				t.Run("restock increments + mints a batch", func(t *testing.T) {
					id := create(&inventory_iface.TransactionCreateRequest{
						WarehouseId: txWarehouse,
						Tx:          &inventory_iface.TransactionCreateRequest_Restock{Restock: &inventory_iface.TransactionRestock{Items: items(2, 5)}},
					})
					assert.Equal(t, int64(5), stockOf(2))
					assert.Equal(t, int64(1), batchCount(id))

					var b inventory_models.StockBatch
					assert.NoError(t, db.Where("inbound_id = ?", id).First(&b).Error)
					assert.Equal(t, int64(5), b.StartCount)
					assert.Equal(t, int64(5), b.EndCount)
					assert.Equal(t, float64(5), b.Price)
				})

				t.Run("return increments + mints a batch", func(t *testing.T) {
					id := create(&inventory_iface.TransactionCreateRequest{
						WarehouseId: txWarehouse,
						Tx:          &inventory_iface.TransactionCreateRequest_StockReturn{StockReturn: &inventory_iface.TransactionReturn{Items: items(3, 5)}},
					})
					assert.Equal(t, int64(5), stockOf(3))
					assert.Equal(t, int64(1), batchCount(id))
				})

				t.Run("found_back increments + mints a batch", func(t *testing.T) {
					id := create(&inventory_iface.TransactionCreateRequest{
						WarehouseId: txWarehouse,
						Tx:          &inventory_iface.TransactionCreateRequest_FoundBack{FoundBack: &inventory_iface.TransactionFoundBack{Items: items(4, 5)}},
					})
					assert.Equal(t, int64(5), stockOf(4))
					assert.Equal(t, int64(1), batchCount(id))
				})

				t.Run("problem decrements, no batch", func(t *testing.T) {
					id := create(&inventory_iface.TransactionCreateRequest{
						WarehouseId: txWarehouse,
						Tx:          &inventory_iface.TransactionCreateRequest_Problem{Problem: &inventory_iface.TransactionProblem{Items: items(5, 5)}},
					})
					assert.Equal(t, int64(-5), stockOf(5))
					assert.Equal(t, int64(0), batchCount(id))
				})

				t.Run("cancel reverses state, voids batch, is idempotent", func(t *testing.T) {
					// inbound: restock product 10 by 8, then cancel.
					id := create(&inventory_iface.TransactionCreateRequest{
						WarehouseId: txWarehouse,
						Tx:          &inventory_iface.TransactionCreateRequest_Restock{Restock: &inventory_iface.TransactionRestock{Items: items(10, 8)}},
					})
					assert.Equal(t, int64(8), stockOf(10))
					assert.Equal(t, int64(1), batchCount(id))

					_, err := svc.TransactionCancel(ctx, connect.NewRequest(&inventory_iface.TransactionCancelRequest{
						TransactionId: id, WarehouseId: txWarehouse,
					}))
					assert.NoError(t, err)
					assert.Equal(t, int64(0), stockOf(10))    // reversed
					assert.Equal(t, int64(0), batchCount(id)) // batch voided

					var trx inventory_models.InventoryTransaction
					assert.NoError(t, db.First(&trx, id).Error)
					assert.Equal(t, inventory_models.InvTxCanceled, trx.Status)
					assert.NotNil(t, trx.CanceledAt)

					// second cancel is a no-op.
					_, err = svc.TransactionCancel(ctx, connect.NewRequest(&inventory_iface.TransactionCancelRequest{
						TransactionId: id,
					}))
					assert.NoError(t, err)
					assert.Equal(t, int64(0), stockOf(10))
				})

				t.Run("cancel of an order restores stock AND placements", func(t *testing.T) {
					seedRestock(11, 6)
					assert.NoError(t, db.Create(&inventory_models.StockPlacement{
						ProductID: 11, WarehouseID: txWarehouse, RackID: 3, Count: 6,
					}).Error)

					id := create(&inventory_iface.TransactionCreateRequest{
						WarehouseId: txWarehouse,
						Tx:          &inventory_iface.TransactionCreateRequest_Order{Order: &inventory_iface.TransactionOrder{Items: items(11, 4)}},
					})
					assert.Equal(t, int64(2), stockOf(11))
					assert.Equal(t, int64(2), placementCount(11, 3))

					_, err := svc.TransactionCancel(ctx, connect.NewRequest(&inventory_iface.TransactionCancelRequest{
						TransactionId: id, WarehouseId: txWarehouse,
					}))
					assert.NoError(t, err)
					assert.Equal(t, int64(6), stockOf(11))           // state restored
					assert.Equal(t, int64(6), placementCount(11, 3)) // placements restored

					// The reversal wrote ORDER_CANCELED placement logs.
					var n int64
					assert.NoError(t, db.Model(&inventory_models.StockPlacementLog{}).
						Where("transaction_id = ? AND change_type = ?", id,
							inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_CANCELED).
						Count(&n).Error)
					assert.Equal(t, int64(1), n)

					// Second cancel stays idempotent (placements not double-reversed).
					_, err = svc.TransactionCancel(ctx, connect.NewRequest(&inventory_iface.TransactionCancelRequest{
						TransactionId: id, WarehouseId: txWarehouse,
					}))
					assert.NoError(t, err)
					assert.Equal(t, int64(6), placementCount(11, 3))
				})

				t.Run("cancel unknown transaction is NotFound", func(t *testing.T) {
					_, err := svc.TransactionCancel(ctx, connect.NewRequest(&inventory_iface.TransactionCancelRequest{
						TransactionId: 999999,
					}))
					assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
				})
			})
		},
	)
}
