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

func TestProductPlacementLog(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "product placement log",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(&inventory_models.StockPlacementLog{}))

				at := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
				// product 5 / wh 9: two log rows on rack 41, one on rack 42; others are noise.
				assert.NoError(t, db.Create(&[]inventory_models.StockPlacementLog{
					{ID: 1, ProductID: 5, WarehouseID: 9, RackID: 41, TransactionID: 100, UserID: 7, ChangeType: inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_CREATED, Change: -3, BalanceCount: -3, CreatedAt: at},
					{ID: 2, ProductID: 5, WarehouseID: 9, RackID: 41, TransactionID: 100, UserID: 7, ChangeType: inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_CANCELED, Change: 3, BalanceCount: 0, CreatedAt: at},
					{ID: 3, ProductID: 5, WarehouseID: 9, RackID: 42, TransactionID: 200, UserID: 8, ChangeType: inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK, Change: 5, BalanceCount: 5, CreatedAt: at},
					{ID: 4, ProductID: 6, WarehouseID: 9, RackID: 41, TransactionID: 300, UserID: 9, ChangeType: inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK, Change: 1, BalanceCount: 1, CreatedAt: at},
				}).Error)

				svc := inventory.NewInventoryService(db)
				ctx := context.Background()

				t.Run("page nil is invalid argument", func(t *testing.T) {
					_, err := svc.ProductPlacementLog(ctx, connect.NewRequest(&inventory_iface.ProductPlacementLogRequest{
						ProductId: 5, WarehouseId: 9,
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})

				t.Run("scoped to product + warehouse, newest first", func(t *testing.T) {
					res, err := svc.ProductPlacementLog(ctx, connect.NewRequest(&inventory_iface.ProductPlacementLogRequest{
						ProductId: 5, WarehouseId: 9,
						Page: &common.PageFilter{Page: 1, Limit: 10},
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Logs, 3)
					assert.Equal(t, int64(3), res.Msg.PageInfo.TotalItems)
					// id DESC
					assert.Equal(t, uint64(3), res.Msg.Logs[0].Id)
					assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK, res.Msg.Logs[0].ChangeType)
					assert.Equal(t, int64(5), res.Msg.Logs[0].Change)
					assert.Equal(t, uint64(200), res.Msg.Logs[0].TransactionId)
					assert.Equal(t, uint64(8), res.Msg.Logs[0].UserId)
				})

				t.Run("optional rack_id filter", func(t *testing.T) {
					res, err := svc.ProductPlacementLog(ctx, connect.NewRequest(&inventory_iface.ProductPlacementLogRequest{
						ProductId: 5, WarehouseId: 9, RackId: 41,
						Page: &common.PageFilter{Page: 1, Limit: 10},
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Logs, 2)
					for _, l := range res.Msg.Logs {
						assert.Equal(t, uint64(41), l.RackId)
					}
				})
			})
		})
}
