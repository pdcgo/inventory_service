package inventory_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory"
	"github.com/pdcgo/inventory_service/inventory_models"
	common "github.com/pdcgo/schema/services/common/v1"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestProductPlacementList(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "product placement list",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(&inventory_models.StockPlacement{}))

				at := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
				// product 5 / wh 9 placed in racks 41 and 42; other rows are noise.
				assert.NoError(t, db.Create(&[]inventory_models.StockPlacement{
					{ID: 1, ProductID: 5, WarehouseID: 9, RackID: 42, Count: 7, CreatedAt: at, UpdatedAt: at},
					{ID: 2, ProductID: 5, WarehouseID: 9, RackID: 41, Count: 3, CreatedAt: at, UpdatedAt: at},
					{ID: 3, ProductID: 5, WarehouseID: 8, RackID: 41, Count: 9, CreatedAt: at, UpdatedAt: at},
					{ID: 4, ProductID: 6, WarehouseID: 9, RackID: 41, Count: 2, CreatedAt: at, UpdatedAt: at},
				}).Error)

				svc := inventory.NewInventoryService(db)
				ctx := context.Background()

				t.Run("page nil is invalid argument", func(t *testing.T) {
					_, err := svc.ProductPlacementList(ctx, connect.NewRequest(&inventory_iface.ProductPlacementListRequest{
						ProductId: 5, WarehouseId: 9,
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})

				t.Run("scoped to product + warehouse, ordered by rack", func(t *testing.T) {
					res, err := svc.ProductPlacementList(ctx, connect.NewRequest(&inventory_iface.ProductPlacementListRequest{
						ProductId: 5, WarehouseId: 9,
						Page: &common.PageFilter{Page: 1, Limit: 10},
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Placements, 2)
					assert.Equal(t, int64(2), res.Msg.PageInfo.TotalItems)
					// rack_id ASC -> rack 41 first
					assert.Equal(t, uint64(41), res.Msg.Placements[0].RackId)
					assert.Equal(t, int64(3), res.Msg.Placements[0].Count)
					assert.Equal(t, uint64(42), res.Msg.Placements[1].RackId)
				})

				t.Run("optional rack_id filter", func(t *testing.T) {
					res, err := svc.ProductPlacementList(ctx, connect.NewRequest(&inventory_iface.ProductPlacementListRequest{
						ProductId: 5, WarehouseId: 9, RackId: 42,
						Page: &common.PageFilter{Page: 1, Limit: 10},
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Placements, 1)
					assert.Equal(t, uint64(42), res.Msg.Placements[0].RackId)
					assert.Equal(t, int64(7), res.Msg.Placements[0].Count)
				})
			})
		})
}
