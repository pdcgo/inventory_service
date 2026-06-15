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
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

func TestProcessStockBatchLog(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "process stock batch log",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(
					&inventory_models.StockState{},
					&inventory_models.StockBatchLog{},
				))

				at := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

				stateOf := func(productID uint64) inventory_models.StockState {
					var s inventory_models.StockState
					assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ?", productID, uint64(9)).First(&s).Error)
					return s
				}
				logsOf := func(productID uint64) []*inventory_models.StockBatchLog {
					logs := []*inventory_models.StockBatchLog{}
					assert.NoError(t, db.Where("product_id = ?", productID).Order("id asc").Find(&logs).Error)
					return logs
				}

				// product 1 already has 10 ready; product 2 has no state yet.
				assert.NoError(t, db.Create(&inventory_models.StockState{
					ProductID: 1, WarehouseID: 9, StockReady: 10, CreatedAt: at, UpdatedAt: at,
				}).Error)

				t.Run("order created subtracts ready stock/value and logs the balance", func(t *testing.T) {
					_, err := inventory_mutations.NewProcessStockBatchLog(db)(&inventory_iface.StockChange{
						At:          timestamppb.New(at),
						WarehouseId: 9,
						UserId:      7,
						Change: &inventory_iface.StockChange_OrderCreated{
							OrderCreated: &inventory_iface.OrderCreated{OrderId: 100},
						},
						Changes: []*inventory_iface.ChangeItem{
							{ProductId: 1, ChangeCount: 3, ChangeAmount: 30},
							{ProductId: 2, ChangeCount: 5, ChangeAmount: 50},
						},
					})
					assert.NoError(t, err)

					assert.Equal(t, int64(7), stateOf(1).StockReady)           // 10 - 3
					assert.Equal(t, float64(-30), stateOf(1).StockReadyAmount) // 0 - 30
					assert.Equal(t, int64(-5), stateOf(2).StockReady)          // 0 - 5 (state created)
					assert.Equal(t, float64(-50), stateOf(2).StockReadyAmount) // 0 - 50

					l1 := logsOf(1)
					assert.Len(t, l1, 1)
					assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_CREATED, l1[0].ChangeType)
					assert.Equal(t, uint64(7), l1[0].UserID)
					assert.Equal(t, int64(-3), l1[0].Change)
					assert.Equal(t, int64(7), l1[0].BalanceCount)
					assert.Equal(t, float64(10), l1[0].Price)
					assert.Equal(t, float64(-30), l1[0].BalanceAmount)

					l2 := logsOf(2)
					assert.Len(t, l2, 1)
					assert.Equal(t, int64(-5), l2[0].Change)
					assert.Equal(t, int64(-5), l2[0].BalanceCount)
					assert.Equal(t, float64(-50), l2[0].BalanceAmount)
				})

				t.Run("order canceled adds the stock/value back", func(t *testing.T) {
					_, err := inventory_mutations.NewProcessStockBatchLog(db)(&inventory_iface.StockChange{
						At:          timestamppb.New(at),
						WarehouseId: 9,
						UserId:      7,
						Change: &inventory_iface.StockChange_OrderCanceled{
							OrderCanceled: &inventory_iface.OrderCanceled{OrderId: 100},
						},
						Changes: []*inventory_iface.ChangeItem{
							{ProductId: 1, ChangeCount: 3, ChangeAmount: 30},
						},
					})
					assert.NoError(t, err)

					assert.Equal(t, int64(10), stateOf(1).StockReady)        // 7 + 3 back to 10
					assert.Equal(t, float64(0), stateOf(1).StockReadyAmount) // -30 + 30 back to 0
					l1 := logsOf(1)
					assert.Len(t, l1, 2)
					assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_CANCELED, l1[1].ChangeType)
					assert.Equal(t, int64(3), l1[1].Change)
					assert.Equal(t, int64(10), l1[1].BalanceCount)
					assert.Equal(t, float64(0), l1[1].BalanceAmount)
				})

				t.Run("restock adds new stock/value", func(t *testing.T) {
					_, err := inventory_mutations.NewProcessStockBatchLog(db)(&inventory_iface.StockChange{
						At:            timestamppb.New(at),
						WarehouseId:   9,
						UserId:        7,
						TransactionId: 50,
						Change: &inventory_iface.StockChange_Restock{
							Restock: &inventory_iface.Restock{},
						},
						Changes: []*inventory_iface.ChangeItem{{ProductId: 3, ChangeCount: 8, ChangeAmount: 80}},
					})
					assert.NoError(t, err)
					assert.Equal(t, int64(8), stateOf(3).StockReady)
					assert.Equal(t, float64(80), stateOf(3).StockReadyAmount)
					l := logsOf(3)
					assert.Len(t, l, 1)
					assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK, l[0].ChangeType)
					assert.Equal(t, int64(8), l[0].Change)
					assert.Equal(t, float64(10), l[0].Price)
					assert.Equal(t, float64(80), l[0].BalanceAmount)
					assert.Equal(t, uint64(50), l[0].TransactionID)
				})

				t.Run("problem subtracts stock", func(t *testing.T) {
					_, err := inventory_mutations.NewProcessStockBatchLog(db)(&inventory_iface.StockChange{
						At:            timestamppb.New(at),
						WarehouseId:   9,
						UserId:        7,
						TransactionId: 60,
						Change: &inventory_iface.StockChange_Problem{
							Problem: &inventory_iface.Problem{},
						},
						Changes: []*inventory_iface.ChangeItem{{ProductId: 1, ChangeCount: 2, ChangeAmount: 20}},
					})
					assert.NoError(t, err)
					assert.Equal(t, int64(8), stateOf(1).StockReady)           // 10 - 2
					assert.Equal(t, float64(-20), stateOf(1).StockReadyAmount) // 0 - 20
				})

				t.Run("adjustment uses the signed count/amount", func(t *testing.T) {
					_, err := inventory_mutations.NewProcessStockBatchLog(db)(&inventory_iface.StockChange{
						At:            timestamppb.New(at),
						WarehouseId:   9,
						UserId:        7,
						TransactionId: 70,
						Change: &inventory_iface.StockChange_Adjustment{
							Adjustment: &inventory_iface.Adjustment{},
						},
						Changes: []*inventory_iface.ChangeItem{{ProductId: 3, ChangeCount: -5, ChangeAmount: -50}}, // negative = decrease
					})
					assert.NoError(t, err)
					assert.Equal(t, int64(3), stateOf(3).StockReady)          // 8 - 5
					assert.Equal(t, float64(30), stateOf(3).StockReadyAmount) // 80 - 50
				})

				t.Run("missing change reason errors", func(t *testing.T) {
					_, err := inventory_mutations.NewProcessStockBatchLog(db)(&inventory_iface.StockChange{
						WarehouseId: 9,
						Changes:     []*inventory_iface.ChangeItem{{ProductId: 1, ChangeCount: 1, ChangeAmount: 10}},
					})
					assert.Error(t, err)
				})
			})
		},
	)
}
