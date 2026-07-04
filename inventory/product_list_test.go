package inventory_test

import (
	"testing"
	"time"

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

// teams stand-in (name for the TEAM data type / ProductDetail team_name).
type teamRow struct {
	ID   uint `gorm:"primarykey"`
	Name string
}

func (teamRow) TableName() string { return "teams" }

func productListMaps(data []*inventory_iface.ProductData) (
	map[uint64]*inventory_iface.ProductGeneralItem,
	map[uint64]*inventory_iface.ProductStockItem,
	map[uint64]*inventory_iface.ProductTeamItem,
	map[uint64]*inventory_iface.ProductConfigItem,
) {
	var g map[uint64]*inventory_iface.ProductGeneralItem
	var s map[uint64]*inventory_iface.ProductStockItem
	var tm map[uint64]*inventory_iface.ProductTeamItem
	var c map[uint64]*inventory_iface.ProductConfigItem
	for _, d := range data {
		switch v := d.Data.(type) {
		case *inventory_iface.ProductData_General:
			g = v.General.Data
		case *inventory_iface.ProductData_Stock:
			s = v.Stock.Data
		case *inventory_iface.ProductData_Team:
			tm = v.Team.Data
		case *inventory_iface.ProductData_Config:
			c = v.Config.Data
		}
	}
	return g, s, tm, c
}

func TestProductList(t *testing.T) {
	var scenario moretest_mock.DbScenario
	moretest.Suite(t, "product list",
		moretest.SetupListFunc{moretest_mock.MockPostgresDatabase(&scenario)},
		func(t *testing.T) {
			scenario(t, func(db *gorm.DB) {
				assert.NoError(t, db.AutoMigrate(
					&inventory_models.StockState{},
					&inventory_models.StockBatch{},
					&inventory_models.StockPlacement{},
					&inventory_models.ProductConfig{},
					&productRow{},
					&teamRow{},
				))
				at := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
				assert.NoError(t, db.Create(&[]teamRow{{ID: 1, Name: "Team A"}, {ID: 2, Name: "Team B"}}).Error)
				assert.NoError(t, db.Create(&[]productRow{
					{ID: 100, TeamID: 1, Name: "Apple"},
					{ID: 200, TeamID: 2, Name: "Banana"},
					{ID: 300, TeamID: 1, Name: "Cherry", Deleted: true}, // excluded
					{ID: 400, TeamID: 1, Name: "Durian"},                // wh10 only
				}).Error)
				assert.NoError(t, db.Create(&[]inventory_models.StockState{
					{ProductID: 100, WarehouseID: 9, StockReady: 5, StockReadyAmount: 50, CreatedAt: at, UpdatedAt: at},
					{ProductID: 200, WarehouseID: 9, StockReady: 10, StockReadyAmount: 200, CreatedAt: at, UpdatedAt: at},
					{ProductID: 300, WarehouseID: 9, StockReady: 1, StockReadyAmount: 10, CreatedAt: at, UpdatedAt: at},
					{ProductID: 400, WarehouseID: 10, StockReady: 7, StockReadyAmount: 70, CreatedAt: at, UpdatedAt: at},
				}).Error)
				// p100 stock detail: 1 open batch, 2 racks.
				assert.NoError(t, db.Create(&inventory_models.StockBatch{ProductID: 100, WarehouseID: 9, BatchCode: "b1", StartCount: 5, EndCount: 5, Price: 10, CreatedAt: at, UpdatedAt: at}).Error)
				assert.NoError(t, db.Create(&[]inventory_models.StockPlacement{
					{ProductID: 100, WarehouseID: 9, RackID: 11, Count: 3, CreatedAt: at, UpdatedAt: at},
					{ProductID: 100, WarehouseID: 9, RackID: 12, Count: 2, CreatedAt: at, UpdatedAt: at},
				}).Error)
				// p100 has a config; p200 does not.
				assert.NoError(t, db.Create(&inventory_models.ProductConfig{
					ProductID: 100, WarehouseID: 9,
					QueueType:        inventory_iface.QueueType_QUEUE_TYPE_LIFO,
					PlacementPicking: inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_BIGGER,
				}).Error)

				svc := inventory.NewInventoryService(db)
				page := &common.PageFilter{Page: 1, Limit: 20}
				nameAsc := &inventory_iface.ProductListSort{
					SortType: common.SortType_SORT_TYPE_ASC,
					S:        &inventory_iface.ProductListSort_General{General: inventory_iface.ProductGeneralSort_PRODUCT_GENERAL_SORT_NAME},
				}

				t.Run("lists warehouse products with all data types", func(t *testing.T) {
					res, err := svc.ProductList(t.Context(), connect.NewRequest(&inventory_iface.ProductListRequest{
						Filter: &inventory_iface.ProductListFilter{WarehouseId: 9, Page: page},
						Sort:   nameAsc,
						DataTypes: []inventory_iface.ProductListDataType{
							inventory_iface.ProductListDataType_PRODUCT_LIST_DATA_TYPE_GENERAL,
							inventory_iface.ProductListDataType_PRODUCT_LIST_DATA_TYPE_STOCK,
							inventory_iface.ProductListDataType_PRODUCT_LIST_DATA_TYPE_TEAM,
							inventory_iface.ProductListDataType_PRODUCT_LIST_DATA_TYPE_CONFIG,
						},
					}))
					assert.NoError(t, err)
					assert.Equal(t, []uint64{100, 200}, res.Msg.Ids) // deleted (300) + other-warehouse (400) excluded

					g, s, tm, c := productListMaps(res.Msg.Data)
					assert.Equal(t, "Apple", g[100].Name)
					assert.Equal(t, int64(5), s[100].StockReady)
					assert.Equal(t, float64(50), s[100].StockReadyAmount)
					assert.Equal(t, int64(1), s[100].BatchCount)
					assert.Equal(t, int64(2), s[100].RackCount)
					assert.Equal(t, uint64(1), tm[100].TeamId)
					assert.Equal(t, "Team A", tm[100].TeamName)
					assert.Equal(t, inventory_iface.QueueType_QUEUE_TYPE_LIFO, c[100].QueueType)
					assert.True(t, c[100].Configured)
					assert.Equal(t, inventory_iface.QueueType_QUEUE_TYPE_FIFO, c[200].QueueType) // default
					assert.False(t, c[200].Configured)
				})

				t.Run("search filters by name", func(t *testing.T) {
					res, err := svc.ProductList(t.Context(), connect.NewRequest(&inventory_iface.ProductListRequest{
						Filter:    &inventory_iface.ProductListFilter{WarehouseId: 9, Search: "ban", Page: page},
						Sort:      nameAsc,
						DataTypes: []inventory_iface.ProductListDataType{inventory_iface.ProductListDataType_PRODUCT_LIST_DATA_TYPE_GENERAL},
					}))
					assert.NoError(t, err)
					assert.Equal(t, []uint64{200}, res.Msg.Ids)
				})

				t.Run("team_id filters to the team's products", func(t *testing.T) {
					res, err := svc.ProductList(t.Context(), connect.NewRequest(&inventory_iface.ProductListRequest{
						Filter:    &inventory_iface.ProductListFilter{WarehouseId: 9, TeamId: 2, Page: page},
						Sort:      nameAsc,
						DataTypes: []inventory_iface.ProductListDataType{inventory_iface.ProductListDataType_PRODUCT_LIST_DATA_TYPE_GENERAL},
					}))
					assert.NoError(t, err)
					assert.Equal(t, []uint64{200}, res.Msg.Ids)
				})

				t.Run("sort by stock ready desc", func(t *testing.T) {
					res, err := svc.ProductList(t.Context(), connect.NewRequest(&inventory_iface.ProductListRequest{
						Filter: &inventory_iface.ProductListFilter{WarehouseId: 9, Page: page},
						Sort: &inventory_iface.ProductListSort{
							SortType: common.SortType_SORT_TYPE_DESC,
							S:        &inventory_iface.ProductListSort_Stock{Stock: inventory_iface.ProductStockSort_PRODUCT_STOCK_SORT_STOCK_READY},
						},
						DataTypes: []inventory_iface.ProductListDataType{inventory_iface.ProductListDataType_PRODUCT_LIST_DATA_TYPE_STOCK},
					}))
					assert.NoError(t, err)
					assert.Equal(t, []uint64{200, 100}, res.Msg.Ids) // 10, 5
				})
			})
		},
	)
}
