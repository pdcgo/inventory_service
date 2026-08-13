package inventory_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Minimal stand-ins for the shared db_models tables, carrying only the columns the
// batch handlers read — they dodge the association cascade AutoMigrate would pull in.
type invTxRow struct {
	ID          uint64 `gorm:"primarykey"`
	ExternOrdID string
	Receipt     string
	Type        string
	Status      string
	Deleted     bool
}

func (invTxRow) TableName() string { return "inv_transactions" }

type invTxItemRow struct {
	ID               uint64 `gorm:"primarykey"`
	InvTransactionID uint64
	SkuID            string
	Count            int64
	Price            float64
	Total            float64
}

func (invTxItemRow) TableName() string { return "inv_tx_items" }

func TestTransactionByIds(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "transaction by ids",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(&invTxRow{}))
				assert.NoError(t, db.Create(&[]invTxRow{
					{ID: 1, ExternOrdID: "EXT-1", Receipt: "RCP-1", Type: "restock", Status: "completed"},
					{ID: 2, ExternOrdID: "EXT-2", Receipt: "RCP-2", Type: "adj_in", Status: "waiting"},
					{ID: 3, ExternOrdID: "EXT-3", Receipt: "RCP-3", Deleted: true},
					// a stored value the wire enum does not know about.
					{ID: 4, ExternOrdID: "EXT-4", Type: "who_knows", Status: "who_knows"},
				}).Error)

				svc := inventory.NewInventoryService(db)
				res, err := svc.TransactionByIds(context.Background(), connect.NewRequest(&inventory_iface.TransactionByIdsRequest{
					Ids: []uint64{1, 2, 3, 4, 404},
				}))
				assert.NoError(t, err)

				txs := res.Msg.GetTransactions()
				assert.Len(t, txs, 3) // 3 is deleted, 404 missing
				assert.Equal(t, "EXT-1", txs[1].GetExternOrderId())
				assert.Equal(t, "RCP-1", txs[1].GetReceipt())
				assert.Equal(t, inventory_iface.TransactionType_TRANSACTION_TYPE_RESTOCK, txs[1].GetType())
				assert.Equal(t, inventory_iface.TransactionStatus_TRANSACTION_STATUS_COMPLETED, txs[1].GetStatus())

				// the adjustment-in a found-back recovery creates.
				assert.Equal(t, inventory_iface.TransactionType_TRANSACTION_TYPE_ADJ_IN, txs[2].GetType())
				assert.Equal(t, inventory_iface.TransactionStatus_TRANSACTION_STATUS_WAITING, txs[2].GetStatus())

				// unknown stored values degrade to UNSPECIFIED rather than erroring.
				assert.Equal(t, inventory_iface.TransactionType_TRANSACTION_TYPE_UNSPECIFIED, txs[4].GetType())
				assert.Equal(t, inventory_iface.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED, txs[4].GetStatus())

				assert.NotContains(t, txs, uint64(3))
				assert.NotContains(t, txs, uint64(404))
			})
		},
	)
}

func TestTransactionItemByIds(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "transaction item by ids",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(&invTxItemRow{}))
				assert.NoError(t, db.Create(&[]invTxItemRow{
					{ID: 10, InvTransactionID: 1, SkuID: "SKU-A", Count: 3, Price: 5000, Total: 15000},
					{ID: 11, InvTransactionID: 1, SkuID: "SKU-B", Count: 1, Price: 2500, Total: 2500},
				}).Error)

				svc := inventory.NewInventoryService(db)
				res, err := svc.TransactionItemByIds(context.Background(), connect.NewRequest(&inventory_iface.TransactionItemByIdsRequest{
					Ids: []uint64{10, 11, 999},
				}))
				assert.NoError(t, err)

				items := res.Msg.GetItems()
				assert.Len(t, items, 2)

				it := items[10]
				assert.Equal(t, uint64(1), it.GetInvTransactionId())
				assert.Equal(t, "SKU-A", it.GetSkuId())
				assert.Equal(t, int64(3), it.GetCount())
				assert.Equal(t, float64(5000), it.GetPrice())
				assert.Equal(t, float64(15000), it.GetTotal())
				assert.NotContains(t, items, uint64(999))
			})
		},
	)
}

