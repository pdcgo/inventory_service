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

func TestProductConfigUpdate(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "product config update",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(&inventory_models.ProductConfig{}))
				svc := inventory.NewInventoryService(db)

				update := func(qt inventory_iface.QueueType, pp inventory_iface.PlacementPickingType) error {
					_, err := svc.ProductConfigUpdate(t.Context(), connect.NewRequest(&inventory_iface.ProductConfigUpdateRequest{
						ProductId: 5, WarehouseId: 9, QueueType: qt, PlacementPicking: pp,
					}))
					return err
				}
				count := func() int64 {
					var n int64
					assert.NoError(t, db.Model(&inventory_models.ProductConfig{}).Count(&n).Error)
					return n
				}
				load := func() inventory_models.ProductConfig {
					var c inventory_models.ProductConfig
					assert.NoError(t, db.Where("product_id = ? AND warehouse_id = ?", 5, 9).First(&c).Error)
					return c
				}

				t.Run("inserts a config", func(t *testing.T) {
					assert.NoError(t, update(inventory_iface.QueueType_QUEUE_TYPE_LIFO, inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_BIGGER))
					c := load()
					assert.Equal(t, inventory_iface.QueueType_QUEUE_TYPE_LIFO, c.QueueType)
					assert.Equal(t, inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_BIGGER, c.PlacementPicking)
					assert.Equal(t, int64(1), count())
				})

				t.Run("upserts (no duplicate row)", func(t *testing.T) {
					assert.NoError(t, update(inventory_iface.QueueType_QUEUE_TYPE_BY_EXPIRING_DATE, inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_SMALLER))
					c := load()
					assert.Equal(t, inventory_iface.QueueType_QUEUE_TYPE_BY_EXPIRING_DATE, c.QueueType)
					assert.Equal(t, inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_SMALLER, c.PlacementPicking)
					assert.Equal(t, int64(1), count()) // updated in place
				})

				t.Run("validation", func(t *testing.T) {
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(update(inventory_iface.QueueType_QUEUE_TYPE_UNSPECIFIED, inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_SMALLER)))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(update(inventory_iface.QueueType_QUEUE_TYPE_FIFO, inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_UNSPECIFIED)))
				})
			})
		},
	)
}
