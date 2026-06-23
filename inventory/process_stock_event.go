package inventory

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/pdcgo/inventory_service/inventory_models"
	"github.com/pdcgo/inventory_service/inventory_mutations"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	warehouse_iface "github.com/pdcgo/schema/services/warehouse_iface/v1"
	"github.com/pdcgo/shared/db_models"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// ProcessStockEvent applies a warehouse StockEvent by expanding the transaction
// it references into per-product stock changes. The rich stock_change variant is
// not published yet, so each handled variant carries only a transaction_id: the
// transaction's items are expanded (inv_tx_items, minus inv_item_problems) into
// per-SKU StockChangeLogs and converted via stockChangeLogToInventory. Deferred
// variants (adjustment, stock_change) are no-ops for now.
//
// It is shared by both ingestion paths — the Pub/Sub HTTP push handler and the
// InventoryService.PushStockEvent RPC — and expects to run inside a transaction.
func ProcessStockEvent(tx *gorm.DB, event *warehouse_iface.StockEvent) error {
	switch data := event.Data.(type) {
	case *warehouse_iface.StockEvent_OrderAccepted:
		return applyTxItems(tx, data.OrderAccepted.TransactionId, warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_ACCEPTED)
	case *warehouse_iface.StockEvent_OrderCanceled:
		return applyOrderCancel(tx, data.OrderCanceled.TransactionId)
	case *warehouse_iface.StockEvent_RestockAccepted:
		return applyTxItems(tx, data.RestockAccepted.TransactionId, warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK_ACCEPTED)
	case *warehouse_iface.StockEvent_ReturnAccepted:
		return applyTxItems(tx, data.ReturnAccepted.TransactionId, warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_RETURN_ACCEPTED)
	case *warehouse_iface.StockEvent_StockProblem:
		return applyTxItems(tx, data.StockProblem.TransactionId, warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_STOCK_PROBLEM)
	case *warehouse_iface.StockEvent_StockFoundBack:
		return applyTxItems(tx, data.StockFoundBack.TransactionId, warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_STOCK_FOUND_BACK)
	case *warehouse_iface.StockEvent_TransferWarehouseCreated:
		return applyTransfer(tx, data.TransferWarehouseCreated.TransferId, warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER_WAREHOUSE_OUT)
	case *warehouse_iface.StockEvent_TransferWarehouseAccepted:
		return applyTransfer(tx, data.TransferWarehouseAccepted.TransferId, warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER_WAREHOUSE_IN)
	case *warehouse_iface.StockEvent_TransferWarehouseCanceled:
		return applyTransferCancel(tx, data.TransferWarehouseCanceled.TransferId)
		// TODO(inventory): StockAdjustment is not handled yet — its semantics are not
		// finalized upstream (the warehouse never emits it; its expansion uses n=0).
	}

	return nil
}

// applyTxItems expands a transaction's items into per-SKU StockChangeLogs of the
// given warehouse change type and applies each to inventory stock state.
func applyTxItems(tx *gorm.DB, transactionId uint64, changeType warehouse_iface.StockChangeType) error {
	logs, err := expandTxItems(tx, transactionId, changeType)
	if err != nil {
		return err
	}
	return applyStockLogs(tx, logs)
}

// applyTransfer resolves the WarehouseTransfer for a transfer event and applies its OUT leg
// (OutboundTxID) or IN leg (InboundTxID) under the given change type — mirroring the warehouse
// push_handler dispatch (Created→OUT, Accepted→IN, Canceled→OUT_CANCELED).
func applyTransfer(tx *gorm.DB, transferId uint64, changeType warehouse_iface.StockChangeType) error {
	var transfer db_models.WarehouseTransfer
	if err := tx.First(&transfer, transferId).Error; err != nil {
		return err
	}
	txID := uint64(transfer.OutboundTxID)
	switch changeType {
	case warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER_WAREHOUSE_IN,
		warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER_WAREHOUSE_IN_CANCELED:
		txID = uint64(transfer.InboundTxID)
	}
	return applyTxItems(tx, txID, changeType)
}

// applyOrderCancel reverses an order by reconstructing the cancel from its
// existing stock batch log (see reconstructCancel), using the ORDER_CANCELED
// reason so the reversing log is typed as a cancel.
func applyOrderCancel(tx *gorm.DB, transactionId uint64) error {
	return reconstructCancel(tx, transactionId,
		"order cancel: no stock batch log for transaction %d",
		func() *inventory_iface.StockChange {
			return &inventory_iface.StockChange{
				Change: &inventory_iface.StockChange_OrderCanceled{OrderCanceled: &inventory_iface.OrderCanceled{}},
			}
		})
}

