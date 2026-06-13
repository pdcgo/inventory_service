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

// NewProcessStockBatchLog returns a chain that applies a StockChange to the
// per-(product, warehouse) StockState running balance and appends a StockBatchLog
// per changed product.
//
// Stage 1 locks (or creates) every StockState the change touches up front, so the
// whole StockChange mutates atomically and consistently; stage 2 applies the
// signed delta to each locked state and writes the resulting balance into the log.
// Only the count side (StockReady / BalanceCount) is maintained — cost/batch (FIFO)
// tracking is out of scope here, so the *Amount and Batch* log fields stay zero.
func NewProcessStockBatchLog(tx *gorm.DB) san_execution.NextFuncParam[*inventory_iface.StockChange] {

	state := map[uint64]*inventory_models.StockState{} // state is map[product_id]

	handler := san_execution.NewChainParam(
		func(next san_execution.NextFuncParam[*inventory_iface.StockChange]) san_execution.NextFuncParam[*inventory_iface.StockChange] {
			return func(data *inventory_iface.StockChange) (*inventory_iface.StockChange, error) { // locking StockState
				now := changeTime(data)
				for _, item := range data.Changes {
					if item == nil || item.ProductId == 0 {
						continue
					}
					if _, ok := state[item.ProductId]; ok {
						continue
					}
					st, err := lockOrCreateStockState(tx, item.ProductId, data.WarehouseId, now)
					if err != nil {
						return nil, err
					}
					state[item.ProductId] = st
				}
				return next(data)
			}
		},
		func(next san_execution.NextFuncParam[*inventory_iface.StockChange]) san_execution.NextFuncParam[*inventory_iface.StockChange] {
			return func(data *inventory_iface.StockChange) (*inventory_iface.StockChange, error) { // inserting to table
				now := changeTime(data)
				changeType, sign, err := changeDirection(data)
				if err != nil {
					return nil, err
				}
				for _, item := range data.Changes {
					if item == nil || item.ProductId == 0 {
						continue
					}
					st, ok := state[item.ProductId]
					if !ok {
						continue
					}

					// count only: amount is the quantity; the reason gives the sign.
					delta := sign * int64(item.Amount)
					st.StockReady += delta

					if err := tx.Model(&inventory_models.StockState{}).
						Where("id = ?", st.ID).
						Updates(map[string]interface{}{
							"stock_ready": st.StockReady,
							"updated_at":  now,
						}).Error; err != nil {
						return nil, err
					}

					log := inventory_models.StockBatchLog{
						ProductID:     item.ProductId,
						WarehouseID:   data.WarehouseId,
						UserID:        data.UserId,
						TransactionID: data.TransactionId,
						ChangeType:    changeType,
						Change:        delta,
						BalanceCount:  st.StockReady,
						CreatedAt:     now,
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

// changeDirection maps the StockChange reason to its log change_type and the sign
// applied to ready stock:
//   - stock leaves on order-created and warehouse problems (−);
//   - stock comes in on order-cancel, restock, return, and found-back (+);
//   - adjustment and transfer are caller-signed: the sign multiplier is +1 so the
//     ChangeItem.amount's own sign decides the direction (transfer out / negative
//     adjustment use a negative amount).
func changeDirection(data *inventory_iface.StockChange) (inventory_iface.StockChangeType, int64, error) {
	switch data.Change.(type) {
	case *inventory_iface.StockChange_OrderCreated:
		return inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_CREATED, -1, nil
	case *inventory_iface.StockChange_OrderCanceled:
		return inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_CANCELED, 1, nil
	case *inventory_iface.StockChange_Restock:
		return inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK, 1, nil
	case *inventory_iface.StockChange_Return:
		return inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RETURN, 1, nil
	case *inventory_iface.StockChange_Problem:
		return inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_PROBLEM, -1, nil
	case *inventory_iface.StockChange_FoundBack:
		return inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_FOUND_BACK, 1, nil
	case *inventory_iface.StockChange_Adjustment:
		return inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ADJUSTMENT, 1, nil
	case *inventory_iface.StockChange_Transfer:
		return inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER, 1, nil
	default:
		return inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_UNSPECIFIED, 0, errors.New("stock change reason is required")
	}
}

// changeTime uses the event time when present, else now.
func changeTime(data *inventory_iface.StockChange) time.Time {
	if data.At != nil {
		return data.At.AsTime()
	}
	return time.Now()
}

// lockOrCreateStockState locks the StockState row for (productID, warehouseID) FOR
// UPDATE, creating a zero row if none exists.
func lockOrCreateStockState(tx *gorm.DB, productID, warehouseID uint64, now time.Time) (*inventory_models.StockState, error) {
	var st inventory_models.StockState
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("product_id = ? AND warehouse_id = ?", productID, warehouseID).
		First(&st).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		st = inventory_models.StockState{
			ProductID:   productID,
			WarehouseID: warehouseID,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := tx.Create(&st).Error; err != nil {
			return nil, err
		}
	}
	return &st, nil
}
