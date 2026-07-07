package inventory_test

import (
	"context"
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

func TestRackByIds(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "rack by ids",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(&inventory_models.Rack{}))

				assert.NoError(t, db.Create(&[]inventory_models.Rack{
					{ID: 1, WarehouseID: 9, Name: "A-01"},
					{ID: 2, WarehouseID: 9, Name: "A-02"},
					{ID: 3, WarehouseID: 9, Name: "Gone", Deleted: true},
				}).Error)

				svc := inventory.NewInventoryService(db)
				ctx := context.Background()

				t.Run("nil filter is invalid argument", func(t *testing.T) {
					_, err := svc.RackByIds(ctx, connect.NewRequest(&inventory_iface.RackByIdsRequest{
						DataRequest: []inventory_iface.RackByIdsDataType{inventory_iface.RackByIdsDataType_RACK_BY_IDS_DATA_TYPE_GENERAL},
					}))
					assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
				})

				t.Run("returns non-deleted keyed by id, omits missing and deleted", func(t *testing.T) {
					res, err := svc.RackByIds(ctx, connect.NewRequest(&inventory_iface.RackByIdsRequest{
						Filter:      &inventory_iface.RackByIdsFilter{Ids: []uint64{1, 2, 3, 999999}},
						DataRequest: []inventory_iface.RackByIdsDataType{inventory_iface.RackByIdsDataType_RACK_BY_IDS_DATA_TYPE_GENERAL},
					}))
					assert.NoError(t, err)
					assert.Len(t, res.Msg.Items, 2) // deleted (3) and missing (999999) omitted

					name := func(id uint64) string {
						list := res.Msg.Items[id]
						if list == nil {
							return ""
						}
						for _, it := range list.Items {
							if g, ok := it.D.(*inventory_iface.RackByIdsItem_General); ok {
								return g.General.Name
							}
						}
						return ""
					}
					assert.Equal(t, "A-01", name(1))
					assert.Equal(t, "A-02", name(2))
					assert.Nil(t, res.Msg.Items[3])
					assert.Nil(t, res.Msg.Items[999999])
				})
			})
		})
}
