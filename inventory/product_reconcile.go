package inventory

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_mutations"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"gorm.io/gorm"
)

// ProductReconcile implements [inventory_ifaceconnect.InventoryServiceHandler].
//
// It drives one product's inventory to the legacy on-hand recorded in
// invertory_histories (rows with tx_id IS NULL): the (product, warehouse)
// StockState and every (product, warehouse, rack) StockPlacement — including racks
// that no longer hold stock, which are reconciled to 0. The RPC counterpart of
// `sync-legacy` scoped to a single product.
//
// The whole product is reconciled in one transaction for integrity. Locks are
// taken StockState-first then placements in ascending rack order, matching the
// live push handler's order, so the two never deadlock.
func (s *inventoryServiceImpl) ProductReconcile(
	ctx context.Context,
	req *connect.Request[inventory_iface.ProductReconcileRequest],
) (*connect.Response[inventory_iface.ProductReconcileResponse], error) {
	var err error

	productID := req.Msg.GetProductId()
	warehouseID := req.Msg.GetWarehouseId()
	now := time.Now()

	err = s.
		db.
		WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// StockState: the legacy on-hand count/value for (product, warehouse).
		var total struct {
			Count  int64
			Amount float64
		}
		err = tx.
			Raw(`
			select
				coalesce(sum(-1 * ih.count), 0) as count,
				coalesce(sum(-1 * ih.count * (ih.price + coalesce(ih.ext_price, 0))), 0) as amount
			from invertory_histories ih
			left join skus s on s.id = ih.sku_id
			where 
				ih.tx_id is null 
				and s.product_id = ? 
				and s.warehouse_id = ?
		`, productID, warehouseID).
			Find(&total).
			Error

		if err != nil {
			return err
		}

		// slog.Info("true_stock",
		// 	"product_id", productID,
		// 	"warehouse_id", warehouseID,
		// 	"true_count", total.Count,
		// 	"true_amount", total.Amount,
		// )

		err = inventory_mutations.ReconcileStockState(tx, productID, warehouseID, total.Count, total.Amount, now)

		if err != nil {
			return err
		}

		// StockPlacement: legacy per-rack on-hand, unioned with every rack that
		// currently holds a placement (target 0) so racks that no longer hold stock
		// are reconciled to zero. Ordered by rack_id so locks are taken ascending.
		type rackTarget struct {
			RackID uint64
			Count  int64
		}
		var racks []rackTarget
		err = tx.Raw(`
			select rack_id, sum(cnt) as count from (
				select ih.rack_id as rack_id, (-1 * ih.count) as cnt
				from invertory_histories ih
				left join skus s on s.id = ih.sku_id
				where ih.tx_id is null and s.product_id = ? and s.warehouse_id = ?
				union all
				select sp.rack_id as rack_id, 0 as cnt
				from stock_placements sp
				where sp.product_id = ? and sp.warehouse_id = ?
			) t
			group by rack_id
			order by rack_id
		`, productID, warehouseID, productID, warehouseID).Scan(&racks).Error
		if err != nil {
			return err
		}

		for _, r := range racks {
			if r.RackID == 0 {
				continue
			}
			if err := inventory_mutations.ReconcileStockPlacement(tx, productID, warehouseID, r.RackID, r.Count, now); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&inventory_iface.ProductReconcileResponse{}), nil
}
