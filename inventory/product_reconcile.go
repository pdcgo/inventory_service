package inventory

import (
	"context"
	"io"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	"github.com/pdcgo/inventory_service/inventory_mutations"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ReconcileLogger struct {
	stream *connect.ServerStream[inventory_iface.ProductReconcileResponse]
}

// Write implements [io.Writer].
func (r *ReconcileLogger) Write(p []byte) (n int, err error) {
	c := len(p)

	err = r.stream.Send(&inventory_iface.ProductReconcileResponse{
		Message: string(p),
	})

	return c, err
}

// ProductReconcile implements [inventory_ifaceconnect.InventoryServiceHandler].
//
// It reconciles one product to the legacy on-hand snapshot in a single transaction —
// the RPC counterpart of `sync-legacy` scoped to a single product — streaming a progress
// line (via ReconcileLogger) to the client per reconcile step (StockState, then each rack).
func (s *inventoryServiceImpl) ProductReconcile(
	ctx context.Context,
	req *connect.Request[inventory_iface.ProductReconcileRequest],
	stream *connect.ServerStream[inventory_iface.ProductReconcileResponse],
) error {
	var err error

	productID := req.Msg.GetProductId()
	warehouseID := req.Msg.GetWarehouseId()

	var logwritter io.Writer = &ReconcileLogger{
		stream: stream,
	}

	logger := slog.New(slog.NewTextHandler(logwritter, nil))

	now := time.Now()

	db := s.db.WithContext(ctx)

	err = db.
		Transaction(func(tx *gorm.DB) error {

			// StockState: the legacy on-hand count/value for (product, warehouse).
			var total struct {
				Count  int64
				Amount float64
			}
			if err := tx.
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
				Error; err != nil {
				return err
			}

			if err := inventory_mutations.ReconcileStockState(tx, productID, warehouseID, total.Count, total.Amount, now); err != nil {
				return err
			}

			logger.Info("reconcile state", "productID", productID)

			// StockPlacement: legacy per-rack on-hand, unioned with every rack that
			// currently holds a placement (target 0) so racks that no longer hold stock
			// are reconciled to zero. Ordered by rack_id so locks are taken ascending.
			type rackTarget struct {
				RackID uint64
				Count  int64
			}
			var racks []rackTarget
			err = tx.
				Raw(`
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
					`, productID, warehouseID, productID, warehouseID).
				Scan(&racks).
				Error

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

				logger.Info("reconcile placement", "productID", productID, "rackID", r.RackID)
			}

			return nil
		})

	if err != nil {
		logger.Error("error sync placement and state", "err", err)
		return err
	}

	// this transaction for sync batch stock
	err = db.Transaction(func(tx *gorm.DB) error {
		// 1. source truth of legacy batch stock is from this query
		query := `select 
				coalesce(ih.in_tx_id::text, 'legacy' || '-' || ih.sku_id || '-' || round(ih.price + coalesce(ih.ext_price, 0), 2)::text) as batch_code,
				ih.warehouse_id,
				s.product_id,
				(ih.price + coalesce(ih.ext_price, 0)) as batch_price,
				sum(ih.count * -1) as batch_count
				
				
			from invertory_histories ih
			left join skus s on s.id = ih.sku_id 
			where 
				ih.tx_id is null
				and ih.warehouse_id = ?
				and s.product_id = ?
				
			group by
				batch_code,
				ih.warehouse_id,
				product_id,
				batch_price
			`

		// 2. insert or do nothing to inventory_models.StockBatch.
		// Field names must match the query's snake_case aliases so GORM scans them
		// (batch_price -> BatchPrice, batch_count -> BatchCount).
		type batchRow struct {
			BatchCode   string
			WarehouseID uint64
			ProductID   uint64
			BatchPrice  float64
			BatchCount  int64
		}
		var rows []batchRow
		err := tx.
			Raw(query, warehouseID, productID).
			Scan(&rows).Error
		if err != nil {
			return err
		}

		for _, b := range rows {
			if b.ProductID == 0 || b.BatchCount <= 0 {
				continue // unmapped sku / no on-hand in this batch
			}
			batch := inventory_models.StockBatch{
				ProductID:   b.ProductID,
				WarehouseID: b.WarehouseID,
				InboundID:   0, // legacy reconcile has no inbound transaction
				BatchCode:   b.BatchCode,
				StartCount:  b.BatchCount,
				EndCount:    b.BatchCount,
				Price:       b.BatchPrice,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			// Idempotent: on an existing (product_id, batch_code) leave the row untouched
			// so a batch whose EndCount has drifted from live movements is not clobbered.
			err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&batch).Error
			if err != nil {
				return err
			}

			logger.Info("reconciling batch", "productID", productID, "batch", b.BatchCode)
		}

		logger.Info("reconcile batch success", "productID", productID)
		return nil
	})

	return err
}
