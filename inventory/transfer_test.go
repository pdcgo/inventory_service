package inventory_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory"
	"github.com/pdcgo/inventory_service/inventory_models"
	common "github.com/pdcgo/schema/services/common/v1"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

const (
	srcWarehouse uint64 = 21
	dstWarehouse uint64 = 22
)

// transferListMaps splits a TransferListResponse's oneof data into per-type maps.
func transferListMaps(data []*inventory_iface.TransferData) (
	map[uint64]*inventory_iface.TransferGeneralItem,
	map[uint64]*inventory_iface.TransferTotalItem,
) {
	general := map[uint64]*inventory_iface.TransferGeneralItem{}
	total := map[uint64]*inventory_iface.TransferTotalItem{}
	for _, d := range data {
		if g := d.GetGeneral(); g != nil {
			general = g.GetData()
		}
		if tt := d.GetTotal(); tt != nil {
			total = tt.GetData()
		}
	}
	return general, total
}

func TestTransfer(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "inventory transfer",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(
					&inventory_models.InventoryTransfer{},
					&inventory_models.InventoryTransferItem{},
					&inventory_models.InventoryTransaction{},
					&inventory_models.InventoryTransactionItem{},
					&inventory_models.StockState{},
					&inventory_models.StockBatch{},
					&inventory_models.StockBatchLog{},
					// Accept requires rack placements into the destination warehouse.
					&inventory_models.Rack{},
					&inventory_models.StockPlacement{},
					&inventory_models.StockPlacementLog{},
					// TransferCancel reuses reconstructCancel, whose placement step reads
					// invertory_histories (left join skus). Owned transactions have no such
					// rows, so it no-ops — but the tables must exist.
					&db_models.InvertoryHistory{},
					&skuRow{},
					// detail/list join products + warehouses for names.
					&productRow{},
					&warehouseRow{},
				))

				svc := inventory.NewInventoryService(db)
				ctx := context.Background()

				assert.NoError(t, db.Create(&productRow{ID: 1, TeamID: 3, Name: "Widget"}).Error)
				assert.NoError(t, db.Create(&productRow{ID: 2, TeamID: 3, Name: "Gadget"}).Error)
				assert.NoError(t, db.Create(&warehouseRow{ID: uint(srcWarehouse), Name: "Gudang A"}).Error)
				assert.NoError(t, db.Create(&warehouseRow{ID: uint(dstWarehouse), Name: "Gudang B"}).Error)
				// Destination racks the accept flow places received goods into.
				assert.NoError(t, db.Create(&inventory_models.Rack{ID: 31, WarehouseID: dstWarehouse, Name: "B-1"}).Error)
				assert.NoError(t, db.Create(&inventory_models.Rack{ID: 32, WarehouseID: dstWarehouse, Name: "B-2"}).Error)

				stockOf := func(productID, warehouseID uint64) int64 {
					var st inventory_models.StockState
					res := db.
						Where("product_id = ? AND warehouse_id = ?", productID, warehouseID).
						Limit(1).
						Find(&st)
					assert.NoError(t, res.Error)
					return st.StockReady
				}
				batchCount := func(txID uint64) int64 {
					var n int64
					assert.NoError(t, db.Model(&inventory_models.StockBatch{}).Where("inbound_id = ?", txID).Count(&n).Error)
					return n
				}
				transferRow := func(id uint64) inventory_models.InventoryTransfer {
					var r inventory_models.InventoryTransfer
					assert.NoError(t, db.First(&r, id).Error)
					return r
				}

				// Seed the source stock through the real flow so the derived prices are
				// deterministic: Widget avg 4, Gadget avg 10.
				_, err := svc.TransactionCreate(ctx, connect.NewRequest(&inventory_iface.TransactionCreateRequest{
					TeamId:      3,
					WarehouseId: srcWarehouse,
					Tx: &inventory_iface.TransactionCreateRequest_Restock{Restock: &inventory_iface.TransactionRestock{
						Items: []*inventory_iface.TransactionItem{
							{ProductId: 1, Count: 10, Price: 4},
							{ProductId: 2, Count: 6, Price: 10},
						},
					}},
				}))
				assert.NoError(t, err)
				assert.Equal(t, int64(10), stockOf(1, srcWarehouse))
				assert.Equal(t, int64(6), stockOf(2, srcWarehouse))

				var transfer1 uint64

				t.Run("create moves stock out of the source", func(t *testing.T) {
					res, err := svc.TransferCreate(ctx, connect.NewRequest(&inventory_iface.TransferCreateRequest{
						TeamId:          3,
						FromWarehouseId: srcWarehouse,
						ToWarehouseId:   dstWarehouse,
						Note:            "restock cabang",
						Items: []*inventory_iface.TransferItem{
							{ProductId: 1, Count: 4},
							{ProductId: 2, Count: 3},
						},
					}))
					assert.NoError(t, err)
					transfer1 = res.Msg.GetTransferId()

					r := transferRow(transfer1)
					assert.Equal(t, inventory_models.TransferPending, r.Status)
					assert.NotZero(t, r.OutTransactionID)
					assert.Zero(t, r.InTransactionID)

					assert.Equal(t, int64(6), stockOf(1, srcWarehouse)) // 10 - 4
					assert.Equal(t, int64(3), stockOf(2, srcWarehouse)) // 6 - 3
					assert.Equal(t, int64(0), stockOf(1, dstWarehouse)) // not arrived yet
					assert.Equal(t, int64(0), batchCount(r.OutTransactionID))

					var outTx inventory_models.InventoryTransaction
					assert.NoError(t, db.First(&outTx, r.OutTransactionID).Error)
					assert.Equal(t, inventory_models.InvTxTransferOut, outTx.Type)

					// derived prices stored on the items (source averages).
					var items []inventory_models.InventoryTransferItem
					assert.NoError(t, db.Where("transfer_id = ?", transfer1).Order("id ASC").Find(&items).Error)
					assert.Len(t, items, 2)
					assert.Equal(t, float64(4), items[0].Price)
					assert.Equal(t, float64(10), items[1].Price)
				})

				placementCount := func(productID, rackID uint64) int64 {
					var pl inventory_models.StockPlacement
					res := db.
						Where("product_id = ? AND warehouse_id = ? AND rack_id = ?", productID, dstWarehouse, rackID).
						Limit(1).
						Find(&pl)
					assert.NoError(t, res.Error)
					return pl.Count
				}

				t.Run("accept requires every received unit placed", func(t *testing.T) {
					// short placements (P1 only 2 of 4, P2 missing) → rejected, no mutation.
					_, err := svc.TransferAccept(ctx, connect.NewRequest(&inventory_iface.TransferAcceptRequest{
						TransferId:  transfer1,
						WarehouseId: dstWarehouse,
						Placements: &inventory_iface.TransferPlacements{Items: []*inventory_iface.TransferPlacementItem{
							{ProductId: 1, RackId: 31, Count: 2},
						}},
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
					assert.Equal(t, inventory_models.TransferPending, transferRow(transfer1).Status)
					assert.Equal(t, int64(0), stockOf(1, dstWarehouse)) // untouched

					// rack from another warehouse → rejected.
					_, err = svc.TransferAccept(ctx, connect.NewRequest(&inventory_iface.TransferAcceptRequest{
						TransferId:  transfer1,
						WarehouseId: dstWarehouse,
						Placements: &inventory_iface.TransferPlacements{Items: []*inventory_iface.TransferPlacementItem{
							{ProductId: 1, RackId: 31, Count: 4},
							{ProductId: 2, RackId: 99, Count: 3},
						}},
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})

				t.Run("accept moves stock into the destination", func(t *testing.T) {
					_, err := svc.TransferAccept(ctx, connect.NewRequest(&inventory_iface.TransferAcceptRequest{
						TransferId:  transfer1,
						WarehouseId: dstWarehouse,
						Placements: &inventory_iface.TransferPlacements{Items: []*inventory_iface.TransferPlacementItem{
							{ProductId: 1, RackId: 31, Count: 4},
							{ProductId: 2, RackId: 31, Count: 3},
						}},
					}))
					assert.NoError(t, err)

					r := transferRow(transfer1)
					assert.Equal(t, inventory_models.TransferAccepted, r.Status)
					assert.NotZero(t, r.InTransactionID)
					assert.NotNil(t, r.AcceptedAt)

					assert.Equal(t, int64(4), stockOf(1, dstWarehouse))
					assert.Equal(t, int64(3), stockOf(2, dstWarehouse))
					assert.Equal(t, int64(6), stockOf(1, srcWarehouse))      // source unchanged by accept
					assert.Equal(t, int64(2), batchCount(r.InTransactionID)) // one batch per product

					// received goods placed on the chosen destination rack.
					assert.Equal(t, int64(4), placementCount(1, 31))
					assert.Equal(t, int64(3), placementCount(2, 31))
					var plLogs int64
					assert.NoError(t, db.
						Model(&inventory_models.StockPlacementLog{}).
						Where("transaction_id = ? AND change_type = ?", r.InTransactionID, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER).
						Count(&plLogs).Error)
					assert.Equal(t, int64(2), plLogs)

					var b inventory_models.StockBatch
					assert.NoError(t, db.Where("inbound_id = ? AND product_id = 1", r.InTransactionID).First(&b).Error)
					assert.Equal(t, float64(4), b.Price) // derived source price

					var inTx inventory_models.InventoryTransaction
					assert.NoError(t, db.First(&inTx, r.InTransactionID).Error)
					assert.Equal(t, inventory_models.InvTxTransferIn, inTx.Type)

					// re-accept is a no-op (no double stock), even without placements.
					_, err = svc.TransferAccept(ctx, connect.NewRequest(&inventory_iface.TransferAcceptRequest{TransferId: transfer1}))
					assert.NoError(t, err)
					assert.Equal(t, int64(4), stockOf(1, dstWarehouse))
					assert.Equal(t, int64(4), placementCount(1, 31))
				})

				t.Run("cancel of an accepted transfer is rejected", func(t *testing.T) {
					_, err := svc.TransferCancel(ctx, connect.NewRequest(&inventory_iface.TransferCancelRequest{TransferId: transfer1}))
					assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
				})

				t.Run("cancel pending restores the source", func(t *testing.T) {
					res, err := svc.TransferCreate(ctx, connect.NewRequest(&inventory_iface.TransferCreateRequest{
						TeamId:          3,
						FromWarehouseId: srcWarehouse,
						ToWarehouseId:   dstWarehouse,
						Items:           []*inventory_iface.TransferItem{{ProductId: 1, Count: 2}},
					}))
					assert.NoError(t, err)
					id := res.Msg.GetTransferId()
					assert.Equal(t, int64(4), stockOf(1, srcWarehouse)) // 6 - 2

					_, err = svc.TransferCancel(ctx, connect.NewRequest(&inventory_iface.TransferCancelRequest{
						TransferId: id, WarehouseId: srcWarehouse,
					}))
					assert.NoError(t, err)

					r := transferRow(id)
					assert.Equal(t, inventory_models.TransferCanceled, r.Status)
					assert.NotNil(t, r.CanceledAt)
					assert.Equal(t, int64(6), stockOf(1, srcWarehouse)) // restored

					var outTx inventory_models.InventoryTransaction
					assert.NoError(t, db.First(&outTx, r.OutTransactionID).Error)
					assert.Equal(t, inventory_models.InvTxCanceled, outTx.Status)

					// second cancel is a no-op.
					_, err = svc.TransferCancel(ctx, connect.NewRequest(&inventory_iface.TransferCancelRequest{TransferId: id}))
					assert.NoError(t, err)
					assert.Equal(t, int64(6), stockOf(1, srcWarehouse))

					// accept after cancel is rejected.
					_, err = svc.TransferAccept(ctx, connect.NewRequest(&inventory_iface.TransferAcceptRequest{TransferId: id}))
					assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
				})

				t.Run("detail returns names, items and totals", func(t *testing.T) {
					res, err := svc.TransferDetail(ctx, connect.NewRequest(&inventory_iface.TransferDetailRequest{
						TransferId: transfer1, WarehouseId: dstWarehouse, // matches the to side
					}))
					assert.NoError(t, err)
					assert.Equal(t, "Gudang A", res.Msg.GetFromWarehouseName())
					assert.Equal(t, "Gudang B", res.Msg.GetToWarehouseName())
					assert.Equal(t, inventory_iface.TransferStatus_TRANSFER_STATUS_ACCEPTED, res.Msg.GetStatus())
					assert.Equal(t, int64(7), res.Msg.GetItemCount()) // 4 + 3
					assert.Equal(t, float64(46), res.Msg.GetAmount()) // 4*4 + 3*10
					assert.Len(t, res.Msg.GetItems(), 2)
					assert.Equal(t, "Widget", res.Msg.GetItems()[0].GetProductName())
					assert.Equal(t, float64(16), res.Msg.GetItems()[0].GetTotal())

					t.Run("unknown id is NotFound", func(t *testing.T) {
						_, err := svc.TransferDetail(ctx, connect.NewRequest(&inventory_iface.TransferDetailRequest{TransferId: 999999}))
						assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
					})
				})

				t.Run("list filters, sorts and fills the requested maps", func(t *testing.T) {
					page := &common.PageFilter{Page: 1, Limit: 20}
					list := func(filter *inventory_iface.TransferListFilter, sort *inventory_iface.TransferListSort) *inventory_iface.TransferListResponse {
						filter.Page = page
						res, err := svc.TransferList(ctx, connect.NewRequest(&inventory_iface.TransferListRequest{
							Filter: filter,
							Sort:   sort,
							DataTypes: []inventory_iface.TransferListDataType{
								inventory_iface.TransferListDataType_TRANSFER_LIST_DATA_TYPE_GENERAL,
								inventory_iface.TransferListDataType_TRANSFER_LIST_DATA_TYPE_TOTAL,
							},
						}))
						assert.NoError(t, err)
						return res.Msg
					}

					// either-side filter: both transfers touch src (from) and dst (to).
					bySrc := list(&inventory_iface.TransferListFilter{WarehouseId: srcWarehouse}, nil)
					assert.Len(t, bySrc.GetIds(), 2)
					byDst := list(&inventory_iface.TransferListFilter{WarehouseId: dstWarehouse}, nil)
					assert.Len(t, byDst.GetIds(), 2)
					general, total := transferListMaps(bySrc.GetData())
					g1 := general[transfer1]
					assert.NotNil(t, g1)
					assert.Equal(t, "Gudang A", g1.GetFromWarehouseName())
					assert.Equal(t, "Gudang B", g1.GetToWarehouseName())
					assert.Equal(t, int64(7), total[transfer1].GetItemCount())
					assert.Equal(t, float64(46), total[transfer1].GetAmount())

					// direction filters.
					fromSrc := list(&inventory_iface.TransferListFilter{FromWarehouseId: srcWarehouse}, nil)
					assert.Len(t, fromSrc.GetIds(), 2)
					toSrc := list(&inventory_iface.TransferListFilter{ToWarehouseId: srcWarehouse}, nil)
					assert.Empty(t, toSrc.GetIds())

					// status filter.
					accepted := list(&inventory_iface.TransferListFilter{
						WarehouseId: srcWarehouse,
						Status:      inventory_iface.TransferStatus_TRANSFER_STATUS_ACCEPTED,
					}, nil)
					assert.Equal(t, []uint64{transfer1}, accepted.GetIds())

					// total sort by amount DESC — transfer1 (46) before the canceled one (2*4=8).
					sorted := list(&inventory_iface.TransferListFilter{WarehouseId: srcWarehouse}, &inventory_iface.TransferListSort{
						SortType: common.SortType_SORT_TYPE_DESC,
						S:        &inventory_iface.TransferListSort_Total{Total: inventory_iface.TransferTotalSort_TRANSFER_TOTAL_SORT_AMOUNT},
					})
					assert.Equal(t, transfer1, sorted.GetIds()[0])

					// team filter mismatch returns nothing.
					none := list(&inventory_iface.TransferListFilter{WarehouseId: srcWarehouse, TeamId: 999}, nil)
					assert.Empty(t, none.GetIds())
				})

				t.Run("create validations", func(t *testing.T) {
					_, err := svc.TransferCreate(ctx, connect.NewRequest(&inventory_iface.TransferCreateRequest{
						FromWarehouseId: srcWarehouse,
						ToWarehouseId:   srcWarehouse,
						Items:           []*inventory_iface.TransferItem{{ProductId: 1, Count: 1}},
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err)) // same warehouse

					_, err = svc.TransferCreate(ctx, connect.NewRequest(&inventory_iface.TransferCreateRequest{
						FromWarehouseId: srcWarehouse,
						ToWarehouseId:   dstWarehouse,
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err)) // no items

					_, err = svc.TransferAccept(ctx, connect.NewRequest(&inventory_iface.TransferAcceptRequest{TransferId: 999999}))
					assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))

					_, err = svc.TransferCancel(ctx, connect.NewRequest(&inventory_iface.TransferCancelRequest{TransferId: 999999}))
					assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
				})
			})
		},
	)
}
