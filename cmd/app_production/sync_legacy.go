package main

import (
	"context"
	"log"
	"time"

	"github.com/pdcgo/inventory_service/inventory_mutations"
	"github.com/pdcgo/shared/db_models"
	"github.com/urfave/cli/v3"
	"gorm.io/gorm"
)

type SyncLegacyFunc cli.ActionFunc

// NewSyncLegacyFunc reconciles inventory against the legacy on-hand stock in
// invertory_histories (rows with tx_id IS NULL — stock that never left): the
// (product, warehouse) StockState and the (product, warehouse, rack)
// StockPlacement are each driven to the legacy total via an adjustment. It is a
// one-way sync — skus/racks absent from the legacy ready stock are left untouched.
//
// Each entry is reconciled in its own short transaction (a single FOR UPDATE lock,
// then commit), so the sync only ever holds one row lock at a time. That makes it
// safe to run alongside the live push handler: it can neither deadlock against it
// (it never waits on a second lock while holding one) nor block it with a large,
// long-held lock set.
func NewSyncLegacyFunc(
	db *gorm.DB,
) SyncLegacyFunc {
	return func(ctx context.Context, c *cli.Command) error {
		if err := syncStockState(ctx, db); err != nil {
			return err
		}
		return syncStockPlacement(ctx, db)
	}
}

type stockStateKey struct {
	ProductId   uint64
	WarehouseId uint64
}

type stockTotal struct {
	Count  int64
	Amount float64
}

// syncStockState reconciles the (product, warehouse) StockState count/value to
// the aggregated legacy on-hand, one (product, warehouse) per transaction.
func syncStockState(ctx context.Context, db *gorm.DB) error {
	legacy, err := aggregateLegacyStockState(ctx, db)
	if err != nil {
		return err
	}

	now := time.Now()
	for key, leg := range legacy {
		key, leg := key, leg
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return inventory_mutations.ReconcileStockState(tx, key.ProductId, key.WarehouseId, leg.Count, leg.Amount, now)
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// aggregateLegacyStockState reads the legacy on-hand per sku and folds it onto the
// (product, warehouse) grain. invertory_histories.count is stored negative for
// on-hand stock, so the on-hand count is sum(-1 * count) and the value is netted
// at landed cost.
func aggregateLegacyStockState(ctx context.Context, db *gorm.DB) (map[stockStateKey]stockTotal, error) {
	type row struct {
		SkuId  string
		Count  int64
		Amount float64
	}
	rows := []*row{}
	err := db.WithContext(ctx).Raw(`
		select
			ih.sku_id as sku_id,
			sum(-1 * ih.count) as count,
			sum(-1 * ih.count * (ih.price + coalesce(ih.ext_price, 0))) as amount
		from invertory_histories ih
		where ih.tx_id is null
		group by ih.sku_id
	`).Find(&rows).Error
	if err != nil {
		return nil, err
	}

	legacy := map[stockStateKey]stockTotal{}
	for _, r := range rows {
		sku, err := db_models.SkuID(r.SkuId).Extract()
		if err != nil {
			log.Printf("sync-legacy: skipping malformed sku_id %q: %v", r.SkuId, err)
			continue
		}
		if sku.ProductID == 0 || sku.WarehouseID == 0 {
			continue
		}
		key := stockStateKey{uint64(sku.ProductID), uint64(sku.WarehouseID)}
		agg := legacy[key]
		agg.Count += r.Count
		agg.Amount += r.Amount
		legacy[key] = agg
	}
	return legacy, nil
}

type stockPlacementKey struct {
	ProductId   uint64
	WarehouseId uint64
	RackId      uint64
}

// syncStockPlacement reconciles the (product, warehouse, rack) StockPlacement
// count to the legacy per-rack on-hand, one rack per transaction. Placement tracks
// count only.
func syncStockPlacement(ctx context.Context, db *gorm.DB) error {
	legacy, err := aggregateLegacyPlacement(ctx, db)
	if err != nil {
		return err
	}

	now := time.Now()
	for key, count := range legacy {
		key, count := key, count
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return inventory_mutations.ReconcileStockPlacement(tx, key.ProductId, key.WarehouseId, key.RackId, count, now)
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// aggregateLegacyPlacement reads the legacy on-hand per (sku, rack) and folds it
// onto the (product, warehouse, rack) grain.
func aggregateLegacyPlacement(ctx context.Context, db *gorm.DB) (map[stockPlacementKey]int64, error) {
	type row struct {
		SkuId  string
		RackId uint64
		Count  int64
	}
	rows := []*row{}
	err := db.WithContext(ctx).Raw(`
		select
			ih.sku_id as sku_id,
			ih.rack_id as rack_id,
			sum(-1 * ih.count) as count
		from invertory_histories ih
		where ih.tx_id is null
		group by ih.sku_id, ih.rack_id
	`).Find(&rows).Error
	if err != nil {
		return nil, err
	}

	legacy := map[stockPlacementKey]int64{}
	for _, r := range rows {
		sku, err := db_models.SkuID(r.SkuId).Extract()
		if err != nil {
			log.Printf("sync-legacy: skipping malformed sku_id %q: %v", r.SkuId, err)
			continue
		}
		if sku.ProductID == 0 || sku.WarehouseID == 0 || r.RackId == 0 {
			continue
		}
		key := stockPlacementKey{uint64(sku.ProductID), uint64(sku.WarehouseID), r.RackId}
		legacy[key] += r.Count
	}
	return legacy, nil
}
