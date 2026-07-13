package inventory

import (
	"context"
	"errors"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	"github.com/pdcgo/inventory_service/inventory_mutations"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TransactionCreate implements [inventory_ifaceconnect.InventoryServiceHandler]. It
// records a request-driven, inventory-owned stock mutation and applies it to
// StockState (+ mints a StockBatch for inbound kinds) in one transaction. The five
// kinds map to the existing StockChange reasons (which supply the +/- direction):
// order/problem decrement; restock/return/found_back increment.
//
// The ORDER kind (see applyOrderOutbound) additionally: values the stock-out at the
// warehouse's AVERAGE COST (ignoring the caller price), hard-rejects insufficient
// stock, and auto-picks racks per ProductConfig as ORDER_CREATED placement logs.
// Other kinds still leave per-rack StockPlacement untouched.
func (s *inventoryServiceImpl) TransactionCreate(
	ctx context.Context,
	req *connect.Request[inventory_iface.TransactionCreateRequest],
) (*connect.Response[inventory_iface.TransactionCreateResponse], error) {
	pay := req.Msg
	if pay.GetWarehouseId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("warehouse_id is required"))
	}

	var (
		txType  inventory_models.InventoryTxType
		items   []*inventory_iface.TransactionItem
		inbound bool
	)
	switch {
	case pay.GetOrder() != nil:
		txType, items, inbound = inventory_models.InvTxOrder, pay.GetOrder().GetItems(), false
	case pay.GetRestock() != nil:
		txType, items, inbound = inventory_models.InvTxRestock, pay.GetRestock().GetItems(), true
	case pay.GetStockReturn() != nil:
		txType, items, inbound = inventory_models.InvTxReturn, pay.GetStockReturn().GetItems(), true
	case pay.GetFoundBack() != nil:
		txType, items, inbound = inventory_models.InvTxFoundBack, pay.GetFoundBack().GetItems(), true
	case pay.GetProblem() != nil:
		txType, items, inbound = inventory_models.InvTxProblem, pay.GetProblem().GetItems(), false
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("tx is required"))
	}
	if len(items) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("items are required"))
	}
	for _, it := range items {
		if it.GetProductId() == 0 || it.GetCount() <= 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("each item needs product_id and count > 0"))
		}
	}

	resp := &inventory_iface.TransactionCreateResponse{}
	now := time.Now()

	err := s.db.
		WithContext(ctx).
		Transaction(func(tx *gorm.DB) error {
			trx := inventory_models.InventoryTransaction{
				TeamID:      pay.GetTeamId(),
				WarehouseID: pay.GetWarehouseId(),
				Type:        txType,
				Status:      inventory_models.InvTxActive,
				CreatedAt:   now,
			}
			err := tx.Create(&trx).Error
			if err != nil {
				return err
			}
			txID := trx.ID

			// The ORDER kind has its own path: average-cost valuation, short-stock
			// hard-reject, and per-rack auto-picking (Flow A).
			if txType == inventory_models.InvTxOrder {
				costItems, err := applyOrderOutbound(tx, txID, pay.GetWarehouseId(), items, now)
				if err != nil {
					return err
				}
				resp.TransactionId = txID
				resp.Items = costItems
				return nil
			}

			changes := make([]*inventory_iface.ChangeItem, 0, len(items))
			costItems := make([]*inventory_iface.TransactionCostItem, 0, len(items))
			for _, it := range items {
				item := inventory_models.InventoryTransactionItem{
					TransactionID: txID,
					ProductID:     it.GetProductId(),
					Count:         it.GetCount(),
					Price:         it.GetPrice(),
				}
				err = tx.Create(&item).Error
				if err != nil {
					return err
				}
				changes = append(changes, &inventory_iface.ChangeItem{
					ProductId:    it.GetProductId(),
					ChangeCount:  it.GetCount(), // magnitude; the reason supplies the sign
					ChangeAmount: float64(it.GetCount()) * it.GetPrice(),
				})
				costItems = append(costItems, &inventory_iface.TransactionCostItem{
					ProductId: it.GetProductId(),
					Count:     it.GetCount(),
					UnitCost:  it.GetPrice(), // non-order kinds echo the caller price
					TotalCost: float64(it.GetCount()) * it.GetPrice(),
				})
			}

			change := &inventory_iface.StockChange{
				At:            timestamppb.New(now),
				WarehouseId:   pay.GetWarehouseId(),
				TransactionId: txID,
				Changes:       changes,
			}
			setStockChangeReason(change, txType)
			_, err = inventory_mutations.NewProcessStockBatchLog(tx)(change)
			if err != nil {
				return err
			}

			if inbound {
				err = mintTransactionBatches(tx, txID, pay.GetWarehouseId(), items, now)
				if err != nil {
					return err
				}
			}

			resp.TransactionId = txID
			resp.Items = costItems
			return nil
		})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}

// setStockChangeReason sets the StockChange reason whose sign the batch-log
// processor applies (order/problem = decrement, the rest = increment). It mutates
// the field because the oneof's interface type is unexported.
func setStockChangeReason(change *inventory_iface.StockChange, t inventory_models.InventoryTxType) {
	switch t {
	case inventory_models.InvTxOrder:
		change.Change = &inventory_iface.StockChange_OrderCreated{OrderCreated: &inventory_iface.OrderCreated{}}
	case inventory_models.InvTxRestock:
		change.Change = &inventory_iface.StockChange_Restock{Restock: &inventory_iface.Restock{}}
	case inventory_models.InvTxReturn:
		change.Change = &inventory_iface.StockChange_Return{Return: &inventory_iface.Return{}}
	case inventory_models.InvTxFoundBack:
		change.Change = &inventory_iface.StockChange_FoundBack{FoundBack: &inventory_iface.FoundBack{}}
	case inventory_models.InvTxProblem:
		change.Change = &inventory_iface.StockChange_Problem{Problem: &inventory_iface.Problem{}}
	}
}

// mintTransactionBatches creates one StockBatch per product for an inbound
// transaction (batch_code = the transaction id), sourced from the request items
// rather than invertory_histories. Idempotent via ON CONFLICT DO NOTHING.
func mintTransactionBatches(
	tx *gorm.DB,
	txID, warehouseID uint64,
	items []*inventory_iface.TransactionItem,
	now time.Time,
) error {
	type agg struct {
		count  int64
		amount float64
	}
	byProduct := map[uint64]*agg{}
	order := []uint64{}
	for _, it := range items {
		a, ok := byProduct[it.GetProductId()]
		if !ok {
			a = &agg{}
			byProduct[it.GetProductId()] = a
			order = append(order, it.GetProductId())
		}
		a.count += it.GetCount()
		a.amount += float64(it.GetCount()) * it.GetPrice()
	}

	code := strconv.FormatUint(txID, 10)
	for _, pid := range order {
		a := byProduct[pid]
		if a.count <= 0 {
			continue
		}
		batch := inventory_models.StockBatch{
			ProductID:   pid,
			WarehouseID: warehouseID,
			InboundID:   txID,
			BatchCode:   code,
			StartCount:  a.count,
			EndCount:    a.count,
			Price:       a.amount / float64(a.count),
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		err := tx.
			Clauses(clause.OnConflict{DoNothing: true}).
			Create(&batch).
			Error
		if err != nil {
			return err
		}
	}
	return nil
}
