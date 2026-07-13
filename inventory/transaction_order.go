package inventory

import (
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	"github.com/pdcgo/inventory_service/inventory_mutations"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// applyOrderOutbound applies the ORDER kind of TransactionCreate (Flow A for the
// selling v3 order service):
//   - items are valued at the warehouse's AVERAGE COST (stock_ready_amount /
//     stock_ready from FOR-UPDATE-locked rows), not the caller price — the caller's
//     price is the selling price and would corrupt StockState amounts with margin;
//   - insufficient ready stock HARD-REJECTS with FailedPrecondition;
//   - racks are auto-picked per ProductConfig (placement_picking, default SMALLER)
//     and applied as ORDER_CREATED placement logs anchored to the transaction — the
//     logs double as the warehouse pick list.
//
// Returns the per-product cost valuation for the response (the order service derives
// cross-team product fees from it).
func applyOrderOutbound(
	tx *gorm.DB,
	txID, warehouseID uint64,
	items []*inventory_iface.TransactionItem,
	now time.Time,
) ([]*inventory_iface.TransactionCostItem, error) {
	// Aggregate requested counts per product (stable order; items may repeat a product).
	productIDs := []uint64{}
	required := map[uint64]int64{}
	for _, it := range items {
		if _, ok := required[it.GetProductId()]; !ok {
			productIDs = append(productIDs, it.GetProductId())
		}
		required[it.GetProductId()] += it.GetCount()
	}

	// Lock the aggregates: the same locked row yields both the availability guard and
	// the average cost the stock-out is valued at.
	var states []inventory_models.StockState
	err := tx.
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("warehouse_id = ? AND product_id IN ?", warehouseID, productIDs).
		Find(&states).
		Error
	if err != nil {
		return nil, err
	}

	ready := map[uint64]int64{}
	unitCost := map[uint64]float64{}
	for _, st := range states {
		ready[st.ProductID] = st.StockReady
		if st.StockReady > 0 {
			unitCost[st.ProductID] = st.StockReadyAmount / float64(st.StockReady)
		}
	}
	for _, pid := range productIDs {
		if ready[pid] < required[pid] {
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("insufficient stock for product %d: ready %d < requested %d",
					pid, ready[pid], required[pid]))
		}
	}

	// Persist items + drive the StockState change, both valued at average cost.
	changes := make([]*inventory_iface.ChangeItem, 0, len(items))
	for _, it := range items {
		cost := unitCost[it.GetProductId()]
		item := inventory_models.InventoryTransactionItem{
			TransactionID: txID,
			ProductID:     it.GetProductId(),
			Count:         it.GetCount(),
			Price:         cost,
		}
		err = tx.Create(&item).Error
		if err != nil {
			return nil, err
		}
		changes = append(changes, &inventory_iface.ChangeItem{
			ProductId:    it.GetProductId(),
			ChangeCount:  it.GetCount(), // magnitude; the OrderCreated reason supplies the sign
			ChangeAmount: float64(it.GetCount()) * cost,
		})
	}

	change := &inventory_iface.StockChange{
		At:            timestamppb.New(now),
		WarehouseId:   warehouseID,
		TransactionId: txID,
		Changes:       changes,
		Change:        &inventory_iface.StockChange_OrderCreated{OrderCreated: &inventory_iface.OrderCreated{}},
	}
	_, err = inventory_mutations.NewProcessStockBatchLog(tx)(change)
	if err != nil {
		return nil, err
	}

	err = applyOrderPlacements(tx, txID, warehouseID, productIDs, required, now)
	if err != nil {
		return nil, err
	}

	costItems := make([]*inventory_iface.TransactionCostItem, 0, len(productIDs))
	for _, pid := range productIDs {
		costItems = append(costItems, &inventory_iface.TransactionCostItem{
			ProductId: pid,
			Count:     required[pid],
			UnitCost:  unitCost[pid],
			TotalCost: float64(required[pid]) * unitCost[pid],
		})
	}
	return costItems, nil
}

// applyOrderPlacements greedily allocates each product's ordered count across its
// racks per the ProductConfig placement policy — SMALLER rack first by default,
// BIGGER when configured — and applies the decrements as ORDER_CREATED placement
// logs. A rack shortfall (placements sum < ordered while StockState sufficed, i.e.
// pre-existing state/placement drift) consumes what exists and leaves the remainder
// as drift for Opname to correct.
func applyOrderPlacements(
	tx *gorm.DB,
	txID, warehouseID uint64,
	productIDs []uint64,
	required map[uint64]int64,
	now time.Time,
) error {
	for _, pid := range productIDs {
		picking := inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_SMALLER
		var cfg inventory_models.ProductConfig
		res := tx.
			Where("product_id = ? AND warehouse_id = ?", pid, warehouseID).
			Limit(1).
			Find(&cfg)
		if res.Error != nil {
			return res.Error
		}
		if cfg.ID != 0 && cfg.PlacementPicking != inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_UNSPECIFIED {
			picking = cfg.PlacementPicking
		}

		orderBy := "count ASC, rack_id ASC"
		if picking == inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_BIGGER {
			orderBy = "count DESC, rack_id ASC"
		}

		var placements []inventory_models.StockPlacement
		err := tx.
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("product_id = ? AND warehouse_id = ? AND count > 0", pid, warehouseID).
			Order(orderBy).
			Find(&placements).
			Error
		if err != nil {
			return err
		}

		remaining := required[pid]
		deltas := []inventory_mutations.PlacementDelta{}
		for _, pl := range placements {
			if remaining <= 0 {
				break
			}
			take := pl.Count
			if take > remaining {
				take = remaining
			}
			deltas = append(deltas, inventory_mutations.PlacementDelta{
				ProductID: pid,
				RackID:    pl.RackID,
				Delta:     -take,
			})
			remaining -= take
		}
		// remaining > 0 here = pre-existing rack drift; intentionally not an error.

		if len(deltas) > 0 {
			err = inventory_mutations.ApplyExplicitPlacements(tx, txID, warehouseID,
				inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_CREATED, deltas, now)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
