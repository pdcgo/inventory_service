package inventory_mutations

import (
	"errors"
	"time"

	"github.com/pdcgo/inventory_service/inventory_models"
	"github.com/pdcgo/san_collection/san_execution"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// NewProcessStockPlacementLog maintains the per-(product, warehouse, rack)
// StockPlacement balance for a StockChange. A StockChange carries no rack detail,
// so the rack-level movements are re-derived from invertory_histories for the
// referenced transaction (its tx_id rows hold the per-rack magnitudes). Each rack
// movement becomes a StockPlacementLog and is applied to StockPlacement.Count with
// the reason's direction sign, mirroring NewProcessStockBatchLog so a product's
// racks sum to its StockState.StockReady.
func NewProcessStockPlacementLog(tx *gorm.DB) san_execution.NextFuncParam[*inventory_iface.StockChange] {

	handler := san_execution.NewChainParam(
		func(next san_execution.NextFuncParam[*inventory_iface.StockChange]) san_execution.NextFuncParam[*inventory_iface.StockChange] {
			return func(data *inventory_iface.StockChange) (*inventory_iface.StockChange, error) {
				now := changeTime(data)
				changeType, sign, err := changeDirection(data)
				if err != nil {
					return nil, err
				}

				// Transfer is caller-signed: changeDirection returns +1, but the direction
				// lives in the signed change_count. All racks of one transfer move the same
				// way, so take the sign from the change items (OUT decrements, IN/cancel add).
				if _, isTransfer := data.Change.(*inventory_iface.StockChange_Transfer); isTransfer {
					sign = 1
					if len(data.Changes) > 0 && data.Changes[0].ChangeCount < 0 {
						sign = -1
					}
				}

				// 1. per-rack movements for this transaction. invertory_histories.count
				// is a positive magnitude on tx_id rows; product_id comes from skus.
				type placementRow struct {
					ProductID   uint64
					WarehouseID uint64
					RackID      uint64
					Change      int64
					CreatedAt   time.Time
				}
				rows := []*placementRow{}
				err = tx.
					Table("invertory_histories ih").
					Select([]string{
						"s.product_id as product_id",
						"ih.warehouse_id as warehouse_id",
						"ih.rack_id as rack_id",
						"ih.count as change",
						"ih.created as created_at",
					}).
					Joins("left join skus s on s.id = ih.sku_id").
					Where("ih.tx_id = ?", data.TransactionId).
					Order("ih.rack_id").
					Find(&rows).
					Error
				if err != nil {
					return nil, err
				}

				for _, row := range rows {
					if row.ProductID == 0 || row.RackID == 0 {
						continue // unmapped sku / rack
					}

					// the reason gives the direction; ih.count gives the magnitude.
					delta := sign * row.Change

					// 2. (get and lock) or (create and lock) the placement
					pl, err := lockOrCreateStockPlacement(tx, row.ProductID, row.WarehouseID, row.RackID, now)
					if err != nil {
						return nil, err
					}

					// 3. apply the signed delta to the locked row
					pl.Count += delta
					if err := tx.Model(&inventory_models.StockPlacement{}).
						Where("id = ?", pl.ID).
						Updates(map[string]interface{}{
							"count":      pl.Count,
							"updated_at": now,
						}).Error; err != nil {
						return nil, err
					}

					// 4. log the change with the resulting balance
					log := inventory_models.StockPlacementLog{
						ProductID:     row.ProductID,
						WarehouseID:   row.WarehouseID,
						RackID:        row.RackID,
						TransactionID: data.TransactionId,
						UserID:        data.UserId,
						ChangeType:    changeType,
						Change:        delta,
						BalanceCount:  pl.Count,
						CreatedAt:     row.CreatedAt,
					}
					if err := tx.Create(&log).Error; err != nil {
						return nil, err
					}
				}

				return next(data)
			}
		},
	)
	return handler
}

// lockOrCreateStockPlacement locks the StockPlacement row for
// (productID, warehouseID, rackID) FOR UPDATE, creating a zero row if none exists.
// The create is race-safe (ON CONFLICT DO NOTHING + re-read under the lock) — see
// lockOrCreateStockState. Requires READ COMMITTED.
func lockOrCreateStockPlacement(tx *gorm.DB, productID, warehouseID, rackID uint64, now time.Time) (*inventory_models.StockPlacement, error) {
	var pl inventory_models.StockPlacement
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("product_id = ? AND warehouse_id = ? AND rack_id = ?", productID, warehouseID, rackID).
		First(&pl).Error
	if err == nil {
		return &pl, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	create := inventory_models.StockPlacement{
		ProductID:   productID,
		WarehouseID: warehouseID,
		RackID:      rackID,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&create).Error; err != nil {
		return nil, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("product_id = ? AND warehouse_id = ? AND rack_id = ?", productID, warehouseID, rackID).
		First(&pl).Error; err != nil {
		return nil, err
	}
	return &pl, nil
}
