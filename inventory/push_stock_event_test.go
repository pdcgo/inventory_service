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
	ID          db_models.SkuID `gorm:"primarykey"`
	ProductID   uint
	WarehouseID uint
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
					&db_models.WarehouseTransfer{},
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

				t.Run("applies a multi-product restock (deterministic lock order)", func(t *testing.T) {
					mpAt := time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC)
					sku30, err := db_models.NewSkuID(&db_models.SkuData{WarehouseID: 9, TeamID: 1, ProductID: 30, VariantID: 1})
					assert.NoError(t, err)
					sku31, err := db_models.NewSkuID(&db_models.SkuData{WarehouseID: 9, TeamID: 1, ProductID: 31, VariantID: 1})
					assert.NoError(t, err)

					assert.NoError(t, db.Create(&db_models.InvTransaction{
						ID: 500, TeamID: 1, WarehouseID: 9, CreateByID: 7,
						Type: db_models.InvTxRestock, Status: db_models.InvTxCompleted, Created: mpAt,
					}).Error)
					assert.NoError(t, db.Create(&[]db_models.InvTxItem{
						{ID: 5000, InvTransactionID: 500, SkuID: sku30, Count: 6, Price: 2, Total: 12},
						{ID: 5001, InvTransactionID: 500, SkuID: sku31, Count: 4, Price: 5, Total: 20},
					}).Error)

					_, err = svc.PushStockEvent(t.Context(), connect.NewRequest(&inventory_iface.PushStockEventRequest{
						Event: &warehouse_iface.StockEvent{
							Data: &warehouse_iface.StockEvent_RestockAccepted{
								RestockAccepted: &warehouse_iface.RestockAccepted{TransactionId: 500},
							},
						},
					}))
					assert.NoError(t, err)

					// both products reconcile regardless of expansion/lock order.
					var s30, s31 inventory_models.StockState
					assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ?", uint64(30), uint64(9)).First(&s30).Error)
					assert.Equal(t, int64(6), s30.StockReady)
					assert.Equal(t, float64(12), s30.StockReadyAmount)
					assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ?", uint64(31), uint64(9)).First(&s31).Error)
					assert.Equal(t, int64(4), s31.StockReady)
					assert.Equal(t, float64(20), s31.StockReadyAmount)
				})

				t.Run("applies a found-back event", func(t *testing.T) {
					// distinct tx/product so it doesn't collide with the restock seed.
					fbAt := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
					fbSku, err := db_models.NewSkuID(&db_models.SkuData{WarehouseID: 9, TeamID: 1, ProductID: 8, VariantID: 1})
					assert.NoError(t, err)

					assert.NoError(t, db.Create(&db_models.InvTransaction{
						ID: 200, TeamID: 1, WarehouseID: 9, CreateByID: 7,
						Type: db_models.InvTxRestock, Status: db_models.InvTxCompleted, Created: fbAt,
					}).Error)
					assert.NoError(t, db.Create(&db_models.InvTxItem{
						ID: 3000, InvTransactionID: 200, SkuID: fbSku, Count: 4, Price: 5, Total: 20,
					}).Error)

					res, err := svc.PushStockEvent(t.Context(), connect.NewRequest(&inventory_iface.PushStockEventRequest{
						Event: &warehouse_iface.StockEvent{
							Data: &warehouse_iface.StockEvent_StockFoundBack{
								StockFoundBack: &warehouse_iface.StockFoundBack{TransactionId: 200},
							},
						},
					}))
					assert.NoError(t, err)
					assert.NotNil(t, res)

					// found-back adds stock, like restock.
					var st inventory_models.StockState
					assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ?", uint64(8), uint64(9)).First(&st).Error)
					assert.Equal(t, int64(4), st.StockReady)
					assert.Equal(t, float64(20), st.StockReadyAmount)
				})

				t.Run("applies a warehouse transfer (out then in)", func(t *testing.T) {
					tAt := time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)
					outTxID, inTxID, xferID := uint(300), uint(301), uint(50)

					skuSrc, err := db_models.NewSkuID(&db_models.SkuData{WarehouseID: 9, TeamID: 1, ProductID: 20, VariantID: 1})
					assert.NoError(t, err)
					skuDst, err := db_models.NewSkuID(&db_models.SkuData{WarehouseID: 10, TeamID: 1, ProductID: 20, VariantID: 1})
					assert.NoError(t, err)
					assert.NoError(t, db.Create(&skuRow{ID: skuSrc, ProductID: 20}).Error)
					assert.NoError(t, db.Create(&skuRow{ID: skuDst, ProductID: 20}).Error)

					// outbound tx at source wh 9, inbound tx at dest wh 10 — both positive counts.
					assert.NoError(t, db.Create(&db_models.InvTransaction{
						ID: outTxID, TeamID: 1, WarehouseID: 9, CreateByID: 7,
						Type: db_models.InvTxTransferOut, Status: db_models.InvTxCompleted, Created: tAt,
					}).Error)
					assert.NoError(t, db.Create(&db_models.InvTxItem{
						ID: 4000, InvTransactionID: outTxID, SkuID: skuSrc, Count: 5, Price: 4, Total: 20,
					}).Error)
					assert.NoError(t, db.Create(&db_models.InvTransaction{
						ID: inTxID, TeamID: 1, WarehouseID: 10, CreateByID: 7,
						Type: db_models.InvTxTransferIn, Status: db_models.InvTxCompleted, Created: tAt,
					}).Error)
					assert.NoError(t, db.Create(&db_models.InvTxItem{
						ID: 4001, InvTransactionID: inTxID, SkuID: skuDst, Count: 5, Price: 4, Total: 20,
					}).Error)
					assert.NoError(t, db.Create(&db_models.WarehouseTransfer{
						ID: xferID, OutboundTxID: outTxID, InboundTxID: inTxID, FromWarehouseID: 9, ToWarehouseID: 10, TeamID: 1,
					}).Error)
					// positive tx_id rack rows so the placement processor finds the racks.
					assert.NoError(t, db.Create(&[]db_models.InvertoryHistory{
						{TxID: &outTxID, SkuID: skuSrc, WarehouseID: 9, RackID: 41, Count: 5, Created: tAt},
						{TxID: &inTxID, SkuID: skuDst, WarehouseID: 10, RackID: 42, Count: 5, Created: tAt},
					}).Error)

					// created -> OUT leg: source warehouse 9 stock + rack decrement.
					_, err = svc.PushStockEvent(t.Context(), connect.NewRequest(&inventory_iface.PushStockEventRequest{
						Event: &warehouse_iface.StockEvent{Data: &warehouse_iface.StockEvent_TransferWarehouseCreated{
							TransferWarehouseCreated: &warehouse_iface.TransferWarehouseCreated{TransferId: uint64(xferID)},
						}},
					}))
					assert.NoError(t, err)

					var src inventory_models.StockState
					assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ?", uint64(20), uint64(9)).First(&src).Error)
					assert.Equal(t, int64(-5), src.StockReady)
					var srcRack inventory_models.StockPlacement
					assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ? AND rack_id = ?", uint64(20), uint64(9), uint64(41)).First(&srcRack).Error)
					assert.Equal(t, int64(-5), srcRack.Count)

					// accepted -> IN leg: destination warehouse 10 stock + rack increment.
					_, err = svc.PushStockEvent(t.Context(), connect.NewRequest(&inventory_iface.PushStockEventRequest{
						Event: &warehouse_iface.StockEvent{Data: &warehouse_iface.StockEvent_TransferWarehouseAccepted{
							TransferWarehouseAccepted: &warehouse_iface.TransferWarehouseAccepted{TransferId: uint64(xferID)},
						}},
					}))
					assert.NoError(t, err)

					var dst inventory_models.StockState
					assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ?", uint64(20), uint64(10)).First(&dst).Error)
					assert.Equal(t, int64(5), dst.StockReady)
					var dstRack inventory_models.StockPlacement
					assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ? AND rack_id = ?", uint64(20), uint64(10), uint64(42)).First(&dstRack).Error)
					assert.Equal(t, int64(5), dstRack.Count)
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
