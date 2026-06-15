package inventory_service_test

import (
	"testing"
	"time"

	"github.com/pdcgo/event_source/event_source_mock"
	"github.com/pdcgo/inventory_service"
	"github.com/pdcgo/inventory_service/inventory_models"
	"github.com/pdcgo/san_collection/san_config"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	warehouse_iface "github.com/pdcgo/schema/services/warehouse_iface/v1"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

// invItemProblem is a minimal stand-in for warehouse_models.InvItemProblem so the
// test DB has the inv_item_problems table the transaction expansion LEFT JOINs
// against, without pulling the warehouse_service module into inventory_service.
type invItemProblem struct {
	ID          uint `gorm:"primarykey"`
	SkuID       db_models.SkuID
	TxID        uint
	TxItemID    uint
	ProblemType string
	Count       int
}

func (invItemProblem) TableName() string { return "inv_item_problems" }

func TestInventoryPushHandler(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "inventory push handler",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(
					&db_models.InvTransaction{},
					&db_models.InvTxItem{},
					&db_models.RestockCost{},
					&invItemProblem{},
					&inventory_models.StockState{},
					&inventory_models.StockBatchLog{},
				))

				projectCfg := &san_config.ProjectConfig{ProjectID: "test"}
				handler := inventory_service.NewInventoryPushHandler(db, projectCfg)
				stockSub := projectCfg.PubsubSubscriberPath("inventory-stock-sub")
				at := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

				// product 5 in warehouse 9, encoded into a SkuID the converter extracts.
				sku, err := db_models.NewSkuID(&db_models.SkuData{WarehouseID: 9, TeamID: 1, ProductID: 5, VariantID: 1})
				assert.NoError(t, err)

				// Seed a warehouse transaction (id 100) with one item: 3 units of product 5.
				assert.NoError(t, db.Create(&db_models.InvTransaction{
					ID:          100,
					TeamID:      1,
					WarehouseID: 9,
					CreateByID:  7,
					Type:        db_models.InvTxRestock,
					Status:      db_models.InvTxCompleted,
					Created:     at,
				}).Error)
				assert.NoError(t, db.Create(&db_models.InvTxItem{
					ID:               2000,
					InvTransactionID: 100,
					SkuID:            sku,
					Count:            3,
					Price:            10,
					Total:            30,
				}).Error)

				stateOf := func(productID uint64) (inventory_models.StockState, bool) {
					var s inventory_models.StockState
					res := db.Where("product_id = ? AND warehouse_id = ?", productID, uint64(9)).Limit(1).Find(&s)
					assert.NoError(t, res.Error)
					return s, res.RowsAffected > 0
				}
				logCount := func() int64 {
					var n int64
					assert.NoError(t, db.Model(&inventory_models.StockBatchLog{}).Count(&n).Error)
					return n
				}
				push := func(sub string, event *warehouse_iface.StockEvent) error {
					msg := event_source_mock.NewMockEvent(t, event)
					msg.Subscription = sub
					return handler(t.Context(), msg)
				}

				t.Run("restock accepted expands transaction and adds stock", func(t *testing.T) {
					err := push(stockSub, &warehouse_iface.StockEvent{
						Data: &warehouse_iface.StockEvent_RestockAccepted{
							RestockAccepted: &warehouse_iface.RestockAccepted{TransactionId: 100},
						},
					})
					assert.NoError(t, err)

					s, ok := stateOf(5)
					assert.True(t, ok)
					assert.Equal(t, int64(3), s.StockReady)
					assert.Equal(t, float64(30), s.StockReadyAmount) // 3 × price 10
					assert.Equal(t, int64(1), logCount())

					var log inventory_models.StockBatchLog
					assert.NoError(t, db.First(&log, "product_id = ?", uint64(5)).Error)
					assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK, log.ChangeType)
					assert.Equal(t, int64(3), log.Change)
					assert.Equal(t, int64(3), log.BalanceCount)
					assert.Equal(t, float64(30), log.BalanceAmount)
					assert.Equal(t, float64(10), log.Price)
					assert.Equal(t, uint64(100), log.TransactionID)
					assert.Equal(t, uint64(7), log.UserID)
				})

				t.Run("order accepted expands transaction and subtracts stock", func(t *testing.T) {
					err := push(stockSub, &warehouse_iface.StockEvent{
						Data: &warehouse_iface.StockEvent_OrderAccepted{
							OrderAccepted: &warehouse_iface.OrderAccepted{TransactionId: 100},
						},
					})
					assert.NoError(t, err)

					s, _ := stateOf(5)
					assert.Equal(t, int64(0), s.StockReady)         // 3 − 3
					assert.Equal(t, float64(0), s.StockReadyAmount) // 30 − 30
					assert.Equal(t, int64(2), logCount())

					var log inventory_models.StockBatchLog
					assert.NoError(t, db.Order("id desc").First(&log, "product_id = ?", uint64(5)).Error)
					assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_CREATED, log.ChangeType)
					assert.Equal(t, int64(-3), log.Change)
					assert.Equal(t, float64(0), log.BalanceAmount)
				})

				t.Run("unknown subscription is a no-op ack", func(t *testing.T) {
					err := push("projects/test/subscriptions/some-other-sub", &warehouse_iface.StockEvent{
						Data: &warehouse_iface.StockEvent_RestockAccepted{
							RestockAccepted: &warehouse_iface.RestockAccepted{TransactionId: 100},
						},
					})
					assert.NoError(t, err)

					s, _ := stateOf(5)
					assert.Equal(t, int64(0), s.StockReady) // unchanged
					assert.Equal(t, int64(2), logCount())
				})

				t.Run("missing transaction is a no-op", func(t *testing.T) {
					err := push(stockSub, &warehouse_iface.StockEvent{
						Data: &warehouse_iface.StockEvent_RestockAccepted{
							RestockAccepted: &warehouse_iface.RestockAccepted{TransactionId: 999},
						},
					})
					assert.NoError(t, err)
					assert.Equal(t, int64(2), logCount()) // no new logs
				})
			})
		},
	)
}
