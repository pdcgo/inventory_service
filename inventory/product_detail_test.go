package inventory_test

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory"
	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestProductDetail(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "product detail",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				// productRow/teamRow are defined in rack_list_test.go / product_list_test.go.
				assert.NoError(t, db.AutoMigrate(
					&inventory_models.StockState{},
					&inventory_models.StockBatch{},
					&inventory_models.StockPlacement{},
					&inventory_models.ProductConfig{},
					&productRow{},
					&teamRow{},
				))
				at := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
				assert.NoError(t, db.Create(&teamRow{ID: 2, Name: "Team B"}).Error)
				assert.NoError(t, db.Create(&productRow{ID: 5, TeamID: 2, Name: "Widget"}).Error)
				assert.NoError(t, db.Create(&inventory_models.StockState{ProductID: 5, WarehouseID: 9, StockReady: 10, StockReadyAmount: 100, CreatedAt: at, UpdatedAt: at}).Error)
				// 2 open batches + 1 depleted (end_count 0, excluded).
				assert.NoError(t, db.Create(&[]inventory_models.StockBatch{
					{ProductID: 5, WarehouseID: 9, BatchCode: "b1", StartCount: 5, EndCount: 5, Price: 10, CreatedAt: at, UpdatedAt: at},
					{ProductID: 5, WarehouseID: 9, BatchCode: "b2", StartCount: 3, EndCount: 3, Price: 10, CreatedAt: at, UpdatedAt: at},
					{ProductID: 5, WarehouseID: 9, BatchCode: "b3", StartCount: 2, EndCount: 0, Price: 10, CreatedAt: at, UpdatedAt: at},
				}).Error)
				// racks 11+12 hold stock; rack 13 is zeroed (excluded).
				assert.NoError(t, db.Create(&[]inventory_models.StockPlacement{
					{ProductID: 5, WarehouseID: 9, RackID: 11, Count: 4, CreatedAt: at, UpdatedAt: at},
					{ProductID: 5, WarehouseID: 9, RackID: 12, Count: 6, CreatedAt: at, UpdatedAt: at},
					{ProductID: 5, WarehouseID: 9, RackID: 13, Count: 0, CreatedAt: at, UpdatedAt: at},
				}).Error)

				svc := inventory.NewInventoryService(db)

				t.Run("returns stock aggregates + team + default config", func(t *testing.T) {
					res, err := svc.ProductDetail(t.Context(), connect.NewRequest(&inventory_iface.ProductDetailRequest{ProductId: 5, WarehouseId: 9}))
					assert.NoError(t, err)
					assert.Equal(t, "Widget", res.Msg.Name)
					assert.Equal(t, uint64(2), res.Msg.TeamId)
					assert.Equal(t, "Team B", res.Msg.TeamName)
					assert.Equal(t, int64(10), res.Msg.StockReady)
					assert.Equal(t, float64(100), res.Msg.StockReadyAmount)
					assert.Equal(t, int64(2), res.Msg.BatchCount) // b1, b2 (b3 depleted)
					assert.Equal(t, int64(2), res.Msg.RackCount)  // racks 11, 12 (13 zeroed)
					assert.Equal(t, inventory_iface.QueueType_QUEUE_TYPE_FIFO, res.Msg.QueueType)
					assert.False(t, res.Msg.Configured)
					assert.NotNil(t, res.Msg.UpdatedAt)
				})

				t.Run("reflects a stored config", func(t *testing.T) {
					assert.NoError(t, db.Create(&inventory_models.ProductConfig{
						ProductID: 5, WarehouseID: 9,
						QueueType:        inventory_iface.QueueType_QUEUE_TYPE_LIFO,
						PlacementPicking: inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_BIGGER,
					}).Error)
					res, err := svc.ProductDetail(t.Context(), connect.NewRequest(&inventory_iface.ProductDetailRequest{ProductId: 5, WarehouseId: 9}))
					assert.NoError(t, err)
					assert.Equal(t, inventory_iface.QueueType_QUEUE_TYPE_LIFO, res.Msg.QueueType)
					assert.True(t, res.Msg.Configured)
				})

				t.Run("untracked product is not found", func(t *testing.T) {
					_, err := svc.ProductDetail(t.Context(), connect.NewRequest(&inventory_iface.ProductDetailRequest{ProductId: 999, WarehouseId: 9}))
					assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
				})
			})
		},
	)
}