// applyTransferCancel reverses a canceled warehouse transfer's OUT leg (the
// inbound leg is not touched by a cancel) by reconstructing it from the outbound
// transaction's existing stock batch log (see reconstructCancel), using the
// TRANSFER reason — the same reason the OUT leg was logged under, so the reversal
// nets it to zero.
func applyTransferCancel(tx *gorm.DB, transferId uint64) error {
	var transfer db_models.WarehouseTransfer
	if err := tx.First(&transfer, transferId).Error; err != nil {
		return err
	}
	return reconstructCancel(tx, uint64(transfer.OutboundTxID),
		"transfer cancel: no stock batch log for transaction %d",
		func() *inventory_iface.StockChange {
			return &inventory_iface.StockChange{
				Change: &inventory_iface.StockChange_Transfer{Transfer: &inventory_iface.Transfer{}},
			}
		})
}

// reconstructCancel reverses a transaction by reconstructing the cancel from its
// existing stock batch log rather than re-expanding source items, so a duplicated
// cancel event is idempotent. It sums the net change recorded for the transaction
// per (product, warehouse):
//   - no rows at all → the source was never applied here, so it errors with
//     notFoundMsg (a "...%d" format string; a Pub/Sub re-delivery then retries
//     until the source event lands);
//   - a net of zero → already canceled, so it does nothing;
//   - a non-zero net → writes one reversing batch log per group that brings the
//     transaction's net to zero, then restores rack placement once.
//
// reason supplies a fresh StockChange carrying only the reversal's reason (its
// At/WarehouseId/TransactionId/Changes are filled in here). The net-zero guard
// gates the whole operation (batch log/state and placement alike), so a second
// cancel event re-reads a net of zero and skips everything.
func reconstructCancel(
	tx *gorm.DB,
	transactionId uint64,
	notFoundMsg string,
	reason func() *inventory_iface.StockChange,
) error {
	type netRow struct {
		ProductID   uint64
		WarehouseID uint64
		NetCount    int64
		NetAmount   float64
	}

	// change * price reconstructs each row's signed value delta (Price is the
	// per-unit cost, Change the signed count), so the sum is the net value moved.
	var nets []netRow
	err := tx.
		Model(&inventory_models.StockBatchLog{}).
		Select("product_id, warehouse_id, sum(change) as net_count, sum(change * price) as net_amount").
		Where("transaction_id = ?", transactionId).
		Group("product_id, warehouse_id").
		Order("warehouse_id, product_id"). // deterministic, deadlock-safe lock order
		Find(&nets).
		Error
	if err != nil {
		return err
	}
	if len(nets) == 0 {
		return fmt.Errorf(notFoundMsg, transactionId)
	}

	// Reverse the net per warehouse, skipping groups already at zero.
	byWarehouse := map[uint64][]*inventory_iface.ChangeItem{}
	warehouses := make([]uint64, 0, len(nets))
	for _, n := range nets {
		if n.NetCount == 0 {
			continue
		}
		if _, ok := byWarehouse[n.WarehouseID]; !ok {
			warehouses = append(warehouses, n.WarehouseID)
		}
		byWarehouse[n.WarehouseID] = append(byWarehouse[n.WarehouseID], &inventory_iface.ChangeItem{
			ProductId:    n.ProductID,
			ChangeCount:  -n.NetCount,
			ChangeAmount: -n.NetAmount,
		})
	}
	if len(byWarehouse) == 0 {
		return nil // already canceled
	}

	now := time.Now()
	batchProc := inventory_mutations.NewProcessStockBatchLog(tx)
	for _, warehouseID := range warehouses {
		change := reason()
		change.At = timestamppb.New(now)
		change.WarehouseId = warehouseID
		change.TransactionId = transactionId
		change.Changes = byWarehouse[warehouseID]
		if _, err := batchProc(change); err != nil {
			return err
		}
	}

	// Placement re-derives the rack moves from invertory_histories by tx_id (the
	// warehouse and change items are irrelevant to its rack list), so run it once
	// per transaction to restore the racks the source had moved. Leaving Changes
	// nil keeps the transfer reason's sign at its +1 default, which restores.
	placement := reason()
	placement.At = timestamppb.New(now)
	placement.WarehouseId = warehouses[0]
	placement.TransactionId = transactionId
	_, err = inventory_mutations.NewProcessStockPlacementLog(tx)(placement)
	return err
}

