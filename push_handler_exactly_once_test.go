package inventory_service_test

import (
	"testing"
	"time"

	"github.com/pdcgo/event_source/event_source_mock"
	"github.com/pdcgo/inventory_service"
	"github.com/pdcgo/inventory_service/inventory_models"
	"github.com/pdcgo/san_collection/san_config"
	warehouse_iface "github.com/pdcgo/schema/services/warehouse_iface/v1"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

// TestInventoryPushHandlerExactlyOnce covers the message-id inbox: a redelivered
// message (same MessageID) is skipped, while a fresh MessageID applies again.
func TestInventoryPushHandlerExactlyOnce(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "inventory push handler exactly once",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(
					&db_models.InvTransaction{},
					&db_models.InvTxItem{},
					&db_models.RestockCost{},
					&db_models.InvertoryHistory{},
					&invItemProblem{},
					&placementSku{},
					&inventory_models.StockState{},
					&inventory_models.StockBatch{},
					&inventory_models.StockBatchLog{},
					&inventory_models.InventoryExactlyOnceLog{},
				))

				projectCfg := &san_config.ProjectConfig{ProjectID: "test"}
				handler := inventory_service.NewInventoryPushHandler(db, projectCfg)
				stockSub := projectCfg.PubsubSubscriberPath("inventory-stock-sub")
				at := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

				sku, err := db_models.NewSkuID(&db_models.SkuData{WarehouseID: 9, TeamID: 1, ProductID: 5, VariantID: 1})
				assert.NoError(t, err)
				assert.NoError(t, db.Create(&db_models.InvTransaction{
					ID: 100, TeamID: 1, WarehouseID: 9, CreateByID: 7,
					Type: db_models.InvTxRestock, Status: db_models.InvTxCompleted, Created: at,
				}).Error)
				assert.NoError(t, db.Create(&db_models.InvTxItem{
					ID: 2000, InvTransactionID: 100, SkuID: sku, Count: 3, Price: 10, Total: 30,
				}).Error)

				restock := &warehouse_iface.StockEvent{
					Data: &warehouse_iface.StockEvent_RestockAccepted{
						RestockAccepted: &warehouse_iface.RestockAccepted{TransactionId: 100},
					},
				}
				pushID := func(id string) error {
					msg := event_source_mock.NewMockEvent(t, restock)
					msg.Subscription = stockSub
					msg.Message.MessageID = id
					return handler(t.Context(), msg)
				}
				stockReady := func() int64 {
					var s inventory_models.StockState
					assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ?", uint64(5), uint64(9)).Limit(1).Find(&s).Error)
					return s.StockReady
				}
				dedupCount := func() int64 {
					var n int64
					assert.NoError(t, db.Model(&inventory_models.InventoryExactlyOnceLog{}).Count(&n).Error)
					return n
				}

				t.Run("first delivery applies the event", func(t *testing.T) {
					assert.NoError(t, pushID("msg-1"))
					assert.Equal(t, int64(3), stockReady())
					assert.Equal(t, int64(1), dedupCount())
				})

				t.Run("redelivery with the same message id is a no-op", func(t *testing.T) {
					assert.NoError(t, pushID("msg-1"))
					assert.Equal(t, int64(3), stockReady()) // not 6 — the redelivery was deduped
					assert.Equal(t, int64(1), dedupCount()) // still one inbox row
				})

				t.Run("a different message id is processed (id-based, not content-based)", func(t *testing.T) {
					assert.NoError(t, pushID("msg-2"))
					assert.Equal(t, int64(6), stockReady()) // restock applied again under a new id
					assert.Equal(t, int64(2), dedupCount())
				})
			})
		})
}
