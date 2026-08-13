package inventory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	"github.com/pdcgo/inventory_service/inventory_mutations"
	"github.com/pdcgo/invoice_service/invoice_v2"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	invoice_iface "github.com/pdcgo/schema/services/invoice_iface/v2"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// RestockUpdate implements [inventory_ifaceconnect.InventoryServiceHandler]. It
// applies a BATCH of actions to a restock document — in request order, all-or-
// nothing in one transaction (see docs/restock-implementation.md):
//   - change_status: PENDING <-> PROBLEM marker (optional note append). Pre-accept only.
//   - update_items / update_shipping_fee / update_shipping_info: header/line edits.
//     Pre-accept only.
//   - restock_cancel: PENDING/PROBLEM -> canceled status flip. Pre-accept only —
//     an accepted restock can no longer be canceled. Idempotent.
//   - restock_accept: mints the inventory transaction for the EFFECTIVE counts
//     (ordered - problem; problem goods never enter stock), optionally placing the
//     goods into racks. Sets inventory_transaction_id. Idempotent.
func (s *inventoryServiceImpl) RestockUpdate(
	ctx context.Context,
	req *connect.Request[inventory_iface.RestockUpdateRequest],
) (*connect.Response[inventory_iface.RestockUpdateResponse], error) {
	pay := req.Msg
	if pay.GetRestockId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("restock_id is required"))
	}
	if len(pay.GetActions()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("actions are required"))
	}

	err := s.db.
		WithContext(ctx).
		Transaction(func(tx *gorm.DB) error {
			q := tx.Where("id = ?", pay.GetRestockId())
			if pay.GetWarehouseId() > 0 {
				q = q.Where("warehouse_id = ?", pay.GetWarehouseId())
			}
			var restock inventory_models.InventoryRestock
			err := q.First(&restock).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return connect.NewError(connect.CodeNotFound, errors.New("restock not found"))
				}
				return err
			}

			// Actions apply in order; each mutator keeps the in-memory restock in
			// sync so later actions see the new state (e.g. accept then edit fails).
			for _, action := range pay.GetActions() {
				switch act := action.GetAct().(type) {
				case *inventory_iface.RestockUpdateAction_ChangeStatus:
					err = changeRestockStatus(tx, &restock, act.ChangeStatus)
				case *inventory_iface.RestockUpdateAction_UpdateItems:
					err = updateRestockItems(tx, &restock, act.UpdateItems)
				case *inventory_iface.RestockUpdateAction_UpdateShippingFee:
					err = updateRestockShippingFee(tx, &restock, act.UpdateShippingFee)
				case *inventory_iface.RestockUpdateAction_UpdateShippingInfo:
					err = updateRestockShippingInfo(tx, &restock, act.UpdateShippingInfo)
				case *inventory_iface.RestockUpdateAction_RestockCancel:
					err = cancelRestock(tx, &restock)
				case *inventory_iface.RestockUpdateAction_RestockAccept:
					err = acceptRestock(tx, &restock, act.RestockAccept)
				default:
					err = connect.NewError(connect.CodeInvalidArgument, errors.New("action is required"))
				}
				if err != nil {
					return err
				}
			}
			return nil
		})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&inventory_iface.RestockUpdateResponse{}), nil
}

// requireEditable rejects the pre-accept-only actions once the restock is
// accepted (per the spec) or canceled.
func requireEditable(restock *inventory_models.InventoryRestock) error {
	if restock.Status == inventory_models.RestockAccepted {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("restock is already accepted"))
	}
	if restock.Status == inventory_models.RestockCanceled {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("restock is canceled"))
	}
	return nil
}

