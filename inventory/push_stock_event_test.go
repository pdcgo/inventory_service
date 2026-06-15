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

// stand-ins so the expansion/placement queries (left join inv_item_problems / skus)
// resolve without pulling those models' association graphs into the test.
type invItemProblem struct {
	ID       uint `gorm:"primarykey"`
	TxItemID uint
	Count    int
}

func (invItemProblem) TableName() string { return "inv_item_problems" }

type skuRow struct {
	ID        db_models.SkuID `gorm:"primarykey"`
	ProductID uint
}

func (skuRow) TableName() string { return "skus" }

func TestPushStockEventRPC(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "push stock event rpc",
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

				assert.NoError(t, db.Create(&db_models.InvTransaction{
					ID: 100, TeamID: 1, WarehouseID: 9, CreateByID: 7,
					Type: db_models.InvTxRestock, Status: db_models.InvTxCompleted, Created: at,
				}).Error)
				assert.NoError(t, db.Create(&db_models.InvTxItem{
					ID: 2000, InvTransactionID: 100, SkuID: sku, Count: 3, Price: 10, Total: 30,
				}).Error)

				svc := inventory.NewInventoryService(db)

				t.Run("applies a restock event", func(t *testing.T) {
					res, err := svc.PushStockEvent(t.Context(), connect.NewRequest(&inventory_iface.PushStockEventRequest{
						Event: &warehouse_iface.StockEvent{
							Data: &warehouse_iface.StockEvent_RestockAccepted{
								RestockAccepted: &warehouse_iface.RestockAccepted{TransactionId: 100},
							},
						},
					}))
					assert.NoError(t, err)
					assert.NotNil(t, res)

					var st inventory_models.StockState
					assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ?", uint64(5), uint64(9)).First(&st).Error)
					assert.Equal(t, int64(3), st.StockReady)
					assert.Equal(t, float64(30), st.StockReadyAmount)
				})

				t.Run("nil event is an invalid argument", func(t *testing.T) {
					_, err := svc.PushStockEvent(t.Context(), connect.NewRequest(&inventory_iface.PushStockEventRequest{}))
					assert.Error(t, err)
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})
			})
		},
	)
}
