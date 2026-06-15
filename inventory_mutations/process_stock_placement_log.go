package inventory_mutations

import (
	"github.com/pdcgo/san_collection/san_execution"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"gorm.io/gorm"
)

func NewProcessStockPlacementLog(tx *gorm.DB) san_execution.NextFuncParam[*inventory_iface.StockChange] {

	handler := san_execution.NewChainParam(
		func(next san_execution.NextFuncParam[*inventory_iface.StockChange]) san_execution.NextFuncParam[*inventory_iface.StockChange] {
			return func(data *inventory_iface.StockChange) (*inventory_iface.StockChange, error) {
				// 1. create list of []*inventory_models.StockPlacementLog with this query
				// select
				// 	s.product_id,
				// 	ih.warehouse_id,
				// 	ih.rack_id,
				// 	ih.count as change,
				// 	ih.created as created_at
				// from invertory_histories ih
				// left join skus s on s.id = ih.sku_id
				// where
				// 	ih.tx_id = ?

				// 2. insert log
				// 3. (get and lock) or (create and lock) inventory_models.StockPlacement
				// 4. update change inventory_models.StockPlacement atomicly
				return next(data)
			}
		},
	)
	return handler
}