// changeRestockStatus toggles the PENDING <-> PROBLEM marker (never accepted /
// canceled — those have dedicated actions); a non-empty note is appended.
func changeRestockStatus(tx *gorm.DB, restock *inventory_models.InventoryRestock, act *inventory_iface.ChangeStatus) error {
	err := requireEditable(restock)
	if err != nil {
		return err
	}

	var status inventory_models.RestockStatus
	switch act.GetStatus() {
	case inventory_iface.RestockStatus_RESTOCK_STATUS_PENDING:
		status = inventory_models.RestockPending
	case inventory_iface.RestockStatus_RESTOCK_STATUS_PROBLEM:
		status = inventory_models.RestockProblem
	default:
		return connect.NewError(connect.CodeInvalidArgument, errors.New("change_status only allows pending or problem"))
	}

	note := restock.Note
	if act.GetNote() != "" {
		if note != "" {
			note = note + "\n" + act.GetNote()
		} else {
			note = act.GetNote()
		}
	}

	err = tx.
		Model(&inventory_models.InventoryRestock{}).
		Where("id = ?", restock.ID).
		Updates(map[string]interface{}{
			"status":     status,
			"note":       note,
			"updated_at": time.Now(),
		}).
		Error
	if err != nil {
		return err
	}
	restock.Status = status
	restock.Note = note

	logAction := "edited"
	if status == inventory_models.RestockProblem {
		logAction = "problem"
	}
	return appendRestockLog(tx, restock.ID, logAction, act.GetNote())
}

// updateRestockItems replaces the restock's item lines. Pre-accept only.
func updateRestockItems(tx *gorm.DB, restock *inventory_models.InventoryRestock, act *inventory_iface.UpdateItems) error {
	err := requireEditable(restock)
	if err != nil {
		return err
	}
	err = validateRestockItems(act.GetItems())
	if err != nil {
		return err
	}

	err = tx.
		Where("restock_id = ?", restock.ID).
		Delete(&inventory_models.InventoryRestockItem{}).
		Error
	if err != nil {
		return err
	}
	err = createRestockItems(tx, restock.ID, act.GetItems())
	if err != nil {
		return err
	}
	return appendRestockLog(tx, restock.ID, "edited", "items replaced")
}

// updateRestockShippingFee updates the ongkir. Pre-accept only.
func updateRestockShippingFee(tx *gorm.DB, restock *inventory_models.InventoryRestock, act *inventory_iface.UpdateShippingFee) error {
	err := requireEditable(restock)
	if err != nil {
		return err
	}
	err = tx.
		Model(&inventory_models.InventoryRestock{}).
		Where("id = ?", restock.ID).
		Updates(map[string]interface{}{
			"shipping_cost": act.GetShippingCost(),
			"updated_at":    time.Now(),
		}).
		Error
	if err != nil {
		return err
	}
	restock.ShippingCost = act.GetShippingCost()
	return appendRestockLog(tx, restock.ID, "edited", "shipping fee updated")
}

// updateRestockShippingInfo updates the purchase/shipping info. Pre-accept only.
func updateRestockShippingInfo(tx *gorm.DB, restock *inventory_models.InventoryRestock, act *inventory_iface.UpdateShippingInfo) error {
	err := requireEditable(restock)
	if err != nil {
		return err
	}
	paymentType := paymentTypeToModel(act.GetPaymentType())
	err = tx.
		Model(&inventory_models.InventoryRestock{}).
		Where("id = ?", restock.ID).
		Updates(map[string]interface{}{
			"shipping_id":     act.GetShippingId(),
			"receipt":         act.GetReceipt(),
			"extern_order_id": act.GetExternOrderId(),
			"payment_type":    paymentType,
			"updated_at":      time.Now(),
		}).
		Error
	if err != nil {
		return err
	}
	restock.ShippingID = act.GetShippingId()
	restock.Receipt = act.GetReceipt()
	restock.ExternOrderID = act.GetExternOrderId()
	restock.PaymentType = paymentType
	return appendRestockLog(tx, restock.ID, "edited", "shipping info updated")
}

