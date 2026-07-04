package inventory_test

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory"
	"github.com/pdcgo/inventory_service/inventory_models"
	common "github.com/pdcgo/schema/services/common/v1"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

// Minimal stand-ins for the legacy tables RackList/RackDetail read (name + team), so the
// handlers' joins resolve without pulling the full db_models association graphs.
type warehouseRow struct {
	ID   uint `gorm:"primarykey"`
	Name string
}

func (warehouseRow) TableName() string { return "warehouses" }

type productRow struct {
	ID      uint64 `gorm:"primarykey"`
	TeamID  uint
	Name    string
	Deleted bool
}

func (productRow) TableName() string { return "products" }

// rackListMaps splits a RackListResponse's oneof data into per-type maps.
func rackListMaps(data []*inventory_iface.RackData) (
	map[uint64]*inventory_iface.RackGeneralItem,
	map[uint64]*inventory_iface.RackStockItem,
	map[uint64]*inventory_iface.RackWarehouseItem,
) {
	var g map[uint64]*inventory_iface.RackGeneralItem
	var s map[uint64]*inventory_iface.RackStockItem
	var w map[uint64]*inventory_iface.RackWarehouseItem
	for _, d := range data {
		switch v := d.Data.(type) {
		case *inventory_iface.RackData_General:
			g = v.General.Data
		case *inventory_iface.RackData_Stock:
			s = v.Stock.Data
		case *inventory_iface.RackData_Warehouse:
			w = v.Warehouse.Data
		}
	}
	return g, s, w
}

