package inventory_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory"
	"github.com/pdcgo/inventory_service/inventory_models"
	"github.com/pdcgo/invoice_service/invoice_models"
	common "github.com/pdcgo/schema/services/common/v1"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	warehouse_iface "github.com/pdcgo/schema/services/warehouse_iface/v1"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

const restockWarehouse uint64 = 12

// action constructors — keep the batch requests readable.
func actStatus(status inventory_iface.RestockStatus, note string) *inventory_iface.RestockUpdateAction {
	return &inventory_iface.RestockUpdateAction{Act: &inventory_iface.RestockUpdateAction_ChangeStatus{
		ChangeStatus: &inventory_iface.ChangeStatus{Status: status, Note: note},
	}}
}

func actItems(items ...*inventory_iface.RestockItem) *inventory_iface.RestockUpdateAction {
	return &inventory_iface.RestockUpdateAction{Act: &inventory_iface.RestockUpdateAction_UpdateItems{
		UpdateItems: &inventory_iface.UpdateItems{Items: items},
	}}
}

func actFee(cost float64) *inventory_iface.RestockUpdateAction {
	return &inventory_iface.RestockUpdateAction{Act: &inventory_iface.RestockUpdateAction_UpdateShippingFee{
		UpdateShippingFee: &inventory_iface.UpdateShippingFee{ShippingCost: cost},
	}}
}

func actInfo(info *inventory_iface.UpdateShippingInfo) *inventory_iface.RestockUpdateAction {
	return &inventory_iface.RestockUpdateAction{Act: &inventory_iface.RestockUpdateAction_UpdateShippingInfo{
		UpdateShippingInfo: info,
	}}
}

func actCancel() *inventory_iface.RestockUpdateAction {
	return &inventory_iface.RestockUpdateAction{Act: &inventory_iface.RestockUpdateAction_RestockCancel{
		RestockCancel: &inventory_iface.RestockCancel{},
	}}
}

func actAccept(accept *inventory_iface.RestockAccept) *inventory_iface.RestockUpdateAction {
	return &inventory_iface.RestockUpdateAction{Act: &inventory_iface.RestockUpdateAction_RestockAccept{
		RestockAccept: accept,
	}}
}

