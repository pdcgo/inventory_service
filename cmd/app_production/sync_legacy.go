package main

import (
	"context"
	"database/sql"
	"log"
	"log/slog"
	"math"

	"github.com/pdcgo/inventory_service/inventory_models"
	"github.com/pdcgo/inventory_service/inventory_mutations"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/db_models"
	"github.com/urfave/cli/v3"
	"gorm.io/gorm"
)

type SyncLegacyFunc cli.ActionFunc

// legacyStock is the legacy on-hand total for a (product, warehouse).
type LegacyStock struct {
	ProductId   uint64 `gorm:"-"`
	WarehouseId uint64 `gorm:"-"`
	SkuId       string
	Count       int64
	Amount      float64
}

// NewSyncLegacyFunc reconciles inventory StockState against the legacy on-hand
// stock recorded in invertory_histories (rows with tx_id IS NULL — stock that
// never left). For each (product, warehouse) whose legacy total differs from the
// current StockState, it writes an adjustment so StockState matches the legacy
// value. It is a one-way sync: skus absent from the legacy ready stock are left
// untouched.
func NewSyncLegacyFunc(
	db *gorm.DB,
) SyncLegacyFunc {
	return func(ctx context.Context, c *cli.Command) error {

		changeChan := make(chan *inventory_iface.StockChange, 5)

		go func() {
			defer close(changeChan)

			err := db.
				WithContext(ctx).
				Transaction(func(tx *gorm.DB) error {
					// 1+2+4: stream the legacy on-hand per sku and aggregate it onto the
					// (product, warehouse) grain of StockState.
					return collectLegacyStock(tx, func(leg *LegacyStock) error {
						slog.Info("calculating", "productid", leg.ProductId, "warehouseid", leg.WarehouseId)

						var st inventory_models.StockState
						res := tx.
							Model(&inventory_models.StockState{}).
							Where("product_id = ? AND warehouse_id = ?", leg.ProductId, leg.WarehouseId).
							Limit(1).
							Find(&st)

						if res.Error != nil {
							return res.Error
						}

						diffCount := leg.Count - st.StockReady
						diffAmount := leg.Amount - st.StockReadyAmount
						if diffCount == 0 && math.Abs(diffAmount) < 1e-6 {
							return nil
						}

						change := &inventory_iface.StockChange{
							WarehouseId: leg.WarehouseId,
							Change:      &inventory_iface.StockChange_Adjustment{Adjustment: &inventory_iface.Adjustment{}},
							Changes: []*inventory_iface.ChangeItem{{
								ProductId:    leg.ProductId,
								ChangeCount:  diffCount,
								ChangeAmount: diffAmount,
							}},
						}

						changeChan <- change
						return nil
					})

				}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})

			if err != nil {
				panic(err)
			}

		}()

		var err error
		for change := range changeChan {
			slog.Info("sync", "productid", change)
			err = db.Transaction(func(tx *gorm.DB) error {
				proc := inventory_mutations.NewProcessStockBatchLog(tx)
				_, err = proc(change)
				return err
			})

			if err != nil {
				return err
			}

		}

		return nil

	}
}

// collectLegacyStock streams the legacy ready stock (tx_id IS NULL) grouped per
// sku and folds it onto the (product, warehouse) grain. invertory_histories.count
// is stored negative for on-hand stock, so the on-hand count is sum(-1 * count)
// and the value is sum(-1 * count * (price + ext_price)). Rows are fully drained
// (and closed) before the caller issues further queries on the same tx.
func collectLegacyStock(tx *gorm.DB, handler func(st *LegacyStock) error) error {
	datas := []*LegacyStock{}

	err := tx.
		Raw(`
		select
			ih.sku_id as sku_id,
			sum(-1 * ih.count) as count,
			sum(-1 * ih.count * (ih.price + coalesce(ih.ext_price, 0))) as amount
		from invertory_histories ih
		where ih.tx_id is null
		group by ih.sku_id
	`).
		Find(&datas).Error

	if err != nil {
		return err
	}

	for _, row := range datas {

		sku, err := db_models.SkuID(row.SkuId).Extract()
		if err != nil {
			log.Printf("sync-legacy: skipping malformed sku_id %q: %v", row.SkuId, err)
			continue
		}
		if sku.ProductID == 0 || sku.WarehouseID == 0 {
			continue
		}

		row.ProductId = uint64(sku.ProductID)
		row.WarehouseId = uint64(sku.WarehouseID)

		err = handler(row)
		if err != nil {
			return err
		}
	}

	return nil
}
