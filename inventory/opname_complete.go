package inventory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	"github.com/pdcgo/inventory_service/inventory_mutations"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// OpnameComplete implements [inventory_ifaceconnect.InventoryServiceHandler]. It
// applies the session's COUNTED lines as the absolute truth: per (rack, product)
// delta = counted - the live locked placement count, applied as ADJUSTMENT
// placement-logs anchored to a minted InventoryTransaction (type opname), with the
// (product, warehouse) aggregate driven through the batch-log engine (variance valued
// at the StockState average price). Uncounted lines are untouched. Idempotent.
func (s *inventoryServiceImpl) OpnameComplete(
	ctx context.Context,
	req *connect.Request[inventory_iface.OpnameCompleteRequest],
) (*connect.Response[inventory_iface.OpnameCompleteResponse], error) {
	pay := req.Msg
	if pay.GetOpnameId() == 0 || pay.GetWarehouseId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("opname_id and warehouse_id are required"))
	}

	resp := &inventory_iface.OpnameCompleteResponse{}

	err := s.db.
		WithContext(ctx).
		Transaction(func(tx *gorm.DB) error {
			opname, err := fetchOpname(tx, pay.GetOpnameId(), pay.GetWarehouseId())
			if err != nil {
				return err
			}
			if opname.Status == inventory_models.OpnameCompleted {
				// idempotent
				if opname.InventoryTransactionID != nil {
					resp.TransactionId = *opname.InventoryTransactionID
				}
				return nil
			}
			if opname.Status == inventory_models.OpnameCanceled {
				return connect.NewError(connect.CodeFailedPrecondition, errors.New("opname is canceled"))
			}

			now := time.Now()

			// Counted lines, in (rack, product) order so locks are taken deadlock-safe.
			var lines []inventory_models.InventoryOpnameLine
			err = tx.
				Where("opname_id = ? AND counted = true", opname.ID).
				Order("rack_id ASC, product_id ASC").
				Find(&lines).
				Error
			if err != nil {
				return err
			}

			// Lock the live placements of the counted (rack, product) pairs and diff
			// against the counted values.
			current, err := lockCountedPlacements(tx, opname.WarehouseID, lines)
			if err != nil {
				return err
			}

			deltas := make([]inventory_mutations.PlacementDelta, 0, len(lines))
			varianceByProduct := map[uint64]int64{}
			for _, l := range lines {
				delta := l.CountedCount - current[placementKey(l.RackID, l.ProductID)]
				if delta == 0 {
					continue
				}
				deltas = append(deltas, inventory_mutations.PlacementDelta{
					ProductID: l.ProductID,
					RackID:    l.RackID,
					Delta:     delta,
					Note:      opnameLineNote(l),
				})
				varianceByProduct[l.ProductID] += delta
			}

			var txID uint64
			if len(deltas) > 0 {
				txID, err = applyOpnameAdjustment(tx, opname, deltas, varianceByProduct, now)
				if err != nil {
					return err
				}
			}

			updates := map[string]interface{}{
				"status":       inventory_models.OpnameCompleted,
				"completed_at": now,
				"updated_at":   now,
			}
			if txID > 0 {
				updates["inventory_transaction_id"] = txID
			}
			err = tx.
				Model(&inventory_models.InventoryOpname{}).
				Where("id = ?", opname.ID).
				Updates(updates).
				Error
			if err != nil {
				return err
			}

			resp.TransactionId = txID
			return appendOpnameLog(tx, opname.ID, "completed", "")
		})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}

func placementKey(rackID, productID uint64) string {
	return fmt.Sprintf("%d-%d", rackID, productID)
}

// opnameLineNote composes the placement-log note from a counted line's optional
// reason + note: "opname: <reason>[ — <note>]" (or just the note), "" when neither
// is set.
func opnameLineNote(l inventory_models.InventoryOpnameLine) string {
	switch {
	case l.Reason != "" && l.Note != "":
		return fmt.Sprintf("opname: %s — %s", l.Reason, l.Note)
	case l.Reason != "":
		return "opname: " + l.Reason
	case l.Note != "":
		return "opname: " + l.Note
	default:
		return ""
	}
}

