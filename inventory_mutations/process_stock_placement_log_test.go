package inventory_mutations_test

import (
	"testing"
	"time"

	"github.com/pdcgo/inventory_service/inventory_models"
	"github.com/pdcgo/inventory_service/inventory_mutations"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// skuRow is a minimal stand-in for the skus table so the placement query's
// `left join skus s on s.id = ih.sku_id` resolves product_id, without pulling
// db_models.Sku's full association graph into the test.
type skuRow struct {
	ID        db_models.SkuID `gorm:"primarykey"`
	ProductID uint
}

func (skuRow) TableName() string { return "skus" }

func TestProcessStockPlacementLog(t *testing.T) {
	var scenario moretest_mock.DbScenario

	moretest.Suite(t, "process stock placement log",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&scenario),
		},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(
					&inventory_models.StockPlacement{},
					&inventory_models.StockPlacementLog{},
					&db_models.InvertoryHistory{},
					&skuRow{},
				))

				at := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)

				sku, err := db_models.NewSkuID(&db_models.SkuData{WarehouseID: 9, TeamID: 1, ProductID: 5, VariantID: 1})
				assert.NoError(t, err)
				assert.NoError(t, db.Create(&skuRow{ID: sku, ProductID: 5}).Error)

				// transaction 100 placed/picked product 5 across two racks (8 + 2),
				// stored as positive magnitudes on the tx_id rows.
				assert.NoError(t, db.Create(&[]db_models.InvertoryHistory{
					{TxID: ptr(uint(100)), SkuID: sku, WarehouseID: 9, RackID: 11, Count: 8, Created: at},
					{TxID: ptr(uint(100)), SkuID: sku, WarehouseID: 9, RackID: 12, Count: 2, Created: at},
				}).Error)

				placementOf := func(rackID uint64) (inventory_models.StockPlacement, bool) {
					var p inventory_models.StockPlacement
					res := db.Where("product_id = ? AND warehouse_id = ? AND rack_id = ?", uint64(5), uint64(9), rackID).Limit(1).Find(&p)
					assert.NoError(t, res.Error)
					return p, res.RowsAffected > 0
				}
				logCount := func() int64 {
					var n int64
					assert.NoError(t, db.Model(&inventory_models.StockPlacementLog{}).Count(&n).Error)
					return n
				}

				t.Run("restock adds per-rack stock with the reason sign", func(t *testing.T) {
					_, err := inventory_mutations.NewProcessStockPlacementLog(db)(&inventory_iface.StockChange{
						At:            timestamppb.New(at),
						WarehouseId:   9,
						UserId:        7,
						TransactionId: 100,
						Change:        &inventory_iface.StockChange_Restock{Restock: &inventory_iface.Restock{}},
					})
					assert.NoError(t, err)

					p11, ok := placementOf(11)
					assert.True(t, ok)
					assert.Equal(t, int64(8), p11.Count)
					p12, _ := placementOf(12)
					assert.Equal(t, int64(2), p12.Count)
					assert.Equal(t, int64(2), logCount())

					var log inventory_models.StockPlacementLog
					assert.NoError(t, db.Where("rack_id = ?", uint64(11)).First(&log).Error)
					assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK, log.ChangeType)
					assert.Equal(t, int64(8), log.Change)
					assert.Equal(t, int64(8), log.BalanceCount) // running balance after the change
					assert.Equal(t, uint64(100), log.TransactionID)
					assert.Equal(t, uint64(7), log.UserID)
					assert.Equal(t, uint64(5), log.ProductID)
					assert.True(t, at.Equal(log.CreatedAt))

					var log12 inventory_models.StockPlacementLog
					assert.NoError(t, db.Where("rack_id = ?", uint64(12)).First(&log12).Error)
					assert.Equal(t, int64(2), log12.BalanceCount)
				})

				t.Run("order created subtracts per-rack stock", func(t *testing.T) {
					_, err := inventory_mutations.NewProcessStockPlacementLog(db)(&inventory_iface.StockChange{
						At:            timestamppb.New(at),
						WarehouseId:   9,
						UserId:        7,
						TransactionId: 100,
						Change: &inventory_iface.StockChange_OrderCreated{
							OrderCreated: &inventory_iface.OrderCreated{OrderId: 100},
						},
					})
					assert.NoError(t, err)

					p11, _ := placementOf(11)
					assert.Equal(t, int64(0), p11.Count) // 8 - 8
					p12, _ := placementOf(12)
					assert.Equal(t, int64(0), p12.Count) // 2 - 2
					assert.Equal(t, int64(4), logCount())

					// latest rack-11 log records the order's −8 and the resulting 0 balance.
					var log inventory_models.StockPlacementLog
					assert.NoError(t, db.Where("rack_id = ?", uint64(11)).Order("id desc").First(&log).Error)
					assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_CREATED, log.ChangeType)
					assert.Equal(t, int64(-8), log.Change)
					assert.Equal(t, int64(0), log.BalanceCount)
				})

				t.Run("unknown transaction is a no-op", func(t *testing.T) {
					_, err := inventory_mutations.NewProcessStockPlacementLog(db)(&inventory_iface.StockChange{
						At:            timestamppb.New(at),
						WarehouseId:   9,
						TransactionId: 999,
						Change:        &inventory_iface.StockChange_Restock{Restock: &inventory_iface.Restock{}},
					})
					assert.NoError(t, err)
					assert.Equal(t, int64(4), logCount()) // unchanged
				})

				t.Run("transfer out subtracts per-rack stock", func(t *testing.T) {
					// fresh tx + rack, independent of the prior subtests.
					assert.NoError(t, db.Create(&db_models.InvertoryHistory{
						TxID: ptr(uint(200)), SkuID: sku, WarehouseID: 9, RackID: 31, Count: 6, Created: at,
					}).Error)

					// Transfer reason is caller-signed; the negative ChangeItem drives the OUT sign.
					_, err := inventory_mutations.NewProcessStockPlacementLog(db)(&inventory_iface.StockChange{
						At:            timestamppb.New(at),
						WarehouseId:   9,
						UserId:        7,
						TransactionId: 200,
						Changes:       []*inventory_iface.ChangeItem{{ProductId: 5, ChangeCount: -6, ChangeAmount: -60}},
						Change:        &inventory_iface.StockChange_Transfer{Transfer: &inventory_iface.Transfer{}},
					})
					assert.NoError(t, err)

					p31, ok := placementOf(31)
					assert.True(t, ok)
					assert.Equal(t, int64(-6), p31.Count) // OUT decrements the source rack

					var log inventory_models.StockPlacementLog
					assert.NoError(t, db.Where("rack_id = ?", uint64(31)).Order("id desc").First(&log).Error)
					assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER, log.ChangeType)
					assert.Equal(t, int64(-6), log.Change)
				})
			})
		},
	)
}

func ptr[T any](v T) *T { return &v }
