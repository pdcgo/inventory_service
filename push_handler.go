package inventory_service

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/pdcgo/event_source"
	"github.com/pdcgo/inventory_service/inventory_mutations"
	"github.com/pdcgo/san_collection/san_config"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	warehouse_iface "github.com/pdcgo/schema/services/warehouse_iface/v1"
	"github.com/pdcgo/shared/db_models"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

type InventoryPushHandler event_source.PushHandler

// NewInventoryPushHandler decodes a pushed StockChange and applies it to stock
// state in one transaction. Unknown subscriptions are acked (no-op).
func NewInventoryPushHandler(
	db *gorm.DB,
	projectCfg *san_config.ProjectConfig,
) InventoryPushHandler {
	return func(ctx context.Context, msg *event_source.PushRequest) error {
		switch msg.Subscription {
		case projectCfg.PubsubSubscriberPath("inventory-stock-sub"):
			var event warehouse_iface.StockEvent
			if err := protojson.Unmarshal(msg.Message.Data, &event); err != nil {
				return err
			}
			return db.Transaction(func(tx *gorm.DB) error {
				return processStockEvent(tx, &event)
			})
		}

		return nil
	}
}

// processStockEvent applies a warehouse StockEvent by expanding the transaction
// it references into per-product stock changes. The rich stock_change variant is
// not published yet, so each handled variant carries only a transaction_id: the
// transaction's items are expanded (inv_tx_items, minus inv_item_problems) into
// per-SKU StockChangeLogs and converted via stockChangeLogToInventory. Deferred
// variants (found_back, adjustment, transfers, stock_change) are no-ops for now.
func processStockEvent(tx *gorm.DB, event *warehouse_iface.StockEvent) error {
	switch data := event.Data.(type) {
	case *warehouse_iface.StockEvent_OrderAccepted:
		return applyTxItems(tx, data.OrderAccepted.TransactionId, warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_ACCEPTED)
	case *warehouse_iface.StockEvent_OrderCanceled:
		return applyTxItems(tx, data.OrderCanceled.TransactionId, warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_ORDER_CANCELED)
	case *warehouse_iface.StockEvent_RestockAccepted:
		return applyTxItems(tx, data.RestockAccepted.TransactionId, warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_RESTOCK_ACCEPTED)
	case *warehouse_iface.StockEvent_ReturnAccepted:
		return applyTxItems(tx, data.ReturnAccepted.TransactionId, warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_RETURN_ACCEPTED)
	case *warehouse_iface.StockEvent_StockProblem:
		return applyTxItems(tx, data.StockProblem.TransactionId, warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_STOCK_PROBLEM)
		// TODO(inventory): StockFoundBack, StockAdjustment, and the transfer
		// variants (TransferWarehouse*) are not handled yet — transfers need a
		// WarehouseTransfer → out/in tx lookup + cross-warehouse handling, and
		// found_back/adjustment semantics are not finalized upstream.
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

// applyStockLogs converts each warehouse StockChangeLog to an inventory
// StockChange and runs it through the stock-batch-log processor.
func applyStockLogs(tx *gorm.DB, logs []*warehouse_iface.StockChangeLog) error {
	proc := inventory_mutations.NewProcessStockBatchLog(tx)
	for _, log := range logs {
		change, err := stockChangeLogToInventory(log)
		if err != nil {
			return err
		}
		if change == nil {
			continue // unmapped type
		}
		if _, err := proc(change); err != nil {
			return err
		}
	}
	return nil
}

// txExpandRow is the projected shape of an expanded transaction item.
type txExpandRow struct {
	SkuId         string
	WarehouseId   uint64
	ActorId       uint64
	TransactionId uint64
	TransactionAt time.Time
	ChangeCount   int32
}

func (r *txExpandRow) toStockLog(changeType warehouse_iface.StockChangeType) *warehouse_iface.StockChangeLog {
	return &warehouse_iface.StockChangeLog{
		SkuId:         r.SkuId,
		WarehouseId:   r.WarehouseId,
		ActorId:       r.ActorId,
		TransactionId: r.TransactionId,
		TransactionAt: timestamppb.New(r.TransactionAt),
		ChangeCount:   r.ChangeCount,
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
	// against the source transaction's items.
	netProblems := changeType != warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_STOCK_PROBLEM

	countExpr := "iti.count as change_count"
	query := tx.
		Table("inv_tx_items iti").
		Joins("join inv_transactions it on it.id = iti.inv_transaction_id").
		Where("iti.inv_transaction_id = ?", transactionId)
	if netProblems {
		countExpr = "(iti.count - coalesce(iip.count, 0)) as change_count"
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
		}).
		Find(&rows).
		Error
	if err != nil {
		return nil, err
	}

	logs := make([]*warehouse_iface.StockChangeLog, 0, len(rows))
	for _, r := range rows {
		if r.ChangeCount == 0 {
			continue
		}
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

	amount := math.Abs(float64(log.ChangeCount)) // directional default: magnitude
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
		amount = float64(log.ChangeCount) // caller-signed
	case warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER_WAREHOUSE_OUT,
		warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER_WAREHOUSE_IN,
		warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER_WAREHOUSE_OUT_CANCELED,
		warehouse_iface.StockChangeType_STOCK_CHANGE_TYPE_TRANSFER_WAREHOUSE_IN_CANCELED:
		change.Change = &inventory_iface.StockChange_Transfer{Transfer: &inventory_iface.Transfer{}}
		amount = float64(log.ChangeCount) // caller-signed (out/in carried by the sign)
	default:
		return nil, nil // unmapped type → skip
	}

	sku, err := db_models.SkuID(log.SkuId).Extract()
	if err != nil {
		return nil, err
	}
	change.Changes = []*inventory_iface.ChangeItem{{
		ProductId: uint64(sku.ProductID),
		Amount:    amount,
	}}

	return change, nil
}

type InventoryPushHttpHandler http.HandlerFunc

func NewInventoryPushHttpHandler(handler InventoryPushHandler) InventoryPushHttpHandler {
	return InventoryPushHttpHandler(event_source.NewMuxPushhandler(event_source.PushHandler(handler)))
}