// applyStockLogs converts each warehouse StockChangeLog to an inventory
// StockChange and applies it to inventory state: the stock-batch-log processor
// runs per converted change (updating StockState), while the placement processor
// runs once for the whole transaction (it re-derives per-rack moves from
// invertory_histories by tx_id, so a single call covers every rack).
func applyStockLogs(tx *gorm.DB, logs []*warehouse_iface.StockChangeLog) error {
	// Lock the per-product StockState rows in a deterministic (warehouse, product)
	// order so two concurrent multi-product events can't acquire them in opposite
	// orders and deadlock. The order is invisible to the resulting balances.
	sort.SliceStable(logs, func(i, j int) bool {
		if logs[i].WarehouseId != logs[j].WarehouseId {
			return logs[i].WarehouseId < logs[j].WarehouseId
		}
		return stockLogProductID(logs[i]) < stockLogProductID(logs[j])
	})

	batchProc := inventory_mutations.NewProcessStockBatchLog(tx)
	var placementChange *inventory_iface.StockChange
	for _, log := range logs {
		change, err := stockChangeLogToInventory(log)
		if err != nil {
			return err
		}
		if change == nil {
			continue // unmapped type
		}
		if _, err := batchProc(change); err != nil {
			return err
		}
		if placementChange == nil {
			placementChange = change // any change carries the transaction + reason
		}
	}

	if placementChange != nil {
		if _, err := inventory_mutations.NewProcessStockPlacementLog(tx)(placementChange); err != nil {
			return err
		}
	}
	return nil
}

// stockLogProductID decodes the product id from a change log's SkuID, used only to
// order StockState locking. Returns 0 if the SkuID can't be decoded (such a row is
// then skipped by the batch processor's product_id == 0 guard).
func stockLogProductID(log *warehouse_iface.StockChangeLog) uint64 {
	sku, err := db_models.SkuID(log.SkuId).Extract()
	if err != nil {
		return 0
	}
	return uint64(sku.ProductID)
}

// txExpandRow is the projected shape of an expanded transaction item.
type txExpandRow struct {
	SkuId         string
	WarehouseId   uint64
	ActorId       uint64
	TransactionId uint64
	TransactionAt time.Time
	ChangeCount   int32
	ChangeAmount  float64
}

func (r *txExpandRow) toStockLog(changeType warehouse_iface.StockChangeType) *warehouse_iface.StockChangeLog {
	return &warehouse_iface.StockChangeLog{
		SkuId:         r.SkuId,
		WarehouseId:   r.WarehouseId,
		ActorId:       r.ActorId,
		TransactionId: r.TransactionId,
		TransactionAt: timestamppb.New(r.TransactionAt),
		ChangeCount:   r.ChangeCount,
		ChangeAmount:  r.ChangeAmount,
		Type:          changeType,
	}
}

// expandTxItems mirrors warehouse CreateStockChangeLog's core expansion: each
// inv_tx_item of the transaction becomes a StockChangeLog carrying the positive
// magnitude of that item's change. For the order/restock/return/cancel variants
// the magnitude is netted against any warehouse problem items recorded on the
// same item (count − coalesce(problem.count, 0)); for a STOCK_PROBLEM the items
// of the dedicated problem transaction already are the magnitude, so no netting
// is applied (matching the warehouse's iti.count branch). The reason's sign is
// applied later by stockChangeLogToInventory, so the magnitude stays positive
// here. Rows that net to zero are skipped.
func expandTxItems(tx *gorm.DB, transactionId uint64, changeType warehouse_iface.StockChangeType) ([]*warehouse_iface.StockChangeLog, error) {
	// A STOCK_PROBLEM transaction's own items are the problem quantities, so the
	// magnitude is iti.count; every other variant nets out problem items booked
	// against the source transaction's items. The value side uses the landed unit
	// cost (item price + allocated per-piece restock fee), mirroring the warehouse.
	netProblems := changeType != warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_STOCK_PROBLEM

	countExpr := "iti.count as change_count"
	amountExpr := "(iti.count * (iti.price + coalesce(rc.per_piece_fee, 0))) as change_amount"
	query := tx.
		Table("inv_tx_items iti").
		Joins("join inv_transactions it on it.id = iti.inv_transaction_id").
		Joins("left join restock_costs rc on rc.inv_transaction_id = iti.inv_transaction_id").
		Where("iti.inv_transaction_id = ?", transactionId)
	if netProblems {
		countExpr = "(iti.count - coalesce(iip.count, 0)) as change_count"
		amountExpr = "((iti.count - coalesce(iip.count, 0)) * (iti.price + coalesce(rc.per_piece_fee, 0))) as change_amount"
		query = query.Joins("left join inv_item_problems iip on iip.tx_item_id = iti.id")
	}

	rows := []*txExpandRow{}
	err := query.
		Select([]string{
			"iti.sku_id as sku_id",
			"it.warehouse_id as warehouse_id",
			"it.create_by_id as actor_id",
			"it.id as transaction_id",
			"it.created as transaction_at",
			countExpr,
			amountExpr,
		}).
		Find(&rows).
		Error
	if err != nil {
		return nil, err
	}

	// Transfer legs where stock leaves the warehouse (OUT, IN_CANCELED) carry a negative
	// magnitude; stockChangeLogToInventory passes transfer counts through caller-signed, so the
	// sign must be baked in here (mirrors warehouse CreateStockChangeLog's n = -1).
	sign := int32(1)
	switch changeType {
	case warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER_WAREHOUSE_OUT,
		warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER_WAREHOUSE_IN_CANCELED:
		sign = -1
	}

	logs := make([]*warehouse_iface.StockChangeLog, 0, len(rows))
	for _, r := range rows {
		if r.ChangeCount == 0 {
			continue
		}
		r.ChangeCount *= sign
		r.ChangeAmount *= float64(sign)
		logs = append(logs, r.toStockLog(changeType))
	}
	return logs, nil
}