// acceptRestock mints the inventory transaction for the EFFECTIVE counts
// (ordered - problem), applies the stock mutation (StockState + StockBatchLog +
// StockBatch), optionally places the goods into racks, then links the transaction
// and flips the document to accepted. Re-accepting is a no-op.
func acceptRestock(tx *gorm.DB, restock *inventory_models.InventoryRestock, accept *inventory_iface.RestockAccept) error {
	if restock.Status == inventory_models.RestockAccepted {
		return nil // idempotent
	}
	if restock.Status == inventory_models.RestockCanceled {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("restock is canceled"))
	}

	fee := accept.GetWarehouseAcceptFee()
	if fee < 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("warehouse_accept_fee cannot be negative"))
	}

	var rows []inventory_models.InventoryRestockItem
	err := tx.
		Where("restock_id = ?", restock.ID).
		Order("id ASC").
		Find(&rows).
		Error
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("restock has no items"))
	}
	ordered := map[uint64]*inventory_models.InventoryRestockItem{}
	for i := range rows {
		ordered[rows[i].ProductID] = &rows[i]
	}

	// 1. problem goods discovered at accept: recorded per item, EXCLUDED from stock.
	problems := map[uint64]int64{}
	for _, p := range accept.GetProblems().GetItems() {
		if p.GetProductId() == 0 || p.GetCount() <= 0 {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("each problem item needs product_id and count > 0"))
		}
		item, ok := ordered[p.GetProductId()]
		if !ok {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("problem item product is not part of the restock"))
		}
		if p.GetCount() > item.Count {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("problem count exceeds the ordered count"))
		}
		problems[p.GetProductId()] = p.GetCount()
		err = tx.
			Model(&inventory_models.InventoryRestockItem{}).
			Where("id = ?", item.ID).
			Updates(map[string]interface{}{
				"problem_count": p.GetCount(),
				"problem_note":  p.GetNote(),
			}).
			Error
		if err != nil {
			return err
		}
	}

	// 2. effective counts (what actually enters stock).
	effective := map[uint64]int64{}
	totalEffective := int64(0)
	items := make([]*inventory_iface.TransactionItem, 0, len(rows))
	for _, row := range rows {
		eff := row.Count - problems[row.ProductID]
		effective[row.ProductID] = eff
		if eff > 0 {
			totalEffective += eff
			items = append(items, &inventory_iface.TransactionItem{
				ProductId: row.ProductID,
				Count:     eff,
				Price:     row.Price,
			})
		}
	}
	if len(items) == 0 {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("nothing to accept — every item is problematic"))
	}

	// Landed cost (docs/restock-implementation.md §5): the accepted pieces absorb
	// the whole shipping fee + warehouse accept fee —
	// price = purchase price + (shipping + fee) / accepted pieces.
	// totalEffective > 0 here because items is non-empty.
	perPiece := (restock.ShippingCost + fee) / float64(totalEffective)
	for _, it := range items {
		it.Price += perPiece
	}

	// 3. placements (REQUIRED): every accepted (effective) unit must be placed on a
	// live rack of the restock's warehouse — placed + problem == ordered per product.
	placeDeltas := []inventory_mutations.PlacementDelta{}
	placedPerProduct := map[uint64]int64{}
	rackIDs := []uint64{}
	for _, pl := range accept.GetPlacements().GetItems() {
		if pl.GetProductId() == 0 || pl.GetRackId() == 0 || pl.GetCount() <= 0 {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("each placement needs product_id, rack_id and count > 0"))
		}
		if _, ok := ordered[pl.GetProductId()]; !ok {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("placement product is not part of the restock"))
		}
		placedPerProduct[pl.GetProductId()] += pl.GetCount()
		rackIDs = append(rackIDs, pl.GetRackId())
		placeDeltas = append(placeDeltas, inventory_mutations.PlacementDelta{
			ProductID: pl.GetProductId(),
			RackID:    pl.GetRackId(),
			Delta:     pl.GetCount(),
		})
	}

	if len(rackIDs) > 0 {
		var rackCount int64
		err = tx.
			Model(&inventory_models.Rack{}).
			Where("id IN ? AND warehouse_id = ? AND deleted = false", rackIDs, restock.WarehouseID).
			Distinct("id").
			Count(&rackCount).
			Error
		if err != nil {
			return err
		}
		if rackCount != int64(len(uniqueIDs(rackIDs))) {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("placement rack is not a live rack of the restock's warehouse"))
		}
	}

	// Every effective unit must be placed; an empty/short placements list fails here.
	for pid, eff := range effective {
		if placedPerProduct[pid] != eff {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("every accepted unit must be placed on a rack (placed + problem == ordered per product)"))
		}
	}
	for pid := range placedPerProduct {
		if _, ok := effective[pid]; !ok {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("placement product is not part of the restock"))
		}
	}

	now := time.Now()

	// 4. mint the inventory transaction with the effective items.
	trx := inventory_models.InventoryTransaction{
		TeamID:      restock.TeamID,
		WarehouseID: restock.WarehouseID,
		Type:        inventory_models.InvTxRestock,
		Status:      inventory_models.InvTxActive,
		CreatedAt:   now,
	}
	err = tx.Create(&trx).Error
	if err != nil {
		return err
	}
	txID := trx.ID

	changes := make([]*inventory_iface.ChangeItem, 0, len(items))
	for _, it := range items {
		row := inventory_models.InventoryTransactionItem{
			TransactionID: txID,
			ProductID:     it.GetProductId(),
			Count:         it.GetCount(),
			Price:         it.GetPrice(),
		}
		err = tx.Create(&row).Error
		if err != nil {
			return err
		}
		changes = append(changes, &inventory_iface.ChangeItem{
			ProductId:    it.GetProductId(),
			ChangeCount:  it.GetCount(), // magnitude; the reason supplies the sign
			ChangeAmount: float64(it.GetCount()) * it.GetPrice(),
		})
	}

	change := &inventory_iface.StockChange{
		At:            timestamppb.New(now),
		WarehouseId:   restock.WarehouseID,
		TransactionId: txID,
		Changes:       changes,
	}
	setStockChangeReason(change, inventory_models.InvTxRestock)
	_, err = inventory_mutations.NewProcessStockBatchLog(tx)(change)
	if err != nil {
		return err
	}

	err = mintTransactionBatches(tx, txID, restock.WarehouseID, items, now)
	if err != nil {
		return err
	}

	// 5. place the goods into racks.
	if len(placeDeltas) > 0 {
		err = inventory_mutations.ApplyExplicitPlacements(tx, txID, restock.WarehouseID,
			inventory_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK, placeDeltas, now)
		if err != nil {
			return err
		}
	}

	// 6. link + flip.
	err = tx.
		Model(&inventory_models.InventoryRestock{}).
		Where("id = ?", restock.ID).
		Updates(map[string]interface{}{
			"status":                   inventory_models.RestockAccepted,
			"inventory_transaction_id": txID,
			"accepted_at":              now,
			"updated_at":               now,
			"warehouse_accept_fee":     fee,
		}).
		Error
	if err != nil {
		return err
	}
	restock.Status = inventory_models.RestockAccepted
	restock.InventoryTransactionID = &txID
	restock.WarehouseAcceptFee = fee

	// 7. warehouse accept fee: the selling team (restock.TeamID) owes the accepting
	// warehouse (restock.WarehouseID). Posted atomically in this tx — warehouse gets a
	// RECEIVABLE +fee, selling team the mirrored PAYABLE -fee.
	if fee > 0 && restock.WarehouseID != 0 && restock.TeamID != 0 && restock.WarehouseID != restock.TeamID {
		err = invoice_v2.PostBalanceLog(
			tx, restock.WarehouseID, restock.TeamID,
			invoice_iface.BalanceChangeType_BALANCE_CHANGE_TYPE_WAREHOUSE_FEE,
			fee, invoice_iface.BalanceType_BALANCE_TYPE_RECEIVABLE,
			fmt.Sprintf("restock %d warehouse accept fee", restock.ID), 0, now,
			&invoice_v2.RestockSource{
				TxID:        txID,
				TeamID:      restock.TeamID, // the team charged the fee, constant across legs
				WarehouseID: restock.WarehouseID,
			},
		)
		if err != nil {
			return err
		}
	}

	return appendRestockLog(tx, restock.ID, "accepted", "")
}

// cancelRestock cancels the document — a status flip only, available while the
// restock is not accepted (spec rule 2); the minted transaction of an accepted
// restock is untouchable through this action. A second cancel is a no-op.
func cancelRestock(tx *gorm.DB, restock *inventory_models.InventoryRestock) error {
	if restock.Status == inventory_models.RestockCanceled {
		return nil // idempotent
	}
	if restock.Status == inventory_models.RestockAccepted {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("restock is already accepted"))
	}

	now := time.Now()
	err := tx.
		Model(&inventory_models.InventoryRestock{}).
		Where("id = ?", restock.ID).
		Updates(map[string]interface{}{
			"status":      inventory_models.RestockCanceled,
			"canceled_at": now,
			"updated_at":  now,
		}).
		Error
	if err != nil {
		return err
	}
	restock.Status = inventory_models.RestockCanceled

	return appendRestockLog(tx, restock.ID, "canceled", "")
}

// uniqueIDs returns the distinct values of ids.
func uniqueIDs(ids []uint64) []uint64 {
	seen := map[uint64]bool{}
	out := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