// lockCountedPlacements FOR-UPDATE locks the live StockPlacement rows touched by the
// counted lines and returns their counts keyed by (rack, product). Pairs with no
// placement row are simply absent (current 0); ApplyExplicitPlacements creates them.
func lockCountedPlacements(
	tx *gorm.DB,
	warehouseID uint64,
	lines []inventory_models.InventoryOpnameLine,
) (map[string]int64, error) {
	current := map[string]int64{}
	if len(lines) == 0 {
		return current, nil
	}

	productIDs := make([]uint64, 0, len(lines))
	rackIDs := make([]uint64, 0, len(lines))
	wanted := map[string]bool{}
	for _, l := range lines {
		productIDs = append(productIDs, l.ProductID)
		rackIDs = append(rackIDs, l.RackID)
		wanted[placementKey(l.RackID, l.ProductID)] = true
	}

	var rows []inventory_models.StockPlacement
	err := tx.
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("warehouse_id = ? AND rack_id IN ? AND product_id IN ?",
			warehouseID, uniqueIDs(rackIDs), uniqueIDs(productIDs)).
		Order("rack_id ASC, product_id ASC").
		Find(&rows).
		Error
	if err != nil {
		return nil, err
	}

	for _, r := range rows {
		key := placementKey(r.RackID, r.ProductID)
		if wanted[key] {
			current[key] = r.Count
		}
	}
	return current, nil
}

// applyOpnameAdjustment mints the opname InventoryTransaction and applies the
// variances: per-rack ADJUSTMENT placement-logs plus the (product, warehouse)
// aggregate through the batch-log engine, each product's variance valued at its
// StockState average price.
func applyOpnameAdjustment(
	tx *gorm.DB,
	opname *inventory_models.InventoryOpname,
	deltas []inventory_mutations.PlacementDelta,
	varianceByProduct map[uint64]int64,
	now time.Time,
) (uint64, error) {
	trx := inventory_models.InventoryTransaction{
		TeamID:      opname.TeamID,
		WarehouseID: opname.WarehouseID,
		Type:        inventory_models.InvTxOpname,
		Status:      inventory_models.InvTxActive,
		CreatedAt:   now,
	}
	err := tx.Create(&trx).Error
	if err != nil {
		return 0, err
	}

	prices, err := stockStateAveragePrices(tx, opname.WarehouseID, mapKeys(varianceByProduct))
	if err != nil {
		return 0, err
	}

	// Stable product order for deterministic item/change rows.
	productIDs := mapKeys(varianceByProduct)
	sort.Slice(productIDs, func(i, j int) bool { return productIDs[i] < productIDs[j] })

	changes := make([]*inventory_iface.ChangeItem, 0, len(productIDs))
	for _, pid := range productIDs {
		variance := varianceByProduct[pid]
		if variance == 0 {
			continue // rack-to-rack differences that net out product-wide
		}
		row := inventory_models.InventoryTransactionItem{
			TransactionID: trx.ID,
			ProductID:     pid,
			Count:         variance, // signed
			Price:         prices[pid],
		}
		err = tx.Create(&row).Error
		if err != nil {
			return 0, err
		}
		changes = append(changes, &inventory_iface.ChangeItem{
			ProductId:    pid,
			ChangeCount:  variance, // Adjustment is caller-signed
			ChangeAmount: float64(variance) * prices[pid],
		})
	}

	if len(changes) > 0 {
		change := &inventory_iface.StockChange{
			At:            timestamppb.New(now),
			WarehouseId:   opname.WarehouseID,
			TransactionId: trx.ID,
			Changes:       changes,
			Change:        &inventory_iface.StockChange_Adjustment{Adjustment: &inventory_iface.Adjustment{}},
		}
		_, err = inventory_mutations.NewProcessStockBatchLog(tx)(change)
		if err != nil {
			return 0, err
		}
	}

	err = inventory_mutations.ApplyExplicitPlacements(tx, trx.ID, opname.WarehouseID,
		inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_ADJUSTMENT, deltas, now)
	if err != nil {
		return 0, err
	}

	return trx.ID, nil
}

// stockStateAveragePrices resolves each product's per-unit value from the warehouse's
// StockState average (stock_ready_amount / stock_ready), 0 when it has no stock there.
func stockStateAveragePrices(tx *gorm.DB, warehouseID uint64, productIDs []uint64) (map[uint64]float64, error) {
	prices := map[uint64]float64{}
	if len(productIDs) == 0 {
		return prices, nil
	}

	var rows []struct {
		ProductID        uint64
		StockReady       int64
		StockReadyAmount float64
	}
	err := tx.
		Table("stock_states").
		Select("product_id, stock_ready, stock_ready_amount").
		Where("warehouse_id = ? AND product_id IN ?", warehouseID, productIDs).
		Scan(&rows).
		Error
	if err != nil {
		return nil, err
	}

	for _, r := range rows {
		if r.StockReady > 0 {
			prices[r.ProductID] = r.StockReadyAmount / float64(r.StockReady)
		}
	}
	return prices, nil
}

func mapKeys(m map[uint64]int64) []uint64 {
	out := make([]uint64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
