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

func TestProductConfig(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "product config",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(&inventory_models.ProductConfig{}))
				svc := inventory.NewInventoryService(db)

				t.Run("returns defaults when unset", func(t *testing.T) {
					res, err := svc.ProductConfig(t.Context(), connect.NewRequest(&inventory_iface.ProductConfigRequest{ProductId: 5, WarehouseId: 9}))
					assert.NoError(t, err)
					assert.Equal(t, inventory_iface.QueueType_QUEUE_TYPE_FIFO, res.Msg.QueueType)
					assert.Equal(t, inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_SMALLER, res.Msg.PlacementPicking)
					assert.False(t, res.Msg.Configured)
				})

				t.Run("returns the stored config", func(t *testing.T) {
					assert.NoError(t, db.Create(&inventory_models.ProductConfig{
						ProductID: 5, WarehouseID: 9,
						QueueType:        inventory_iface.QueueType_QUEUE_TYPE_LIFO,
						PlacementPicking: inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_BIGGER,
					}).Error)
					res, err := svc.ProductConfig(t.Context(), connect.NewRequest(&inventory_iface.ProductConfigRequest{ProductId: 5, WarehouseId: 9}))
					assert.NoError(t, err)
					assert.Equal(t, inventory_iface.QueueType_QUEUE_TYPE_LIFO, res.Msg.QueueType)
					assert.Equal(t, inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_BIGGER, res.Msg.PlacementPicking)
					assert.True(t, res.Msg.Configured)
				})

				t.Run("validation", func(t *testing.T) {
					_, err := svc.ProductConfig(t.Context(), connect.NewRequest(&inventory_iface.ProductConfigRequest{ProductId: 0, WarehouseId: 9}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})
			})
		},
	)
}
