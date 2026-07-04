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

func TestRackCreate(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "rack create",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(&inventory_models.Rack{}))
				svc := inventory.NewInventoryService(db)

				res, err := svc.RackCreate(t.Context(), connect.NewRequest(&inventory_iface.RackCreateRequest{
					WarehouseId: 9, Name: "A-01",
				}))
				assert.NoError(t, err)
				assert.NotZero(t, res.Msg.Id)

				var rack inventory_models.Rack
				assert.NoError(t, db.First(&rack, res.Msg.Id).Error)
				assert.Equal(t, "A-01", rack.Name)
				assert.Equal(t, uint64(9), rack.WarehouseID)
				assert.False(t, rack.Deleted)

				t.Run("validation", func(t *testing.T) {
					_, err := svc.RackCreate(t.Context(), connect.NewRequest(&inventory_iface.RackCreateRequest{WarehouseId: 0, Name: "x"}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
					_, err = svc.RackCreate(t.Context(), connect.NewRequest(&inventory_iface.RackCreateRequest{WarehouseId: 9, Name: ""}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})
			})
		},
	)
}