func TestTransactionProblemItemByTxItemIds(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "transaction problem item by tx item ids",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(&invItemProblem{}))
				assert.NoError(t, db.Create(&[]invItemProblem{
					{ID: 1, TxItemID: 10, SkuID: "SKU-A", ProblemType: "broken_w", ProblemNote: "penyok", Count: 2},
					{ID: 2, TxItemID: 11, SkuID: "SKU-B", ProblemType: "lost_w", Count: 1},
					// Two rows on the same tx item: the highest id wins (deterministic).
					{ID: 3, TxItemID: 11, SkuID: "SKU-B", ProblemType: "diff_s", ProblemNote: "sobek", Count: 4},
					// A value outside ware_db.ProblemType (legacy/mistyped) must not fail.
					{ID: 4, TxItemID: 12, SkuID: "SKU-C", ProblemType: "broken_r", Count: 1},
				}).Error)

				svc := inventory.NewInventoryService(db)
				res, err := svc.TransactionProblemItemByTxItemIds(context.Background(),
					connect.NewRequest(&inventory_iface.TransactionProblemItemByTxItemIdsRequest{
						TxItemIds: []uint64{10, 11, 12, 999},
					}))
				assert.NoError(t, err)

				items := res.Msg.GetItems()
				assert.Len(t, items, 3) // keyed by tx_item_id; 999 has no problem row

				assert.Equal(t, uint64(1), items[10].GetId())
				assert.Equal(t, uint64(10), items[10].GetTxItemId())
				assert.Equal(t, "SKU-A", items[10].GetSkuId())
				assert.Equal(t, inventory_iface.ProblemType_PROBLEM_TYPE_BROKEN_IN_WAREHOUSE, items[10].GetProblemType())
				assert.Equal(t, "penyok", items[10].GetProblemNote())
				assert.Equal(t, int64(2), items[10].GetCount())

				// tx item 11 had two rows; id 3 (the later one) is reported.
				assert.Equal(t, uint64(3), items[11].GetId())
				assert.Equal(t, inventory_iface.ProblemType_PROBLEM_TYPE_PRODUCT_DIFFERENT, items[11].GetProblemType())
				assert.Equal(t, int64(4), items[11].GetCount())

				// Unknown stored value degrades to UNSPECIFIED rather than erroring.
				assert.Equal(t, inventory_iface.ProblemType_PROBLEM_TYPE_UNSPECIFIED, items[12].GetProblemType())

				assert.NotContains(t, items, uint64(999))
			})
		},
	)
}

func TestProductBySkuIds(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "product by sku ids",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(&productRow{}))
				assert.NoError(t, db.Create(&[]productRow{
					{ID: 100, Name: "Widget", RefID: "REF-100", Image: datatypes.NewJSONSlice([]string{"img-a", "img-b"})},
					{ID: 101, Name: "Gadget", RefID: "REF-101"}, // no image
					{ID: 102, Name: "Gone", RefID: "REF-102", Deleted: true},
				}).Error)

				// A valid sku_id encodes warehouse/team/product/variant; Extract() reads the
				// product id back out. Build real ones so the handler's decode path is exercised.
				skuID := func(productID uint) string {
					id, err := db_models.NewSkuID(&db_models.SkuData{WarehouseID: 9, TeamID: 7, ProductID: productID, VariantID: 1})
					assert.NoError(t, err)
					return string(id)
				}
				sku100 := skuID(100)
				sku101 := skuID(101)
				sku102 := skuID(102)     // product deleted
				skuMissing := skuID(500) // product not seeded

				svc := inventory.NewInventoryService(db)
				res, err := svc.ProductBySkuIds(context.Background(), connect.NewRequest(&inventory_iface.ProductBySkuIdsRequest{
					SkuIds: []string{sku100, sku101, sku102, skuMissing, "not-a-valid-sku"},
				}))
				assert.NoError(t, err)

				products := res.Msg.GetProducts()
				assert.Len(t, products, 2) // deleted, missing-product, and undecodable are omitted

				assert.Equal(t, uint64(100), products[sku100].GetId())
				assert.Equal(t, "Widget", products[sku100].GetName())
				assert.Equal(t, "REF-100", products[sku100].GetRefId())
				assert.Equal(t, "img-a", products[sku100].GetImage()) // first image

				assert.Equal(t, "Gadget", products[sku101].GetName())
				assert.Equal(t, "", products[sku101].GetImage()) // no image → empty

				assert.NotContains(t, products, sku102)
				assert.NotContains(t, products, skuMissing)
			})
		},
	)
}