func TestRestock(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "inventory restock",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(
					&inventory_models.InventoryRestock{},
					&inventory_models.InventoryRestockItem{},
					&inventory_models.InventoryRestockLog{},
					&inventory_models.InventoryTransaction{},
					&inventory_models.InventoryTransactionItem{},
					&inventory_models.StockState{},
					&inventory_models.StockBatch{},
					&inventory_models.StockBatchLog{},
					&inventory_models.StockPlacement{},
					&inventory_models.StockPlacementLog{},
					&inventory_models.Rack{},
					// RestockDetail joins products for the item names.
					&productRow{},
					// warehouse_accept_fee posts a payable via invoice_v2 in the same tx.
					&invoice_models.TeamBalance{},
					&invoice_models.BalanceChangeLog{},
					&invoice_models.TeamBalanceDailyLog{},
					&invoice_models.BalanceChangeRestockSource{},
				))

				svc := inventory.NewInventoryService(db)
				ctx := context.Background()

				assert.NoError(t, db.Create(&productRow{ID: 1, TeamID: 3, Name: "Widget"}).Error)
				assert.NoError(t, db.Create(&productRow{ID: 2, TeamID: 3, Name: "Gadget"}).Error)
				assert.NoError(t, db.Create(&[]inventory_models.Rack{
					{ID: 101, WarehouseID: restockWarehouse, Name: "Rack A"},
					{ID: 102, WarehouseID: restockWarehouse, Name: "Rack B"},
					{ID: 201, WarehouseID: 99, Name: "Elsewhere"},
				}).Error)

				stockOf := func(productID uint64) int64 {
					var st inventory_models.StockState
					res := db.
						Where("product_id = ? AND warehouse_id = ?", productID, restockWarehouse).
						Limit(1).
						Find(&st)
					assert.NoError(t, res.Error)
					return st.StockReady
				}
				stockAmountOf := func(productID uint64) float64 {
					var st inventory_models.StockState
					res := db.
						Where("product_id = ? AND warehouse_id = ?", productID, restockWarehouse).
						Limit(1).
						Find(&st)
					assert.NoError(t, res.Error)
					return st.StockReadyAmount
				}
				batchCount := func(txID uint64) int64 {
					var n int64
					assert.NoError(t, db.Model(&inventory_models.StockBatch{}).Where("inbound_id = ?", txID).Count(&n).Error)
					return n
				}
				placementOf := func(productID, rackID uint64) int64 {
					var pl inventory_models.StockPlacement
					res := db.
						Where("product_id = ? AND warehouse_id = ? AND rack_id = ?", productID, restockWarehouse, rackID).
						Limit(1).
						Find(&pl)
					assert.NoError(t, res.Error)
					return pl.Count
				}
				createRestock := func(receipt string, items []*inventory_iface.RestockItem) uint64 {
					res, err := svc.RestockCreate(ctx, connect.NewRequest(&inventory_iface.RestockCreateRequest{
						TeamId:        3,
						WarehouseId:   restockWarehouse,
						Supplier:      "PT Alpha",
						Receipt:       receipt,
						Note:          "initial note",
						Items:         items,
						ExternOrderId: "ORD-" + receipt,
						ShippingId:    5,
						ShippingCost:  12000,
						PaymentType:   warehouse_iface.PaymentType_PAYMENT_TYPE_SHOPEEPAY,
					}))
					assert.NoError(t, err)
					return res.Msg.GetRestockId()
				}
				update := func(id uint64, actions ...*inventory_iface.RestockUpdateAction) error {
					_, err := svc.RestockUpdate(ctx, connect.NewRequest(&inventory_iface.RestockUpdateRequest{
						RestockId:   id,
						WarehouseId: restockWarehouse,
						Actions:     actions,
					}))
					return err
				}
				restockRow := func(id uint64) inventory_models.InventoryRestock {
					var r inventory_models.InventoryRestock
					assert.NoError(t, db.First(&r, id).Error)
					return r
				}
				logActions := func(id uint64) []inventory_iface.RestockLogAction {
					res, err := svc.RestockLogList(ctx, connect.NewRequest(&inventory_iface.RestockLogListRequest{
						RestockId: id,
						Page:      &common.PageFilter{Page: 1, Limit: 50},
					}))
					assert.NoError(t, err)
					out := make([]inventory_iface.RestockLogAction, 0, len(res.Msg.GetLogs()))
					for _, l := range res.Msg.GetLogs() {
						out = append(out, l.GetAction())
					}
					return out
				}

				var restock1 uint64

				t.Run("create is pending with no stock effect", func(t *testing.T) {
					restock1 = createRestock("RCP-1", []*inventory_iface.RestockItem{
						{ProductId: 1, Count: 5, Price: 10},
						{ProductId: 2, Count: 3, Price: 20},
					})
					r := restockRow(restock1)
					assert.Equal(t, inventory_models.RestockPending, r.Status)
					assert.Nil(t, r.InventoryTransactionID)
					assert.Equal(t, int64(0), stockOf(1))
				})

				t.Run("one batch applies the pre-accept edits in order", func(t *testing.T) {
					err := update(restock1,
						actItems(&inventory_iface.RestockItem{ProductId: 1, Count: 7, Price: 10}),
						actFee(9000),
						actInfo(&inventory_iface.UpdateShippingInfo{
							ShippingId:    6,
							Receipt:       "RCP-1B",
							ExternOrderId: "ORD-EDIT",
							PaymentType:   warehouse_iface.PaymentType_PAYMENT_TYPE_TRANSFER,
						}),
						actStatus(inventory_iface.RestockStatus_RESTOCK_STATUS_PROBLEM, "double check"),
					)
					assert.NoError(t, err)

					r := restockRow(restock1)
					assert.Equal(t, inventory_models.RestockProblem, r.Status)
					assert.Equal(t, float64(9000), r.ShippingCost)
					assert.Equal(t, uint64(6), r.ShippingID)
					assert.Equal(t, "RCP-1B", r.Receipt)
					assert.Equal(t, "ORD-EDIT", r.ExternOrderID)
					assert.Equal(t, "transfer", r.PaymentType)
					assert.Equal(t, "initial note\ndouble check", r.Note)

					var cnt int64
					assert.NoError(t, db.Model(&inventory_models.InventoryRestockItem{}).Where("restock_id = ?", restock1).Count(&cnt).Error)
					assert.Equal(t, int64(1), cnt) // items replaced

					// marker back to pending.
					assert.NoError(t, update(restock1, actStatus(inventory_iface.RestockStatus_RESTOCK_STATUS_PENDING, "")))
					assert.Equal(t, inventory_models.RestockPending, restockRow(restock1).Status)

					// change_status cannot set accepted.
					err = update(restock1, actStatus(inventory_iface.RestockStatus_RESTOCK_STATUS_ACCEPTED, ""))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})

				t.Run("accept with placements and problems", func(t *testing.T) {
					// ordered 7 → problem 2 → effective 5, placed 3 + 2 across two racks.
					err := update(restock1, actAccept(&inventory_iface.RestockAccept{
						Problems: &inventory_iface.RestockProblems{Items: []*inventory_iface.RestockProblemItem{
							{ProductId: 1, Count: 2, Note: "crushed"},
						}},
						Placements: &inventory_iface.RestockPlacements{Items: []*inventory_iface.RestockPlacementItem{
							{ProductId: 1, RackId: 101, Count: 3},
							{ProductId: 1, RackId: 102, Count: 2},
						}},
					}))
					assert.NoError(t, err)

					r := restockRow(restock1)
					assert.Equal(t, inventory_models.RestockAccepted, r.Status)
					assert.NotNil(t, r.InventoryTransactionID)
					assert.NotNil(t, r.AcceptedAt)

					txID := *r.InventoryTransactionID
					assert.Equal(t, int64(5), stockOf(1)) // problems excluded
					assert.Equal(t, int64(1), batchCount(txID))
					var b inventory_models.StockBatch
					assert.NoError(t, db.Where("inbound_id = ?", txID).First(&b).Error)
					assert.Equal(t, int64(5), b.StartCount)
					// landed cost (§5): 10 + (shipping 9000 + fee 0) / 5 accepted pieces.
					assert.Equal(t, float64(1810), b.Price)
					var trxItem inventory_models.InventoryTransactionItem
					assert.NoError(t, db.Where("transaction_id = ?", txID).First(&trxItem).Error)
					assert.Equal(t, float64(1810), trxItem.Price)
					assert.Equal(t, float64(9050), stockAmountOf(1)) // 5 × 1810

					assert.Equal(t, int64(3), placementOf(1, 101))
					assert.Equal(t, int64(2), placementOf(1, 102))

					var item inventory_models.InventoryRestockItem
					assert.NoError(t, db.Where("restock_id = ? AND product_id = 1", restock1).First(&item).Error)
					assert.Equal(t, int64(2), item.ProblemCount)
					assert.Equal(t, "crushed", item.ProblemNote)

					det, err := svc.RestockDetail(ctx, connect.NewRequest(&inventory_iface.RestockDetailRequest{RestockId: restock1}))
					assert.NoError(t, err)
					assert.Equal(t, txID, det.Msg.GetInventoryTransactionId())
					assert.Equal(t, int64(2), det.Msg.GetItems()[0].GetProblemCount())

					// re-accept is a no-op (no double stock / placements / pricing).
					assert.NoError(t, update(restock1, actAccept(&inventory_iface.RestockAccept{})))
					assert.Equal(t, int64(5), stockOf(1))
					assert.Equal(t, float64(9050), stockAmountOf(1))
					assert.Equal(t, int64(3), placementOf(1, 101))
				})

				t.Run("pre-accept edits are rejected once accepted", func(t *testing.T) {
					err := update(restock1, actFee(1))
					assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
					err = update(restock1, actItems(&inventory_iface.RestockItem{ProductId: 1, Count: 1, Price: 1}))
					assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
				})

				t.Run("cancel after accept is rejected", func(t *testing.T) {
					r := restockRow(restock1)
					txID := *r.InventoryTransactionID

					err := update(restock1, actCancel())
					assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

					// nothing reversed — document, stock, batch and placements intact.
					r = restockRow(restock1)
					assert.Equal(t, inventory_models.RestockAccepted, r.Status)
					assert.Nil(t, r.CanceledAt)
					assert.Equal(t, int64(5), stockOf(1))
					assert.Equal(t, int64(1), batchCount(txID))
					assert.Equal(t, int64(3), placementOf(1, 101))
					assert.Equal(t, int64(2), placementOf(1, 102))

					var trx inventory_models.InventoryTransaction
					assert.NoError(t, db.First(&trx, txID).Error)
					assert.Equal(t, inventory_models.InvTxActive, trx.Status)
				})

				t.Run("log list records the batch chronologically", func(t *testing.T) {
					assert.Equal(t, []inventory_iface.RestockLogAction{
						inventory_iface.RestockLogAction_RESTOCK_LOG_ACTION_CREATED,
						inventory_iface.RestockLogAction_RESTOCK_LOG_ACTION_EDITED,  // items
						inventory_iface.RestockLogAction_RESTOCK_LOG_ACTION_EDITED,  // fee
						inventory_iface.RestockLogAction_RESTOCK_LOG_ACTION_EDITED,  // info
						inventory_iface.RestockLogAction_RESTOCK_LOG_ACTION_PROBLEM, // marker
						inventory_iface.RestockLogAction_RESTOCK_LOG_ACTION_EDITED,  // back to pending
						inventory_iface.RestockLogAction_RESTOCK_LOG_ACTION_ACCEPTED,
					}, logActions(restock1))
				})

				t.Run("accept validations", func(t *testing.T) {
					id := createRestock("RCP-2", []*inventory_iface.RestockItem{{ProductId: 1, Count: 4, Price: 10}})

					// negative warehouse fee is rejected.
					err := update(id, actAccept(&inventory_iface.RestockAccept{WarehouseAcceptFee: -1}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

					// placements must balance placed + problem == ordered.
					err = update(id, actAccept(&inventory_iface.RestockAccept{
						Placements: &inventory_iface.RestockPlacements{Items: []*inventory_iface.RestockPlacementItem{
							{ProductId: 1, RackId: 101, Count: 3},
						}},
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

					// rack must be a live rack of the restock's warehouse.
					err = update(id, actAccept(&inventory_iface.RestockAccept{
						Placements: &inventory_iface.RestockPlacements{Items: []*inventory_iface.RestockPlacementItem{
							{ProductId: 1, RackId: 201, Count: 4},
						}},
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

					// problem product must be a restock line, and <= ordered.
					err = update(id, actAccept(&inventory_iface.RestockAccept{
						Problems: &inventory_iface.RestockProblems{Items: []*inventory_iface.RestockProblemItem{
							{ProductId: 99, Count: 1},
						}},
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
					err = update(id, actAccept(&inventory_iface.RestockAccept{
						Problems: &inventory_iface.RestockProblems{Items: []*inventory_iface.RestockProblemItem{
							{ProductId: 1, Count: 5},
						}},
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

					// everything problematic → nothing to accept.
					err = update(id, actAccept(&inventory_iface.RestockAccept{
						Problems: &inventory_iface.RestockProblems{Items: []*inventory_iface.RestockProblemItem{
							{ProductId: 1, Count: 4},
						}},
					}))
					assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
				})

				t.Run("accept requires placements for every effective unit", func(t *testing.T) {
					id := createRestock("RCP-3", []*inventory_iface.RestockItem{{ProductId: 2, Count: 3, Price: 20}})

					// no placements at all → rejected.
					err := update(id, actAccept(&inventory_iface.RestockAccept{}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

					// short placements (placed 2 != effective 3) → rejected, still pending.
					err = update(id, actAccept(&inventory_iface.RestockAccept{
						Placements: &inventory_iface.RestockPlacements{Items: []*inventory_iface.RestockPlacementItem{
							{ProductId: 2, RackId: 101, Count: 2},
						}},
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
					assert.Equal(t, inventory_models.RestockPending, restockRow(id).Status)
					assert.Equal(t, int64(0), stockOf(2))

					// full placements → accepted, stock enters, placement rows written.
					assert.NoError(t, update(id, actAccept(&inventory_iface.RestockAccept{
						Placements: &inventory_iface.RestockPlacements{Items: []*inventory_iface.RestockPlacementItem{
							{ProductId: 2, RackId: 101, Count: 3},
						}},
					})))
					r := restockRow(id)
					assert.Equal(t, inventory_models.RestockAccepted, r.Status)
					assert.Equal(t, int64(3), stockOf(2))
					assert.Equal(t, int64(3), placementOf(2, 101))
				})

				t.Run("accept posts the warehouse accept fee as a payable", func(t *testing.T) {
					id := createRestock("RCP-6", []*inventory_iface.RestockItem{{ProductId: 1, Count: 2, Price: 10}})
					const fee = 7500.0
					assert.NoError(t, update(id, actAccept(&inventory_iface.RestockAccept{
						Placements: &inventory_iface.RestockPlacements{Items: []*inventory_iface.RestockPlacementItem{
							{ProductId: 1, RackId: 101, Count: 2},
						}},
						WarehouseAcceptFee: fee,
					})))

					r := restockRow(id)
					assert.Equal(t, inventory_models.RestockAccepted, r.Status)
					assert.Equal(t, float64(fee), r.WarehouseAcceptFee)

					// the fee participates in the landed batch price (§5):
					// 10 + (shipping 12000 + fee 7500) / 2 accepted pieces.
					var b inventory_models.StockBatch
					assert.NoError(t, db.Where("inbound_id = ?", *r.InventoryTransactionID).First(&b).Error)
					assert.Equal(t, float64(9760), b.Price)

					balanceOf := func(teamID, forTeamID uint64, bt invoice_iface.BalanceType) float64 {
						var b invoice_models.TeamBalance
						res := db.
							Where("team_id = ? AND for_team_id = ? AND balance_type = ?", teamID, forTeamID, bt).
							Limit(1).
							Find(&b)
						assert.NoError(t, res.Error)
						return b.Balance
					}
					// warehouse (creditor) holds a RECEIVABLE +fee; selling team the mirrored PAYABLE -fee.
					assert.Equal(t, float64(fee), balanceOf(restockWarehouse, 3, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE))
					assert.Equal(t, float64(-fee), balanceOf(3, restockWarehouse, invoice_iface.BalanceType_BALANCE_TYPE_PAYABLE))

					det, err := svc.RestockDetail(ctx, connect.NewRequest(&inventory_iface.RestockDetailRequest{RestockId: id}))
					assert.NoError(t, err)
					assert.Equal(t, float64(fee), det.Msg.GetWarehouseAcceptFee())
				})

				t.Run("cancel pending is a status flip only", func(t *testing.T) {
					id := createRestock("RCP-4", []*inventory_iface.RestockItem{{ProductId: 1, Count: 2, Price: 10}})
					assert.NoError(t, update(id, actCancel()))
					r := restockRow(id)
					assert.Equal(t, inventory_models.RestockCanceled, r.Status)
					assert.NotNil(t, r.CanceledAt)
					assert.Nil(t, r.InventoryTransactionID)

					// second cancel is a no-op; accept after cancel is rejected.
					assert.NoError(t, update(id, actCancel()))
					err := update(id, actAccept(&inventory_iface.RestockAccept{}))
					assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
				})

				t.Run("detail returns header, items and product names", func(t *testing.T) {
					id := createRestock("RCP-5", []*inventory_iface.RestockItem{
						{ProductId: 1, Count: 5, Price: 10},
						{ProductId: 2, Count: 3, Price: 20},
					})
					res, err := svc.RestockDetail(ctx, connect.NewRequest(&inventory_iface.RestockDetailRequest{
						RestockId: id, WarehouseId: restockWarehouse,
					}))
					assert.NoError(t, err)
					assert.Equal(t, "RCP-5", res.Msg.GetReceipt())
					assert.Equal(t, "ORD-RCP-5", res.Msg.GetExternOrderId())
					assert.Equal(t, uint64(5), res.Msg.GetShippingId())
					assert.Equal(t, float64(12000), res.Msg.GetShippingCost())
					assert.Equal(t, warehouse_iface.PaymentType_PAYMENT_TYPE_SHOPEEPAY, res.Msg.GetPaymentType())
					assert.Equal(t, inventory_iface.RestockStatus_RESTOCK_STATUS_PENDING, res.Msg.GetStatus())
					assert.Zero(t, res.Msg.GetInventoryTransactionId())
					assert.Equal(t, int64(8), res.Msg.GetItemCount())
					assert.Equal(t, float64(110), res.Msg.GetAmount())
					assert.Len(t, res.Msg.GetItems(), 2)
					assert.Equal(t, "Widget", res.Msg.GetItems()[0].GetProductName())
				})

				t.Run("list filters by status and search", func(t *testing.T) {
					res, err := svc.RestockList(ctx, connect.NewRequest(&inventory_iface.RestockListRequest{
						Filter: &inventory_iface.RestockListFilter{
							WarehouseId: restockWarehouse,
							Status:      inventory_iface.RestockStatus_RESTOCK_STATUS_PENDING,
							Page:        &common.PageFilter{Page: 1, Limit: 20},
						},
						DataTypes: []inventory_iface.RestockListDataType{
							inventory_iface.RestockListDataType_RESTOCK_LIST_DATA_TYPE_GENERAL,
						},
					}))
					assert.NoError(t, err)
					assert.NotEmpty(t, res.Msg.GetIds()) // RCP-2 (failed accepts keep it pending) + RCP-5
				})

				t.Run("unknown restock is NotFound", func(t *testing.T) {
					err := update(999999, actCancel())
					assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

					_, err = svc.RestockLogList(ctx, connect.NewRequest(&inventory_iface.RestockLogListRequest{
						RestockId: 999999,
						Page:      &common.PageFilter{Page: 1, Limit: 20},
					}))
					assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
				})
			})
		},
	)
}
