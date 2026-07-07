package inventory_test

import (
	"context"
	"testing"

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

const (
	mvWarehouse      uint64 = 9
	mvOtherWarehouse uint64 = 10
)

func TestPlacementMove(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "inventory placement move",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(
					&inventory_models.Rack{},
					&inventory_models.StockPlacement{},
					&inventory_models.StockPlacementLog{},
				))

				svc := inventory.NewInventoryService(db)
				ctx := context.Background()

				// racks 1-3 live in the warehouse; 4 is deleted; 5 belongs elsewhere.
				assert.NoError(t, db.Create(&inventory_models.Rack{ID: 1, WarehouseID: mvWarehouse, Name: "R1"}).Error)
				assert.NoError(t, db.Create(&inventory_models.Rack{ID: 2, WarehouseID: mvWarehouse, Name: "R2"}).Error)
				assert.NoError(t, db.Create(&inventory_models.Rack{ID: 3, WarehouseID: mvWarehouse, Name: "R3"}).Error)
				assert.NoError(t, db.Create(&inventory_models.Rack{ID: 4, WarehouseID: mvWarehouse, Name: "R4", Deleted: true}).Error)
				assert.NoError(t, db.Create(&inventory_models.Rack{ID: 5, WarehouseID: mvOtherWarehouse, Name: "R5"}).Error)

				// moves never touch StockState, so placements can be seeded directly.
				assert.NoError(t, db.Create(&inventory_models.StockPlacement{ProductID: 1, WarehouseID: mvWarehouse, RackID: 1, Count: 10}).Error)
				assert.NoError(t, db.Create(&inventory_models.StockPlacement{ProductID: 2, WarehouseID: mvWarehouse, RackID: 1, Count: 5}).Error)

				placementCount := func(productID, rackID uint64) int64 {
					var pl inventory_models.StockPlacement
					res := db.
						Where("product_id = ? AND warehouse_id = ? AND rack_id = ?", productID, mvWarehouse, rackID).
						Limit(1).
						Find(&pl)
					assert.NoError(t, res.Error)
					return pl.Count
				}
				placementSum := func(productID uint64) int64 {
					var sum int64
					err := db.
						Model(&inventory_models.StockPlacement{}).
						Select("COALESCE(SUM(count), 0)").
						Where("product_id = ? AND warehouse_id = ?", productID, mvWarehouse).
						Scan(&sum).
						Error
					assert.NoError(t, err)
					return sum
				}
				logCount := func() int64 {
					var n int64
					assert.NoError(t, db.Model(&inventory_models.StockPlacementLog{}).Count(&n).Error)
					return n
				}
				move := func(note string, items ...*inventory_iface.PlacementMoveItem) error {
					_, err := svc.PlacementMove(ctx, connect.NewRequest(&inventory_iface.PlacementMoveRequest{
						WarehouseId: mvWarehouse,
						Placements:  items,
						Note:        note,
					}))
					return err
				}
				item := func(productID, from, to uint64, count int64) *inventory_iface.PlacementMoveItem {
					return &inventory_iface.PlacementMoveItem{
						ProductId:  productID,
						FromRackId: from,
						ToRackId:   to,
						Count:      count,
					}
				}

				t.Run("single partial move writes paired MOVE logs with note", func(t *testing.T) {
					err := move("rapikan", item(1, 1, 2, 4))
					assert.NoError(t, err)

					assert.Equal(t, int64(6), placementCount(1, 1))
					assert.Equal(t, int64(4), placementCount(1, 2)) // row auto-created
					assert.Equal(t, int64(10), placementSum(1))     // conserved
					assert.Equal(t, int64(2), logCount())

					var logs []inventory_models.StockPlacementLog
					assert.NoError(t, db.Order("id ASC").Find(&logs).Error)
					assert.Len(t, logs, 2)
					// lock order is ascending rack_id: r1 out leg first.
					assert.Equal(t, int64(-4), logs[0].Change)
					assert.Equal(t, int64(6), logs[0].BalanceCount)
					assert.Equal(t, uint64(1), logs[0].RackID)
					assert.Equal(t, int64(4), logs[1].Change)
					assert.Equal(t, int64(4), logs[1].BalanceCount)
					assert.Equal(t, uint64(2), logs[1].RackID)
					for _, lg := range logs {
						assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_MOVE, lg.ChangeType)
						assert.Equal(t, "rapikan", lg.Note)
						assert.Equal(t, uint64(0), lg.TransactionID)
						assert.Equal(t, uint64(0), lg.UserID)
					}
				})

				t.Run("multi-item move in one call", func(t *testing.T) {
					err := move("konsolidasi", item(1, 2, 3, 2), item(2, 1, 3, 5))
					assert.NoError(t, err)

					assert.Equal(t, int64(6), placementCount(1, 1))
					assert.Equal(t, int64(2), placementCount(1, 2))
					assert.Equal(t, int64(2), placementCount(1, 3))
					assert.Equal(t, int64(0), placementCount(2, 1))
					assert.Equal(t, int64(5), placementCount(2, 3))
					assert.Equal(t, int64(10), placementSum(1))
					assert.Equal(t, int64(5), placementSum(2))
					assert.Equal(t, int64(6), logCount())
				})

				t.Run("move-all keeps the emptied source row", func(t *testing.T) {
					err := move("", item(2, 3, 1, 5))
					assert.NoError(t, err)

					assert.Equal(t, int64(5), placementCount(2, 1))
					assert.Equal(t, int64(0), placementCount(2, 3))
					var rows int64
					assert.NoError(t, db.
						Model(&inventory_models.StockPlacement{}).
						Where("product_id = ? AND rack_id = ?", 2, 3).
						Count(&rows).
						Error)
					assert.Equal(t, int64(1), rows) // kept at 0, not deleted
				})

				t.Run("note surfaces via ProductPlacementLog rpc", func(t *testing.T) {
					res, err := svc.ProductPlacementLog(ctx, connect.NewRequest(&inventory_iface.ProductPlacementLogRequest{
						ProductId:   1,
						WarehouseId: mvWarehouse,
						Page:        &common.PageFilter{Page: 1, Limit: 10},
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Logs, 4)
					notes := map[string]bool{}
					for _, lg := range res.Msg.Logs {
						assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_MOVE, lg.ChangeType)
						notes[lg.Note] = true
					}
					assert.True(t, notes["rapikan"])
					assert.True(t, notes["konsolidasi"])
				})

				t.Run("cumulative out-legs overdraft rolls everything back", func(t *testing.T) {
					before := logCount()
					// P1 on r1 has 6; the two out legs together need 7.
					err := move("too much", item(1, 1, 2, 4), item(1, 1, 3, 3))
					assert.Error(t, err)
					assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

					assert.Equal(t, int64(6), placementCount(1, 1))
					assert.Equal(t, int64(2), placementCount(1, 2))
					assert.Equal(t, int64(2), placementCount(1, 3))
					assert.Equal(t, before, logCount())
				})

				t.Run("single item over balance", func(t *testing.T) {
					err := move("", item(2, 1, 2, 99))
					assert.Error(t, err)
					assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
					assert.Contains(t, err.Error(), "insufficient stock")
				})

				t.Run("move from rack with no placement row", func(t *testing.T) {
					err := move("", item(2, 2, 1, 1)) // P2 never placed on r2
					assert.Error(t, err)
					assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
				})

				t.Run("rack scoping", func(t *testing.T) {
					err := move("", item(1, 1, 5, 1)) // r5 belongs to another warehouse
					assert.Error(t, err)
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

					err = move("", item(1, 4, 1, 1)) // r4 is deleted
					assert.Error(t, err)
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})

				t.Run("validation", func(t *testing.T) {
					cases := []struct {
						name string
						run  func() error
					}{
						{"zero warehouse_id", func() error {
							_, err := svc.PlacementMove(ctx, connect.NewRequest(&inventory_iface.PlacementMoveRequest{
								Placements: []*inventory_iface.PlacementMoveItem{item(1, 1, 2, 1)},
							}))
							return err
						}},
						{"empty placements", func() error { return move("") }},
						{"zero product_id", func() error { return move("", item(0, 1, 2, 1)) }},
						{"zero from_rack_id", func() error { return move("", item(1, 0, 2, 1)) }},
						{"zero to_rack_id", func() error { return move("", item(1, 1, 0, 1)) }},
						{"zero count", func() error { return move("", item(1, 1, 2, 0)) }},
						{"same from and to rack", func() error { return move("", item(1, 1, 1, 1)) }},
						{"duplicate item", func() error { return move("", item(1, 1, 2, 1), item(1, 1, 2, 2)) }},
					}
					for _, c := range cases {
						err := c.run()
						assert.Error(t, err, c.name)
						assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), c.name)
					}
				})
			})
		},
	)
}
