package inventory

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// ProductDetail implements [inventory_ifaceconnect.InventoryServiceHandler].
//
// Returns one product's state in a warehouse: name/team, stock (from StockState),
// batch count (open StockBatches) + rack count (distinct StockPlacements with stock),
// and its config (defaults when unset). NotFound if the product isn't tracked there.
func (s *inventoryServiceImpl) ProductDetail(
	ctx context.Context,
	req *connect.Request[inventory_iface.ProductDetailRequest],
) (*connect.Response[inventory_iface.ProductDetailResponse], error) {
	pay := req.Msg
	if pay.GetProductId() == 0 || pay.GetWarehouseId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("product_id and warehouse_id are required"))
	}
	db := s.db.WithContext(ctx)

	var head struct {
		StockReady       int64
		StockReadyAmount float64
		UpdatedAt        time.Time
		Name             string
		TeamID           uint64
		TeamName         string
	}
	res := db.
		Table("stock_states ss").
		Joins("LEFT JOIN products p ON p.id = ss.product_id").
		Joins("LEFT JOIN teams t ON t.id = p.team_id").
		Where("ss.product_id = ? AND ss.warehouse_id = ?", pay.GetProductId(), pay.GetWarehouseId()).
		Select("ss.stock_ready, ss.stock_ready_amount, ss.updated_at, p.name, p.team_id, t.name as team_name").
		Limit(1).
		Find(&head)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("product not tracked in this warehouse"))
	}

	var batchCount int64
	if err := db.Table("stock_batches").
		Where("product_id = ? AND warehouse_id = ? AND end_count > 0", pay.GetProductId(), pay.GetWarehouseId()).
		Count(&batchCount).Error; err != nil {
		return nil, err
	}

	var rackCount int64
	if err := db.Table("stock_placements").
		Where("product_id = ? AND warehouse_id = ? AND count > 0", pay.GetProductId(), pay.GetWarehouseId()).
		Distinct("rack_id").
		Count(&rackCount).Error; err != nil {
		return nil, err
	}

	resp := &inventory_iface.ProductDetailResponse{
		ProductId:        pay.GetProductId(),
		WarehouseId:      pay.GetWarehouseId(),
		Name:             head.Name,
		TeamId:           head.TeamID,
		TeamName:         head.TeamName,
		StockReady:       head.StockReady,
		StockReadyAmount: head.StockReadyAmount,
		BatchCount:       batchCount,
		RackCount:        rackCount,
		QueueType:        inventory_iface.QueueType_QUEUE_TYPE_FIFO,
		PlacementPicking: inventory_iface.PlacementPickingType_PLACEMENT_PICKING_TYPE_SMALLER,
		Configured:       false,
		UpdatedAt:        timestamppb.New(head.UpdatedAt),
	}

	var cfg inventory_models.ProductConfig
	err := db.Where("product_id = ? AND warehouse_id = ?", pay.GetProductId(), pay.GetWarehouseId()).First(&cfg).Error
	if err == nil {
		resp.QueueType = cfg.QueueType
		resp.PlacementPicking = cfg.PlacementPicking
		resp.Configured = true
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// Ongoing (in-transit) inbound/outbound for this product+warehouse — pending docs only
	// (accepted/canceled are already reflected in StockState, so they must NOT count).
	pid := pay.GetProductId()
	wid := pay.GetWarehouseId()
	sumSelect := "COALESCE(SUM(x.count), 0) AS count, COALESCE(SUM(x.count * x.price), 0) AS amount"

	var restockAgg ongoingAgg
	err = db.
		Table("inventory_restock_items x").
		Joins("JOIN inventory_restocks r ON r.id = x.restock_id").
		Where("r.warehouse_id = ? AND x.product_id = ? AND r.status IN ?", wid, pid,
			[]inventory_models.RestockStatus{inventory_models.RestockPending, inventory_models.RestockProblem}).
		Select(sumSelect).
		Scan(&restockAgg).
		Error
	if err != nil {
		return nil, err
	}
	resp.OngoingRestockCount = restockAgg.Count
	resp.OngoingRestockAmount = restockAgg.Amount

	var transferOutAgg ongoingAgg
	err = db.
		Table("inventory_transfer_items x").
		Joins("JOIN inventory_transfers t ON t.id = x.transfer_id").
		Where("t.from_warehouse_id = ? AND x.product_id = ? AND t.status = ?", wid, pid, inventory_models.TransferPending).
		Select(sumSelect).
		Scan(&transferOutAgg).
		Error
	if err != nil {
		return nil, err
	}
	resp.OngoingTransferOutCount = transferOutAgg.Count
	resp.OngoingTransferOutAmount = transferOutAgg.Amount

	var transferInAgg ongoingAgg
	err = db.
		Table("inventory_transfer_items x").
		Joins("JOIN inventory_transfers t ON t.id = x.transfer_id").
		Where("t.to_warehouse_id = ? AND x.product_id = ? AND t.status = ?", wid, pid, inventory_models.TransferPending).
		Select(sumSelect).
		Scan(&transferInAgg).
		Error
	if err != nil {
		return nil, err
	}
	resp.OngoingTransferInCount = transferInAgg.Count
	resp.OngoingTransferInAmount = transferInAgg.Amount

	return connect.NewResponse(resp), nil
}

// ongoingAgg is the count+amount rollup for a set of pending restock/transfer items.
type ongoingAgg struct {
	Count  int64
	Amount float64
}
