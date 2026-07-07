package inventory

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	"github.com/pdcgo/inventory_service/inventory_mutations"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// TransferCreate implements [inventory_ifaceconnect.InventoryServiceHandler]. It
// records a warehouse-to-warehouse transfer and applies its OUT leg at the source:
// stock exits the source warehouse immediately (in transit) and re-enters only when
// the destination accepts (TransferAccept). Item prices are derived from the source
// StockState average so value is conserved across warehouses.
func (s *inventoryServiceImpl) TransferCreate(
	ctx context.Context,
	req *connect.Request[inventory_iface.TransferCreateRequest],
) (*connect.Response[inventory_iface.TransferCreateResponse], error) {
	pay := req.Msg
	if pay.GetFromWarehouseId() == 0 || pay.GetToWarehouseId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("from_warehouse_id and to_warehouse_id are required"))
	}
	if pay.GetFromWarehouseId() == pay.GetToWarehouseId() {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("from and to warehouse must differ"))
	}
	if len(pay.GetItems()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("items are required"))
	}
	for _, it := range pay.GetItems() {
		if it.GetProductId() == 0 || it.GetCount() <= 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("each item needs product_id and count > 0"))
		}
	}

	resp := &inventory_iface.TransferCreateResponse{}
	now := time.Now()

	err := s.db.
		WithContext(ctx).
		Transaction(func(tx *gorm.DB) error {
			// Derive the per-unit value each product leaves the source at.
			prices, err := sourceAveragePrices(tx, pay.GetFromWarehouseId(), pay.GetItems())
			if err != nil {
				return err
			}

			transfer := inventory_models.InventoryTransfer{
				TeamID:          pay.GetTeamId(),
				FromWarehouseID: pay.GetFromWarehouseId(),
				ToWarehouseID:   pay.GetToWarehouseId(),
				Note:            pay.GetNote(),
				Status:          inventory_models.TransferPending,
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			err = tx.Create(&transfer).Error
			if err != nil {
				return err
			}

			items := make([]*inventory_iface.TransactionItem, 0, len(pay.GetItems()))
			for _, it := range pay.GetItems() {
				row := inventory_models.InventoryTransferItem{
					TransferID: transfer.ID,
					ProductID:  it.GetProductId(),
					Count:      it.GetCount(),
					Price:      prices[it.GetProductId()],
				}
				err = tx.Create(&row).Error
				if err != nil {
					return err
				}
				items = append(items, &inventory_iface.TransactionItem{
					ProductId: row.ProductID,
					Count:     row.Count,
					Price:     row.Price,
				})
			}

			// OUT leg: stock exits the source (Transfer reason is caller-signed).
			outTxID, err := applyTransferLeg(tx, inventory_models.InvTxTransferOut,
				transfer.TeamID, pay.GetFromWarehouseId(), items, -1, now)
			if err != nil {
				return err
			}

			err = tx.
				Model(&inventory_models.InventoryTransfer{}).
				Where("id = ?", transfer.ID).
				Update("out_transaction_id", outTxID).
				Error
			if err != nil {
				return err
			}

			resp.TransferId = transfer.ID
			return nil
		})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}

// sourceAveragePrices resolves the per-unit value of each requested product from the
// source warehouse's StockState average (stock_ready_amount / stock_ready), 0 when the
// product has no (or non-positive) stock there.
func sourceAveragePrices(tx *gorm.DB, warehouseID uint64, items []*inventory_iface.TransferItem) (map[uint64]float64, error) {
	ids := make([]uint64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.GetProductId())
	}

	var rows []struct {
		ProductID        uint64
		StockReady       int64
		StockReadyAmount float64
	}
	err := tx.
		Table("stock_states").
		Select("product_id, stock_ready, stock_ready_amount").
		Where("warehouse_id = ? AND product_id IN ?", warehouseID, ids).
		Scan(&rows).
		Error
	if err != nil {
		return nil, err
	}

	prices := map[uint64]float64{}
	for _, r := range rows {
		if r.StockReady > 0 {
			prices[r.ProductID] = r.StockReadyAmount / float64(r.StockReady)
		}
	}
	return prices, nil
}

// applyTransferLeg mints one leg of a transfer: an InventoryTransaction (+ items) and
// the caller-signed Transfer StockChange applied through the batch-log engine.
// sign = -1 for the OUT leg at the source, +1 for the IN leg at the destination.
func applyTransferLeg(
	tx *gorm.DB,
	txType inventory_models.InventoryTxType,
	teamID, warehouseID uint64,
	items []*inventory_iface.TransactionItem,
	sign int64,
	now time.Time,
) (uint64, error) {
	trx := inventory_models.InventoryTransaction{
		TeamID:      teamID,
		WarehouseID: warehouseID,
		Type:        txType,
		Status:      inventory_models.InvTxActive,
		CreatedAt:   now,
	}
	err := tx.Create(&trx).Error
	if err != nil {
		return 0, err
	}

	changes := make([]*inventory_iface.ChangeItem, 0, len(items))
	for _, it := range items {
		row := inventory_models.InventoryTransactionItem{
			TransactionID: trx.ID,
			ProductID:     it.GetProductId(),
			Count:         it.GetCount(),
			Price:         it.GetPrice(),
		}
		err = tx.Create(&row).Error
		if err != nil {
			return 0, err
		}
		changes = append(changes, &inventory_iface.ChangeItem{
			ProductId:    it.GetProductId(),
			ChangeCount:  sign * it.GetCount(), // Transfer is caller-signed
			ChangeAmount: float64(sign) * float64(it.GetCount()) * it.GetPrice(),
		})
	}

	change := &inventory_iface.StockChange{
		At:            timestamppb.New(now),
		WarehouseId:   warehouseID,
		TransactionId: trx.ID,
		Changes:       changes,
		Change:        &inventory_iface.StockChange_Transfer{Transfer: &inventory_iface.Transfer{}},
	}
	_, err = inventory_mutations.NewProcessStockBatchLog(tx)(change)
	if err != nil {
		return 0, err
	}

	return trx.ID, nil
}
