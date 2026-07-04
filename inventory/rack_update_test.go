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

func TestRackUpdate(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "rack update",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(&inventory_models.Rack{}))
				assert.NoError(t, db.Create(&inventory_models.Rack{ID: 1, WarehouseID: 9, Name: "old"}).Error)
				assert.NoError(t, db.Create(&inventory_models.Rack{ID: 2, WarehouseID: 9, Name: "gone", Deleted: true}).Error)
				svc := inventory.NewInventoryService(db)

				_, err := svc.RackUpdate(t.Context(), connect.NewRequest(&inventory_iface.RackUpdateRequest{Id: 1, WarehouseId: 9, Name: "new"}))
				assert.NoError(t, err)
				var rack inventory_models.Rack
				assert.NoError(t, db.First(&rack, 1).Error)
				assert.Equal(t, "new", rack.Name)

				t.Run("wrong warehouse is not found", func(t *testing.T) {
					_, err := svc.RackUpdate(t.Context(), connect.NewRequest(&inventory_iface.RackUpdateRequest{Id: 1, WarehouseId: 10, Name: "x"}))
					assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
				})
				t.Run("deleted rack is not found", func(t *testing.T) {
					_, err := svc.RackUpdate(t.Context(), connect.NewRequest(&inventory_iface.RackUpdateRequest{Id: 2, WarehouseId: 9, Name: "x"}))
					assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
				})
				t.Run("validation", func(t *testing.T) {
					_, err := svc.RackUpdate(t.Context(), connect.NewRequest(&inventory_iface.RackUpdateRequest{Id: 1, WarehouseId: 9, Name: ""}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})
			})
		},
	)
}