// stockChangeLogToInventory converts one warehouse StockChangeLog into an inventory
// StockChange. The warehouse change_count is the signed delta, and the processor
// re-derives the sign from the reason for directional types — so directional
// reasons take abs(change_count) (the reason supplies the sign) while the
// caller-signed reasons (adjustment, transfer) pass the signed value. Either way
// the StockState moves by exactly change_count. Returns (nil, nil) for an unmapped
// change type so a stray log is skipped rather than poisoning the message.
func stockChangeLogToInventory(log *warehouse_iface.StockChangeLog) (*inventory_iface.StockChange, error) {
	change := &inventory_iface.StockChange{
		At:            log.TransactionAt,
		WarehouseId:   log.WarehouseId,
		UserId:        log.ActorId,
		TransactionId: log.TransactionId,
	}

	// directional default: magnitudes (the reason supplies the sign downstream).
	count := int64(math.Abs(float64(log.ChangeCount)))
	value := math.Abs(log.ChangeAmount)
	switch log.Type {
	case warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_ACCEPTED:
		change.Change = &inventory_iface.StockChange_OrderCreated{OrderCreated: &inventory_iface.OrderCreated{}}
	case warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_CANCELED:
		change.Change = &inventory_iface.StockChange_OrderCanceled{OrderCanceled: &inventory_iface.OrderCanceled{}}
	case warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK_ACCEPTED:
		change.Change = &inventory_iface.StockChange_Restock{Restock: &inventory_iface.Restock{}}
	case warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_RETURN_ACCEPTED:
		change.Change = &inventory_iface.StockChange_Return{Return: &inventory_iface.Return{}}
	case warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_STOCK_PROBLEM:
		change.Change = &inventory_iface.StockChange_Problem{Problem: &inventory_iface.Problem{}}
	case warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_STOCK_FOUND_BACK:
		change.Change = &inventory_iface.StockChange_FoundBack{FoundBack: &inventory_iface.FoundBack{}}
	case warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_STOCK_ADJUSTMENT:
		change.Change = &inventory_iface.StockChange_Adjustment{Adjustment: &inventory_iface.Adjustment{}}
		count = int64(log.ChangeCount) // caller-signed
		value = log.ChangeAmount       // caller-signed
	case warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER_WAREHOUSE_OUT,
		warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER_WAREHOUSE_IN,
		warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER_WAREHOUSE_OUT_CANCELED,
		warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER_WAREHOUSE_IN_CANCELED:
		change.Change = &inventory_iface.StockChange_Transfer{Transfer: &inventory_iface.Transfer{}}
		count = int64(log.ChangeCount) // caller-signed (out/in carried by the sign)
		value = log.ChangeAmount       // caller-signed
	default:
		return nil, nil // unmapped type → skip
	}

	sku, err := db_models.SkuID(log.SkuId).Extract()
	if err != nil {
		return nil, err
	}
	change.Changes = []*inventory_iface.ChangeItem{{
		ProductId:    uint64(sku.ProductID),
		ChangeCount:  count,
		ChangeAmount: value,
	}}

	return change, nil
}
