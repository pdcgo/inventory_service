package inventory_test

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory"
	"github.com/pdcgo/inventory_service/inventory_models"
	"github.com/pdcgo/schema/services/common/v1"
	"github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/db_models"
	"github.com/pdcgo/shared/pkg/moretest"
	"github.com/pdcgo/shared/pkg/moretest/moretest_mock"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestStockMovement(t *testing.T) {
	var dbScenario moretest_mock.DbScenario

	moretest.Suite(t, "test stock movement",
		moretest.SetupListFunc{
			moretest_mock.MockPostgresDatabase(&dbScenario),
		},
		func(t *testing.T) {
			dbScenario(t, func(db *gorm.DB) {
				err := db.AutoMigrate(
					&inventory_models.StockBatchLog{},
					&db_models.InvTransaction{},
					&db_models.Team{},
					&db_models.User{},
				)

				assert.NoError(t, err)

				stockBatchLogs := []inventory_models.StockBatchLog{
					{
						ID:            10,
						TransactionID: 1,
						WarehouseID:   1,
						ProductID:     1,
						UserID:        1,
						ChangeType:    inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK,
						Change:        1,
						BalanceCount:  1,
						CreatedAt:     time.Now().Add(-time.Hour),
					},
					{
						ID:            20,
						TransactionID: 1,
						WarehouseID:   1,
						ProductID:     1,
						UserID:        1,
						ChangeType:    inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK,
						Change:        1,
						BalanceCount:  2,
						CreatedAt:     time.Now().Add(-time.Hour + (time.Minute * 20)),
					},
					{
						ID:            12,
						TransactionID: 10,
						WarehouseID:   2,
						ProductID:     4,
						UserID:        3,
						ChangeType:    inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK,
						Change:        1,
						BalanceCount:  3,
						CreatedAt:     time.Now(),
					},
				}

				err = db.Create(&stockBatchLogs).Error
				err = db.Create(&db_models.InvTransaction{
					ID:         1,
					Receipt:    "awb",
					TeamID:     1,
					CreateByID: 1,
				}).Error
				err = db.Create(&db_models.Team{
					ID:       1,
					Name:     "team-kakashi",
					TeamCode: "TK",
				}).Error
				err = db.Create(&db_models.User{
					ID:       1,
					Username: "user1",
					Name:     "User One",
				}).Error

				assert.NoError(t, err)

				service := inventory.NewInventoryService(db)

				t.Run("testing stock movement", func(t *testing.T) {
					data, _ := service.StockMovement(t.Context(), &connect.Request[inventory_iface.StockMovementRequest]{
						Msg: &inventory_iface.StockMovementRequest{
							WarehouseId: 1,
							ProductId:   1,
							Page:        &common.PageFilter{Page: 1, Limit: 1},
						}})

					assert.Equal(t, 1, len(data.Msg.Movements))

					fData := data.Msg.Movements[0]

					assert.Equal(t, uint64(20), fData.Id)
					assert.Equal(t, uint64(1), fData.TransactionId)
					assert.Equal(t, uint64(1), fData.WarehouseId)
					assert.Equal(t, uint64(1), fData.ProductId)
					assert.Equal(t, uint64(1), fData.UserId)
					assert.Equal(t, inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK, fData.ChangeType)
					assert.Equal(t, int64(1), fData.Change)
					assert.Equal(t, int64(2), fData.BalanceCount)
					assert.Equal(t, "team-kakashi", fData.TransactionInfo.TeamName)
					assert.Equal(t, "TK", fData.TransactionInfo.TeamCode)
					assert.Equal(t, "user1", fData.TransactionInfo.UserUsername)
					assert.Equal(t, "User One", fData.TransactionInfo.UserName)

				})
			})

		},
	)
}
