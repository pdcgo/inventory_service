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

				t.Run("order decrements, no batch", func(t *testing.T) {
					id := create(&inventory_iface.TransactionCreateRequest{
						WarehouseId: txWarehouse,
						Tx:          &inventory_iface.TransactionCreateRequest_Order{Order: &inventory_iface.TransactionOrder{Items: items(1, 5)}},
					})
					assert.Equal(t, int64(-5), stockOf(1))
					assert.Equal(t, int64(0), batchCount(id))
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

				t.Run("cancel of an order restores stock", func(t *testing.T) {
					id := create(&inventory_iface.TransactionCreateRequest{
						WarehouseId: txWarehouse,
						Tx:          &inventory_iface.TransactionCreateRequest_Order{Order: &inventory_iface.TransactionOrder{Items: items(11, 4)}},
					})
					assert.Equal(t, int64(-4), stockOf(11))

					_, err := svc.TransactionCancel(ctx, connect.NewRequest(&inventory_iface.TransactionCancelRequest{
						TransactionId: id, WarehouseId: txWarehouse,
					}))
					assert.NoError(t, err)
					assert.Equal(t, int64(0), stockOf(11))
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
