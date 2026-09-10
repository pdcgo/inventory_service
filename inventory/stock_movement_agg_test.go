package inventory_test

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory"
	"github.com/pdcgo/inventory_service/inventory_models"
	"github.com/pdcgo/schema/services/common/v1"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// jakarta is the business timezone the daily buckets are cut in.
var jakarta = time.FixedZone("Asia/Jakarta", 7*60*60)

// TestStockMovementAggregations covers the two rollups over one product: per-day totals and
// the split by change type. Product 1 lives in two warehouses so the tests also pin down
// what an unscoped (warehouse_id 0) query does with balances, which cannot simply be summed.
func TestStockMovementAggregations(t *testing.T) {
	var dbScenario moretest_mock.DbScenario

	moretest.Suite(t, "test stock movement aggregations",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&dbScenario),
		},
		func(t *testing.T) {
			dbScenario(t, func(db *gorm.DB) {
				// the movement list joins transaction info, so those tables must exist even
				// though this fixture leaves them empty.
				assert.NoError(t, db.AutoMigrate(
					&inventory_models.StockBatchLog{},
					&db_models.InvTransaction{},
					&db_models.Team{},
					&db_models.User{},
				))

				dayA := time.Date(2026, 8, 8, 9, 0, 0, 0, jakarta)
				dayB := time.Date(2026, 8, 9, 9, 0, 0, 0, jakarta)

				logs := []inventory_models.StockBatchLog{
					// warehouse 1, day A: +10 @100, then -4 @100 -> closes at 6 (600).
					{ID: 1, ProductID: 1, WarehouseID: 1, ChangeType: inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK,
						Change: 10, Price: 100, BalanceCount: 10, BalanceAmount: 1000, CreatedAt: dayA},
					{ID: 2, ProductID: 1, WarehouseID: 1, ChangeType: inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_CREATED,
						Change: -4, Price: 100, BalanceCount: 6, BalanceAmount: 600, CreatedAt: dayA.Add(2 * time.Hour)},
					// warehouse 1, day B: +5 @120 -> closes at 11 (1200).
					{ID: 3, ProductID: 1, WarehouseID: 1, ChangeType: inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK,
						Change: 5, Price: 120, BalanceCount: 11, BalanceAmount: 1200, CreatedAt: dayB},
					// warehouse 2, day B: +3 @200 -> closes at 3 (600).
					{ID: 4, ProductID: 1, WarehouseID: 2, ChangeType: inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK,
						Change: 3, Price: 200, BalanceCount: 3, BalanceAmount: 600, CreatedAt: dayB},
				}
				assert.NoError(t, db.Create(&logs).Error)

				service := inventory.NewInventoryService(db)
				page := &common.PageFilter{Page: 1, Limit: 10}

				daily := func(warehouseID uint64) []*inventory_iface.DailyMovementItem {
					res, err := service.StockMovementDaily(t.Context(), connect.NewRequest(
						&inventory_iface.StockMovementDailyRequest{
							ProductId:   1,
							WarehouseId: warehouseID,
							Page:        page,
						}))
					assert.NoError(t, err)
					return res.Msg.Days
				}

				t.Run("daily rolls flow and closing position per day", func(t *testing.T) {
					days := daily(1)
					assert.Len(t, days, 2)

					// newest first
					b := days[0]
					assert.Equal(t, time.Date(2026, 8, 9, 0, 0, 0, 0, jakarta), b.Day.AsTime().In(jakarta))
					assert.Equal(t, int64(5), b.TotalIn)
					assert.Equal(t, int64(0), b.TotalOut)
					assert.Equal(t, float64(600), b.AmountIn)
					assert.Equal(t, int64(11), b.BalanceCount)
					assert.Equal(t, float64(1200), b.BalanceAmount)

					a := days[1]
					assert.Equal(t, time.Date(2026, 8, 8, 0, 0, 0, 0, jakarta), a.Day.AsTime().In(jakarta))
					assert.Equal(t, int64(10), a.TotalIn)
					// out is a positive magnitude, not a negative number
					assert.Equal(t, int64(4), a.TotalOut)
					assert.Equal(t, float64(1000), a.AmountIn)
					assert.Equal(t, float64(400), a.AmountOut)
					// closing position of the day, not the day's net
					assert.Equal(t, int64(6), a.BalanceCount)
					assert.Equal(t, float64(600), a.BalanceAmount)
					assert.Equal(t, float64(100), a.Price)
				})

				t.Run("unscoped warehouse sums each warehouse's own closing balance", func(t *testing.T) {
					days := daily(0)
					assert.Len(t, days, 2)

					b := days[0]
					assert.Equal(t, int64(8), b.TotalIn)            // 5 + 3
					assert.Equal(t, float64(1200), b.AmountIn)      // 600 + 600
					assert.Equal(t, int64(14), b.BalanceCount)      // 11 + 3, not double counted
					assert.Equal(t, float64(1800), b.BalanceAmount) // 1200 + 600
					assert.InDelta(t, 1800.0/14.0, b.Price, 0.0001)

					// day A only exists in warehouse 1, so it is unchanged
					assert.Equal(t, int64(6), days[1].BalanceCount)
				})

				t.Run("breakdown splits net change by change type", func(t *testing.T) {
					res, err := service.StockMovementBreakdown(t.Context(), connect.NewRequest(
						&inventory_iface.StockMovementBreakdownRequest{
							ProductId:   1,
							WarehouseId: 1,
						}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Breakdowns, 2)

					// ordered by change type: ORDER_CREATED (1) then RESTOCK (3)
					out := res.Msg.Breakdowns[0]
					assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_CREATED, out.ChangeType)
					assert.Equal(t, int64(-4), out.ChangeCount)
					assert.Equal(t, float64(-400), out.ChangeAmount)
					assert.Equal(t, int64(1), out.TransactionCount)

					in := res.Msg.Breakdowns[1]
					assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK, in.ChangeType)
					assert.Equal(t, int64(15), in.ChangeCount)      // 10 + 5
					assert.Equal(t, float64(1600), in.ChangeAmount) // 1000 + 600
					assert.Equal(t, int64(2), in.TransactionCount)

					// the rows sum to the window's net stock change
					assert.Equal(t, int64(11), out.ChangeCount+in.ChangeCount)
				})

				t.Run("selling movement spans warehouses and carries value", func(t *testing.T) {
					res, err := service.StockMovementSelling(t.Context(), connect.NewRequest(
						&inventory_iface.StockMovementSellingRequest{
							ProductId: 1,
							Page:      page,
						}))
					assert.NoError(t, err)
					// all four rows: both warehouses, since warehouse_id was omitted
					assert.Len(t, res.Msg.Movements, 4)

					newest := res.Msg.Movements[0]
					assert.Equal(t, uint64(4), newest.Id)
					assert.Equal(t, uint64(2), newest.WarehouseId)
					assert.Equal(t, float64(200), newest.Price)
					// unscoped, so the position is the running total across both warehouses —
					// not warehouse 2's own stored 3 / 600
					assert.Equal(t, int64(14), newest.BalanceCount)      // 10 - 4 + 5 + 3
					assert.Equal(t, float64(1800), newest.BalanceAmount) // 1000 - 400 + 600 + 600
				})

				t.Run("selling movement still narrows when a warehouse is given", func(t *testing.T) {
					res, err := service.StockMovementSelling(t.Context(), connect.NewRequest(
						&inventory_iface.StockMovementSellingRequest{
							ProductId:   1,
							WarehouseId: 2,
							Page:        page,
						}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Movements, 1)
					assert.Equal(t, uint64(4), res.Msg.Movements[0].Id)
					// scoped, so the stored per-warehouse position is read straight out
					assert.Equal(t, int64(3), res.Msg.Movements[0].BalanceCount)
					assert.Equal(t, float64(600), res.Msg.Movements[0].BalanceAmount)
				})

				t.Run("selling movement filters by the timestamp range", func(t *testing.T) {
					// window starting midway through day B: only that day's rows survive.
					res, err := service.StockMovementSelling(t.Context(), connect.NewRequest(
						&inventory_iface.StockMovementSellingRequest{
							ProductId: 1,
							TimeRange: &common.TimeFilterRange{
								StartDate: timestamppb.New(dayB.Add(-time.Hour)),
							},
							Page: page,
						}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Movements, 2)
				})
			})
		},
	)
}
