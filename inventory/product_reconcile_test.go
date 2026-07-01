package inventory_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory"
	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/schema/services/inventory_iface/v1/inventory_ifaceconnect"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestProductReconcileRPC(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "product reconcile rpc",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(
					&db_models.InvertoryHistory{},
					&skuRow{}, // skus stand-in (defined in push_stock_event_test.go)
					&inventory_models.StockState{},
					&inventory_models.StockBatchLog{},
					&inventory_models.StockBatch{},
					&inventory_models.StockPlacement{},
					&inventory_models.StockPlacementLog{},
				))

				at := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)

				// two variants of product 5 in warehouse 9.
				sku1, err := db_models.NewSkuID(&db_models.SkuData{WarehouseID: 9, TeamID: 1, ProductID: 5, VariantID: 1})
				assert.NoError(t, err)
				sku2, err := db_models.NewSkuID(&db_models.SkuData{WarehouseID: 9, TeamID: 1, ProductID: 5, VariantID: 2})
				assert.NoError(t, err)
				assert.NoError(t, db.Create(&[]skuRow{{ID: sku1, ProductID: 5, WarehouseID: 9}, {ID: sku2, ProductID: 5, WarehouseID: 9}}).Error)

				outboundTx := uint(999)
				// legacy on-hand (tx_id null), unit value = price + ext = 10:
				// rack 11 → 5 + 2 = 7, rack 12 → 3 (total 10, value 100).
				assert.NoError(t, db.Create(&[]db_models.InvertoryHistory{
					{SkuID: sku1, WarehouseID: 9, RackID: 11, Count: -5, Price: 8, ExtPrice: 2},
					{SkuID: sku1, WarehouseID: 9, RackID: 12, Count: -3, Price: 8, ExtPrice: 2},
					{SkuID: sku2, WarehouseID: 9, RackID: 11, Count: -2, Price: 8, ExtPrice: 2},
					// outbound row (tx_id set) ignored by the tx_id IS NULL filter
					{SkuID: sku1, WarehouseID: 9, RackID: 11, Count: 1, Price: 8, ExtPrice: 2, TxID: &outboundTx},
				}).Error)

				// StockState lags (2/20 vs 10/100); rack 13 holds stale placement (4).
				assert.NoError(t, db.Create(&inventory_models.StockState{
					ProductID: 5, WarehouseID: 9, StockReady: 2, StockReadyAmount: 20, CreatedAt: at, UpdatedAt: at,
				}).Error)
				assert.NoError(t, db.Create(&inventory_models.StockPlacement{
					ProductID: 5, WarehouseID: 9, RackID: 13, Count: 4, CreatedAt: at, UpdatedAt: at,
				}).Error)

				// Real streaming RPC, exercised through an in-process connect server so the
				// server-stream path is covered end to end — connect's ServerStream has no
				// exported constructor, so the handler can't be called directly. Mirrors
				// user_service/user/team_sync_legacy_test.go.
				svc := inventory.NewInventoryService(db)
				mux := http.NewServeMux()
				mux.Handle(inventory_ifaceconnect.NewInventoryServiceHandler(svc))
				srv := httptest.NewServer(mux)
				defer srv.Close()
				client := inventory_ifaceconnect.NewInventoryServiceClient(srv.Client(), srv.URL)

				// reconcile runs ProductReconcile and returns how many progress lines streamed.
				reconcile := func(productID, warehouseID uint64) (int, error) {
					stream, err := client.ProductReconcile(context.Background(),
						connect.NewRequest(&inventory_iface.ProductReconcileRequest{ProductId: productID, WarehouseId: warehouseID}))
					if err != nil {
						return 0, err
					}
					defer stream.Close()
					n := 0
					for stream.Receive() {
						n++
					}
					return n, stream.Err()
				}

				n, err := reconcile(5, 9)
				assert.NoError(t, err)
				assert.Equal(t, 5, n) // "reconcile state" + 3 "reconcile placement" (racks 11/12/13) + "reconcile batch"

				// StockState reconciled to legacy total.
				var st inventory_models.StockState
				assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ?", uint64(5), uint64(9)).First(&st).Error)
				assert.Equal(t, int64(10), st.StockReady)
				assert.Equal(t, float64(100), st.StockReadyAmount)

				placementOf := func(rackID uint64) inventory_models.StockPlacement {
					var p inventory_models.StockPlacement
					assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ? AND rack_id = ?", uint64(5), uint64(9), rackID).First(&p).Error)
					return p
				}
				assert.Equal(t, int64(7), placementOf(11).Count)
				assert.Equal(t, int64(3), placementOf(12).Count)
				assert.Equal(t, int64(0), placementOf(13).Count) // stale rack zeroed

				// one StockState adjustment (8) + three placement adjustments.
				var stateLogs int64
				assert.NoError(t, db.Model(&inventory_models.StockBatchLog{}).Where("product_id = ?", uint64(5)).Count(&stateLogs).Error)
				assert.Equal(t, int64(1), stateLogs)
				var placementLogs int64
				assert.NoError(t, db.Model(&inventory_models.StockPlacementLog{}).Where("product_id = ?", uint64(5)).Count(&placementLogs).Error)
				assert.Equal(t, int64(3), placementLogs) // racks 11, 12, 13

				// StockBatch: one legacy batch per sku group at unit value 10
				// (sku1 rack11+rack12 = 5+3 = 8; sku2 = 2).
				var batches []inventory_models.StockBatch
				assert.NoError(t, db.Where("product_id = ?", uint64(5)).Order("start_count desc").Find(&batches).Error)
				assert.Len(t, batches, 2)
				for _, b := range batches {
					assert.Equal(t, float64(10), b.Price)
					assert.Equal(t, b.StartCount, b.EndCount) // fresh legacy batch
				}
				assert.Equal(t, int64(8), batches[0].StartCount)
				assert.Equal(t, int64(2), batches[1].StartCount)

				t.Run("second run is a no-op", func(t *testing.T) {
					n, err := reconcile(5, 9)
					assert.NoError(t, err)
					assert.Equal(t, 5, n) // still streams progress, but writes no new logs/batches
					var cnt int64
					assert.NoError(t, db.Model(&inventory_models.StockPlacementLog{}).Count(&cnt).Error)
					assert.Equal(t, int64(3), cnt) // no new placement logs
					var batchCnt int64
					assert.NoError(t, db.Model(&inventory_models.StockBatch{}).Count(&batchCnt).Error)
					assert.Equal(t, int64(2), batchCnt) // OnConflict DoNothing → no duplicate batches
				})
			})
		},
	)
}
