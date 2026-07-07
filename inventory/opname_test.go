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

const opnameWarehouse uint64 = 9

func TestOpname(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "inventory opname",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(
					&inventory_models.InventoryOpname{},
					&inventory_models.InventoryOpnameLine{},
					&inventory_models.InventoryOpnameLog{},
					&inventory_models.InventoryTransaction{},
					&inventory_models.InventoryTransactionItem{},
					&inventory_models.StockState{},
					&inventory_models.StockBatch{},
					&inventory_models.StockBatchLog{},
					&inventory_models.StockPlacement{},
					&inventory_models.StockPlacementLog{},
					&inventory_models.Rack{},
					// OpnameDetail joins products + racks for the line names.
					&productRow{},
				))

				svc := inventory.NewInventoryService(db)
				ctx := context.Background()

				assert.NoError(t, db.Create(&productRow{ID: 1, TeamID: 3, Name: "Widget"}).Error)
				assert.NoError(t, db.Create(&productRow{ID: 2, TeamID: 3, Name: "Gadget"}).Error)
				assert.NoError(t, db.Create(&productRow{ID: 3, TeamID: 3, Name: "Doohickey"}).Error)
				assert.NoError(t, db.Create(&[]inventory_models.Rack{
					{ID: 101, WarehouseID: opnameWarehouse, Name: "Rack A"},
					{ID: 102, WarehouseID: opnameWarehouse, Name: "Rack B"},
				}).Error)

				// Placements: p1 on two racks, p2 on one; a zero-count row must NOT seed a line.
				assert.NoError(t, db.Create(&[]inventory_models.StockPlacement{
					{ProductID: 1, WarehouseID: opnameWarehouse, RackID: 101, Count: 10},
					{ProductID: 1, WarehouseID: opnameWarehouse, RackID: 102, Count: 3},
					{ProductID: 2, WarehouseID: opnameWarehouse, RackID: 101, Count: 5},
					{ProductID: 2, WarehouseID: opnameWarehouse, RackID: 102, Count: 0},
				}).Error)
				// Aggregates (avg price: p1 = 10, p2 = 5).
				assert.NoError(t, db.Create(&[]inventory_models.StockState{
					{ProductID: 1, WarehouseID: opnameWarehouse, StockReady: 13, StockReadyAmount: 130},
					{ProductID: 2, WarehouseID: opnameWarehouse, StockReady: 5, StockReadyAmount: 25},
				}).Error)

				placementCount := func(productID, rackID uint64) int64 {
					var pl inventory_models.StockPlacement
					res := db.
						Where("product_id = ? AND warehouse_id = ? AND rack_id = ?", productID, opnameWarehouse, rackID).
						Limit(1).
						Find(&pl)
					assert.NoError(t, res.Error)
					return pl.Count
				}
				stateOf := func(productID uint64) (int64, float64) {
					var st inventory_models.StockState
					res := db.
						Where("product_id = ? AND warehouse_id = ?", productID, opnameWarehouse).
						Limit(1).
						Find(&st)
					assert.NoError(t, res.Error)
					return st.StockReady, st.StockReadyAmount
				}
				count := func(opnameID, rackID uint64, items ...*inventory_iface.OpnameCountItem) error {
					_, err := svc.OpnameLineCount(ctx, connect.NewRequest(&inventory_iface.OpnameLineCountRequest{
						OpnameId:    opnameID,
						WarehouseId: opnameWarehouse,
						RackId:      rackID,
						Items:       items,
					}))
					return err
				}
				item := func(productID uint64, counted int64) *inventory_iface.OpnameCountItem {
					return &inventory_iface.OpnameCountItem{ProductId: productID, CountedCount: counted}
				}

				// --- create ---
				created, err := svc.OpnameCreate(ctx, connect.NewRequest(&inventory_iface.OpnameCreateRequest{
					TeamId:      3,
					WarehouseId: opnameWarehouse,
					Name:        "July stock take",
				}))
				assert.NoError(t, err)
				opnameID := created.Msg.OpnameId
				assert.Greater(t, opnameID, uint64(0))

				t.Run("create seeds lines from placements with count > 0", func(t *testing.T) {
					det, err := svc.OpnameDetail(ctx, connect.NewRequest(&inventory_iface.OpnameDetailRequest{
						OpnameId: opnameID, WarehouseId: opnameWarehouse,
					}))
					assert.NoError(t, err)
					assert.Equal(t, inventory_iface.OpnameStatus_OPNAME_STATUS_PENDING, det.Msg.Status)
					assert.Len(t, det.Msg.Lines, 3) // the zero-count (p2, rack 102) is excluded
					first := det.Msg.Lines[0]       // ordered rack ASC, product ASC → (101, p1)
					assert.Equal(t, "Rack A", first.RackName)
					assert.Equal(t, "Widget", first.ProductName)
					assert.Equal(t, int64(10), first.ExpectedCount)
					assert.False(t, first.Counted)
				})

				t.Run("line count upserts, re-count overwrites, unknown product gets expected 0", func(t *testing.T) {
					assert.NoError(t, count(opnameID, 101, item(1, 8), item(2, 5)))
					// re-count overwrites, now with a reason + note
					assert.NoError(t, count(opnameID, 101, &inventory_iface.OpnameCountItem{
						ProductId:    1,
						CountedCount: 9,
						Reason:       inventory_iface.OpnameReasonType_OPNAME_REASON_TYPE_LOST,
						Note:         "one missing",
					}))
					// found-but-not-expected, with a reason only
					assert.NoError(t, count(opnameID, 101, &inventory_iface.OpnameCountItem{
						ProductId:    3,
						CountedCount: 2,
						Reason:       inventory_iface.OpnameReasonType_OPNAME_REASON_TYPE_BROKEN,
					}))

					det, err := svc.OpnameDetail(ctx, connect.NewRequest(&inventory_iface.OpnameDetailRequest{
						OpnameId: opnameID, WarehouseId: opnameWarehouse,
					}))
					assert.NoError(t, err)
					assert.Len(t, det.Msg.Lines, 4)
					byKey := map[[2]uint64]*inventory_iface.OpnameLineItem{}
					for _, l := range det.Msg.Lines {
						byKey[[2]uint64{l.RackId, l.ProductId}] = l
					}
					assert.Equal(t, int64(9), byKey[[2]uint64{101, 1}].CountedCount)
					assert.True(t, byKey[[2]uint64{101, 1}].Counted)
					assert.Equal(t, inventory_iface.OpnameReasonType_OPNAME_REASON_TYPE_LOST, byKey[[2]uint64{101, 1}].Reason)
					assert.Equal(t, "one missing", byKey[[2]uint64{101, 1}].Note)
					assert.Equal(t, int64(5), byKey[[2]uint64{101, 2}].CountedCount)
					assert.Equal(t, inventory_iface.OpnameReasonType_OPNAME_REASON_TYPE_UNSPECIFIED, byKey[[2]uint64{101, 2}].Reason)
					assert.Equal(t, int64(0), byKey[[2]uint64{101, 3}].ExpectedCount)
					assert.Equal(t, int64(2), byKey[[2]uint64{101, 3}].CountedCount)
					assert.Equal(t, inventory_iface.OpnameReasonType_OPNAME_REASON_TYPE_BROKEN, byKey[[2]uint64{101, 3}].Reason)
					assert.False(t, byKey[[2]uint64{102, 1}].Counted) // untouched
				})

				t.Run("list returns general + progress", func(t *testing.T) {
					res, err := svc.OpnameList(ctx, connect.NewRequest(&inventory_iface.OpnameListRequest{
						Filter: &inventory_iface.OpnameListFilter{
							WarehouseId: opnameWarehouse,
							Page:        &common.PageFilter{Page: 1, Limit: 10},
						},
						DataTypes: []inventory_iface.OpnameListDataType{
							inventory_iface.OpnameListDataType_OPNAME_LIST_DATA_TYPE_GENERAL,
							inventory_iface.OpnameListDataType_OPNAME_LIST_DATA_TYPE_PROGRESS,
						},
					}))
					assert.NoError(t, err)
					assert.Contains(t, res.Msg.Ids, opnameID)
					var general *inventory_iface.OpnameGeneralItem
					var progress *inventory_iface.OpnameProgressItem
					for _, d := range res.Msg.Data {
						switch v := d.Data.(type) {
						case *inventory_iface.OpnameData_General:
							general = v.General.Data[opnameID]
						case *inventory_iface.OpnameData_Progress:
							progress = v.Progress.Data[opnameID]
						}
					}
					assert.NotNil(t, general)
					assert.Equal(t, "July stock take", general.Name)
					assert.NotNil(t, progress)
					assert.Equal(t, int64(4), progress.LineCount)
					assert.Equal(t, int64(3), progress.CountedCount)
					// p1: 9 vs 10 and p3: 2 vs 0 differ; p2 matches.
					assert.Equal(t, int64(2), progress.DiscrepancyCount)
				})

				t.Run("complete applies counted values as anchored ADJUSTMENTs; uncounted untouched", func(t *testing.T) {
					res, err := svc.OpnameComplete(ctx, connect.NewRequest(&inventory_iface.OpnameCompleteRequest{
						OpnameId: opnameID, WarehouseId: opnameWarehouse,
					}))
					assert.NoError(t, err)
					txID := res.Msg.TransactionId
					assert.Greater(t, txID, uint64(0))

					// Placements: counted values applied; uncounted (p1, rack 102) untouched.
					assert.Equal(t, int64(9), placementCount(1, 101))
					assert.Equal(t, int64(5), placementCount(2, 101))
					assert.Equal(t, int64(2), placementCount(3, 101))
					assert.Equal(t, int64(3), placementCount(1, 102))

					// Aggregates: p1 13→12 valued at avg 10; p2 unchanged; p3 0→2 (no prior state).
					c1, a1 := stateOf(1)
					assert.Equal(t, int64(12), c1)
					assert.InDelta(t, 120, a1, 0.001)
					c2, _ := stateOf(2)
					assert.Equal(t, int64(5), c2)
					c3, _ := stateOf(3)
					assert.Equal(t, int64(2), c3)

					// Placement logs: ADJUSTMENT rows anchored to the minted transaction,
					// carrying the line's reason/note.
					var logs []inventory_models.StockPlacementLog
					assert.NoError(t, db.
						Where("transaction_id = ?", txID).
						Order("product_id ASC").
						Find(&logs).
						Error)
					assert.Len(t, logs, 2) // p1 -1, p3 +2 (p2 delta 0 skipped)
					for _, l := range logs {
						assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ADJUSTMENT, l.ChangeType)
					}
					assert.Equal(t, int64(-1), logs[0].Change)
					assert.Equal(t, "opname: lost — one missing", logs[0].Note)
					assert.Equal(t, int64(2), logs[1].Change)
					assert.Equal(t, "opname: broken", logs[1].Note)

					// Idempotent re-complete returns the same transaction, no new logs.
					res2, err := svc.OpnameComplete(ctx, connect.NewRequest(&inventory_iface.OpnameCompleteRequest{
						OpnameId: opnameID, WarehouseId: opnameWarehouse,
					}))
					assert.NoError(t, err)
					assert.Equal(t, txID, res2.Msg.TransactionId)
					var n int64
					assert.NoError(t, db.Model(&inventory_models.StockPlacementLog{}).
						Where("transaction_id = ?", txID).Count(&n).Error)
					assert.Equal(t, int64(2), n)
				})

				t.Run("completed session rejects count and cancel", func(t *testing.T) {
					err := count(opnameID, 101, item(1, 99))
					assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
					_, err = svc.OpnameCancel(ctx, connect.NewRequest(&inventory_iface.OpnameCancelRequest{
						OpnameId: opnameID, WarehouseId: opnameWarehouse,
					}))
					assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
				})

				t.Run("cancel discards counts with no stock effect", func(t *testing.T) {
					created2, err := svc.OpnameCreate(ctx, connect.NewRequest(&inventory_iface.OpnameCreateRequest{
						TeamId: 3, WarehouseId: opnameWarehouse, Name: "Canceled take",
					}))
					assert.NoError(t, err)
					id2 := created2.Msg.OpnameId

					assert.NoError(t, count(id2, 101, item(1, 1)))
					_, err = svc.OpnameCancel(ctx, connect.NewRequest(&inventory_iface.OpnameCancelRequest{
						OpnameId: id2, WarehouseId: opnameWarehouse,
					}))
					assert.NoError(t, err)
					// idempotent
					_, err = svc.OpnameCancel(ctx, connect.NewRequest(&inventory_iface.OpnameCancelRequest{
						OpnameId: id2, WarehouseId: opnameWarehouse,
					}))
					assert.NoError(t, err)
					// stock untouched by the canceled count
					assert.Equal(t, int64(9), placementCount(1, 101))
					// completing a canceled session is rejected
					_, err = svc.OpnameComplete(ctx, connect.NewRequest(&inventory_iface.OpnameCompleteRequest{
						OpnameId: id2, WarehouseId: opnameWarehouse,
					}))
					assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
				})

				t.Run("unknown opname is not found", func(t *testing.T) {
					_, err := svc.OpnameDetail(ctx, connect.NewRequest(&inventory_iface.OpnameDetailRequest{
						OpnameId: 999999, WarehouseId: opnameWarehouse,
					}))
					assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
				})
			})
		},
	)
}
