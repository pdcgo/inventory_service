package inventory_test

import (
	"testing"

	"github.com/pdcgo/inventory_service/inventory_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

// Verifies the (product_id, batch_code) partial unique index on stock_batches:
// a non-empty code is unique per product, while empty/unset codes are exempt.
func TestStockBatchCodeUnique(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "stock batch code unique",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(&inventory_models.StockBatch{}))

				// All the allowed cases first. (The moretest harness runs the whole
				// scenario in one transaction, and a constraint violation aborts it —
				// so the single expected-error insert must come last.)
				assert.NoError(t, db.Create(&[]inventory_models.StockBatch{
					{ProductID: 1, WarehouseID: 9, BatchCode: "B1"}, // first code for product 1
					{ProductID: 1, WarehouseID: 9, BatchCode: "B2"}, // different code, same product
					{ProductID: 2, WarehouseID: 9, BatchCode: "B1"}, // same code, different product
					{ProductID: 1, WarehouseID: 9, BatchCode: ""},   // empty codes are exempt...
					{ProductID: 1, WarehouseID: 9, BatchCode: ""},   // ...so repeats are allowed
				}).Error)

				// A duplicate (product, non-empty code) violates the partial unique index.
				assert.Error(t, db.Create(&inventory_models.StockBatch{
					ProductID: 1, WarehouseID: 9, BatchCode: "B1",
				}).Error)
			})
		},
	)
}
