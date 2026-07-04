package inventory_test

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory"
	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestRackDetail(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "rack detail",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				// warehouseRow is defined in rack_list_test.go (same package).
				assert.NoError(t, db.AutoMigrate(&inventory_models.Rack{}, &inventory_models.StockPlacement{}, &warehouseRow{}))
				assert.NoError(t, db.Create(&warehouseRow{ID: 9, Name: "Main WH"}).Error)
				assert.NoError(t, db.Create(&inventory_models.Rack{ID: 1, WarehouseID: 9, Name: "A-01"}).Error)
				// rack 1: products 5 (3) + 6 (2) held; product 7 present but zero (excluded from product count).
				assert.NoError(t, db.Create(&[]inventory_models.StockPlacement{
					{ProductID: 5, WarehouseID: 9, RackID: 1, Count: 3},
					{ProductID: 6, WarehouseID: 9, RackID: 1, Count: 2},
					{ProductID: 7, WarehouseID: 9, RackID: 1, Count: 0},
				}).Error)
				svc := inventory.NewInventoryService(db)

				res, err := svc.RackDetail(t.Context(), connect.NewRequest(&inventory_iface.RackDetailRequest{Id: 1, WarehouseId: 9}))
				assert.NoError(t, err)
				assert.Equal(t, "A-01", res.Msg.Name)
				assert.Equal(t, int64(5), res.Msg.StockCount)     // 3 + 2 + 0
				assert.Equal(t, int64(2), res.Msg.ProductCount)   // products 5 and 6 (7 has count 0)
				assert.Equal(t, "Main WH", res.Msg.WarehouseName)
				assert.NotNil(t, res.Msg.CreatedAt)

				t.Run("wrong warehouse is not found", func(t *testing.T) {
					_, err := svc.RackDetail(t.Context(), connect.NewRequest(&inventory_iface.RackDetailRequest{Id: 1, WarehouseId: 10}))
					assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
				})
			})
		},
	)
}
