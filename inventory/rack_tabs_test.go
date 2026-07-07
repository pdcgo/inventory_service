package inventory_test

import (
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

// Rack Detail "Products" tab: products currently on a rack, names + counts, rack-scoped.
func TestRackProductList(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "rack product list",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(&inventory_models.StockPlacement{}, &productRow{}))
				at := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
				assert.NoError(t, db.Create(&[]productRow{
					{ID: 5, TeamID: 2, Name: "Widget"},
					{ID: 6, TeamID: 2, Name: "Gadget"},
					{ID: 7, TeamID: 2, Name: "Zeroed"},
				}).Error)
				// StockPlacement is unique on (product, warehouse, rack), so each row is a distinct triple.
				assert.NoError(t, db.Create(&[]inventory_models.StockPlacement{
					{ProductID: 5, WarehouseID: 9, RackID: 11, Count: 4, CreatedAt: at, UpdatedAt: at},
					{ProductID: 6, WarehouseID: 9, RackID: 11, Count: 6, CreatedAt: at, UpdatedAt: at},
					{ProductID: 7, WarehouseID: 9, RackID: 11, Count: 0, CreatedAt: at, UpdatedAt: at},  // zeroed → excluded
					{ProductID: 5, WarehouseID: 9, RackID: 12, Count: 3, CreatedAt: at, UpdatedAt: at},  // other rack → excluded
					{ProductID: 6, WarehouseID: 99, RackID: 11, Count: 7, CreatedAt: at, UpdatedAt: at}, // other warehouse → excluded
				}).Error)

				svc := inventory.NewInventoryService(db)

				res, err := svc.RackProductList(t.Context(), connect.NewRequest(&inventory_iface.RackProductListRequest{
					RackId: 11, WarehouseId: 9, Page: &common.PageFilter{Page: 1, Limit: 50},
				}))
				assert.NoError(t, err)
				assert.Len(t, res.Msg.GetItems(), 2)
				// ordered by count DESC: Gadget(6) then Widget(4).
				assert.Equal(t, uint64(6), res.Msg.GetItems()[0].GetProductId())
				assert.Equal(t, "Gadget", res.Msg.GetItems()[0].GetProductName())
				assert.Equal(t, int64(6), res.Msg.GetItems()[0].GetCount())
				assert.Equal(t, "Widget", res.Msg.GetItems()[1].GetProductName())
				assert.Equal(t, int64(4), res.Msg.GetItems()[1].GetCount())
			})
		},
	)
}

// Rack Detail "Histories" tab: the rack's placement log across products, time-filtered.
func TestRackHistory(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "rack history",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(&inventory_models.StockPlacementLog{}, &productRow{}))
				assert.NoError(t, db.Create(&[]productRow{
					{ID: 5, TeamID: 2, Name: "Widget"},
					{ID: 6, TeamID: 2, Name: "Gadget"},
				}).Error)
				mk := func(y, m, d int) time.Time { return time.Date(y, time.Month(m), d, 9, 0, 0, 0, time.UTC) }
				assert.NoError(t, db.Create(&[]inventory_models.StockPlacementLog{
					{ProductID: 5, WarehouseID: 9, RackID: 11, ChangeType: inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK, Change: 4, BalanceCount: 4, CreatedAt: mk(2026, 7, 4)}, // before window → excluded
					{ProductID: 5, WarehouseID: 9, RackID: 11, ChangeType: inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_MOVE, Change: -1, BalanceCount: 3, CreatedAt: mk(2026, 7, 6)},   // in window
					{ProductID: 6, WarehouseID: 9, RackID: 11, ChangeType: inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK, Change: 6, BalanceCount: 6, CreatedAt: mk(2026, 7, 6)}, // in window
					{ProductID: 5, WarehouseID: 9, RackID: 11, ChangeType: inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_MOVE, Change: 2, BalanceCount: 5, CreatedAt: mk(2026, 7, 9)},    // after window → excluded
					{ProductID: 5, WarehouseID: 9, RackID: 12, ChangeType: inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_MOVE, Change: 1, BalanceCount: 1, CreatedAt: mk(2026, 7, 6)},    // other rack → excluded
				}).Error)

				svc := inventory.NewInventoryService(db)

				res, err := svc.RackHistory(t.Context(), connect.NewRequest(&inventory_iface.RackHistoryRequest{
					RackId: 11, WarehouseId: 9,
					Page:      &common.PageFilter{Page: 1, Limit: 50},
					TimeRange: &common.TimeFilter{StartDate: mk(2026, 7, 5).UnixMicro(), EndDate: mk(2026, 7, 8).UnixMicro()},
				}))
				assert.NoError(t, err)
				assert.Len(t, res.Msg.GetItems(), 2) // only the two 2026-07-06 rows on rack 11
				names := map[uint64]string{}
				for _, it := range res.Msg.GetItems() {
					names[it.GetProductId()] = it.GetProductName()
				}
				assert.Equal(t, "Widget", names[5])
				assert.Equal(t, "Gadget", names[6])
			})
		},
	)
}
