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

func TestProductBatchList(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "product batch list",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(&inventory_models.StockBatch{}))

				at := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
				// product 5 / wh 9 has 3 batches; product 5 / wh 8 and product 6 / wh 9 are noise.
				assert.NoError(t, db.Create(&[]inventory_models.StockBatch{
					{ID: 1, ProductID: 5, WarehouseID: 9, InboundID: 11, StartCount: 0, EndCount: 10, Price: 4, CreatedAt: at, UpdatedAt: at},
					{ID: 2, ProductID: 5, WarehouseID: 9, InboundID: 12, StartCount: 10, EndCount: 25, Price: 5, CreatedAt: at, UpdatedAt: at},
					{ID: 3, ProductID: 5, WarehouseID: 9, InboundID: 13, StartCount: 25, EndCount: 40, Price: 6, CreatedAt: at, UpdatedAt: at},
					{ID: 4, ProductID: 5, WarehouseID: 8, InboundID: 14, StartCount: 0, EndCount: 7, Price: 3, CreatedAt: at, UpdatedAt: at},
					{ID: 5, ProductID: 6, WarehouseID: 9, InboundID: 15, StartCount: 0, EndCount: 9, Price: 2, CreatedAt: at, UpdatedAt: at},
				}).Error)

				svc := inventory.NewInventoryService(db)
				ctx := context.Background()

				t.Run("page nil is invalid argument", func(t *testing.T) {
					_, err := svc.ProductBatchList(ctx, connect.NewRequest(&inventory_iface.ProductBatchListRequest{
						ProductId: 5, WarehouseId: 9,
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})

				t.Run("scoped to product + warehouse, newest first", func(t *testing.T) {
					res, err := svc.ProductBatchList(ctx, connect.NewRequest(&inventory_iface.ProductBatchListRequest{
						ProductId: 5, WarehouseId: 9,
						Page: &common.PageFilter{Page: 1, Limit: 10},
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Batches, 3)
					assert.Equal(t, int64(3), res.Msg.PageInfo.TotalItems)
					assert.Equal(t, int64(1), res.Msg.PageInfo.TotalPage)
					// id DESC
					assert.Equal(t, uint64(3), res.Msg.Batches[0].Id)
					// field mapping on the newest batch
					assert.Equal(t, uint64(5), res.Msg.Batches[0].ProductId)
					assert.Equal(t, uint64(9), res.Msg.Batches[0].WarehouseId)
					assert.Equal(t, uint64(13), res.Msg.Batches[0].InboundId)
					assert.Equal(t, int64(40), res.Msg.Batches[0].EndCount)
					assert.Equal(t, float64(6), res.Msg.Batches[0].Price)
				})

				t.Run("pagination splits pages", func(t *testing.T) {
					p1, err := svc.ProductBatchList(ctx, connect.NewRequest(&inventory_iface.ProductBatchListRequest{
						ProductId: 5, WarehouseId: 9,
						Page: &common.PageFilter{Page: 1, Limit: 2},
					}))
					assert.NoError(t, err)
					assert.Len(t, p1.Msg.Batches, 2)
					assert.Equal(t, int64(3), p1.Msg.PageInfo.TotalItems)
					assert.Equal(t, int64(2), p1.Msg.PageInfo.TotalPage)

					p2, err := svc.ProductBatchList(ctx, connect.NewRequest(&inventory_iface.ProductBatchListRequest{
						ProductId: 5, WarehouseId: 9,
						Page: &common.PageFilter{Page: 2, Limit: 2},
					}))
					assert.NoError(t, err)
					assert.Len(t, p2.Msg.Batches, 1)
					assert.Equal(t, uint64(1), p2.Msg.Batches[0].Id) // oldest, on page 2
				})
			})
		})
}