func TestRackList(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "rack list",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(
					&inventory_models.Rack{},
					&inventory_models.StockPlacement{},
					&warehouseRow{},
					&productRow{},
				))
				assert.NoError(t, db.Create(&warehouseRow{ID: 9, Name: "Main WH"}).Error)
				assert.NoError(t, db.Create(&[]inventory_models.Rack{
					{ID: 1, WarehouseID: 9, Name: "A-01"},
					{ID: 2, WarehouseID: 9, Name: "A-02"},
					{ID: 3, WarehouseID: 9, Name: "B-01"},
					{ID: 4, WarehouseID: 9, Name: "gone", Deleted: true}, // excluded
					{ID: 5, WarehouseID: 10, Name: "OTHER"},             // other warehouse
				}).Error)
				assert.NoError(t, db.Create(&[]productRow{{ID: 100, TeamID: 1}, {ID: 200, TeamID: 2}}).Error)
				// r1 holds team-1 stock (5); r2 holds team-2 stock (10); r3 holds team-1 but zero.
				assert.NoError(t, db.Create(&[]inventory_models.StockPlacement{
					{ProductID: 100, WarehouseID: 9, RackID: 1, Count: 5},
					{ProductID: 200, WarehouseID: 9, RackID: 2, Count: 10},
					{ProductID: 100, WarehouseID: 9, RackID: 3, Count: 0},
				}).Error)

				svc := inventory.NewInventoryService(db)
				page := &common.PageFilter{Page: 1, Limit: 20}
				nameAsc := &inventory_iface.RackListSort{
					SortType: common.SortType_SORT_TYPE_ASC,
					S:        &inventory_iface.RackListSort_General{General: inventory_iface.RackGeneralSort_RACK_GENERAL_SORT_NAME},
				}

				t.Run("lists warehouse racks with all data types", func(t *testing.T) {
					res, err := svc.RackList(t.Context(), connect.NewRequest(&inventory_iface.RackListRequest{
						Filter: &inventory_iface.RackListFilter{WarehouseId: 9, Page: page},
						Sort:   nameAsc,
						DataTypes: []inventory_iface.RackListDataType{
							inventory_iface.RackListDataType_RACK_LIST_DATA_TYPE_GENERAL,
							inventory_iface.RackListDataType_RACK_LIST_DATA_TYPE_STOCK,
							inventory_iface.RackListDataType_RACK_LIST_DATA_TYPE_WAREHOUSE,
						},
					}))
					assert.NoError(t, err)
					assert.Equal(t, []uint64{1, 2, 3}, res.Msg.Ids) // deleted + other-warehouse excluded, name asc

					g, s, w := rackListMaps(res.Msg.Data)
					assert.Equal(t, "A-01", g[1].Name)
					assert.Equal(t, int64(5), s[1].StockCount)
					assert.Equal(t, int64(1), s[1].ProductCount)
					assert.Equal(t, int64(10), s[2].StockCount)
					assert.Equal(t, int64(0), s[3].StockCount)
					assert.Equal(t, int64(0), s[3].ProductCount)
					assert.Equal(t, "Main WH", w[1].WarehouseName)
				})

				t.Run("search filters by name", func(t *testing.T) {
					res, err := svc.RackList(t.Context(), connect.NewRequest(&inventory_iface.RackListRequest{
						Filter:    &inventory_iface.RackListFilter{WarehouseId: 9, Search: "A-", Page: page},
						Sort:      nameAsc,
						DataTypes: []inventory_iface.RackListDataType{inventory_iface.RackListDataType_RACK_LIST_DATA_TYPE_GENERAL},
					}))
					assert.NoError(t, err)
					assert.Equal(t, []uint64{1, 2}, res.Msg.Ids)
				})

				t.Run("team_id filters to racks holding the team's stock", func(t *testing.T) {
					res, err := svc.RackList(t.Context(), connect.NewRequest(&inventory_iface.RackListRequest{
						Filter:    &inventory_iface.RackListFilter{WarehouseId: 9, TeamId: 1, Page: page},
						Sort:      nameAsc,
						DataTypes: []inventory_iface.RackListDataType{inventory_iface.RackListDataType_RACK_LIST_DATA_TYPE_GENERAL},
					}))
					assert.NoError(t, err)
					assert.Equal(t, []uint64{1}, res.Msg.Ids) // r1 only (r3 holds team-1 but count 0)
				})

				t.Run("product_id filters to racks holding the product", func(t *testing.T) {
					res, err := svc.RackList(t.Context(), connect.NewRequest(&inventory_iface.RackListRequest{
						Filter:    &inventory_iface.RackListFilter{WarehouseId: 9, ProductId: 200, Page: page},
						Sort:      nameAsc,
						DataTypes: []inventory_iface.RackListDataType{inventory_iface.RackListDataType_RACK_LIST_DATA_TYPE_GENERAL},
					}))
					assert.NoError(t, err)
					assert.Equal(t, []uint64{2}, res.Msg.Ids) // only r2 holds product 200

					res, err = svc.RackList(t.Context(), connect.NewRequest(&inventory_iface.RackListRequest{
						Filter:    &inventory_iface.RackListFilter{WarehouseId: 9, ProductId: 100, Page: page},
						Sort:      nameAsc,
						DataTypes: []inventory_iface.RackListDataType{inventory_iface.RackListDataType_RACK_LIST_DATA_TYPE_GENERAL},
					}))
					assert.NoError(t, err)
					assert.Equal(t, []uint64{1}, res.Msg.Ids) // r1 holds product 100 (r3 has it but count 0)
				})

				t.Run("sort by stock count desc", func(t *testing.T) {
					res, err := svc.RackList(t.Context(), connect.NewRequest(&inventory_iface.RackListRequest{
						Filter: &inventory_iface.RackListFilter{WarehouseId: 9, Page: page},
						Sort: &inventory_iface.RackListSort{
							SortType: common.SortType_SORT_TYPE_DESC,
							S:        &inventory_iface.RackListSort_Stock{Stock: inventory_iface.RackStockSort_RACK_STOCK_SORT_STOCK_COUNT},
						},
						DataTypes: []inventory_iface.RackListDataType{inventory_iface.RackListDataType_RACK_LIST_DATA_TYPE_STOCK},
					}))
					assert.NoError(t, err)
					assert.Equal(t, []uint64{2, 1, 3}, res.Msg.Ids) // 10, 5, 0
				})
			})
		},
	)
}
