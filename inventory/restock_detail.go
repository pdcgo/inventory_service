package inventory

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// RestockDetail implements [inventory_ifaceconnect.InventoryServiceHandler]. It
// returns the restock document header plus its line items with product names
// (resolved from the legacy products table, as ProductList does).
func (s *inventoryServiceImpl) RestockDetail(
	ctx context.Context,
	req *connect.Request[inventory_iface.RestockDetailRequest],
) (*connect.Response[inventory_iface.RestockDetailResponse], error) {
	pay := req.Msg
	if pay.GetRestockId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("restock_id is required"))
	}
	db := s.db.WithContext(ctx)

	q := db.Where("id = ?", pay.GetRestockId())
	if pay.GetWarehouseId() > 0 {
		q = q.Where("warehouse_id = ?", pay.GetWarehouseId())
	}
	var restock inventory_models.InventoryRestock
	err := q.First(&restock).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("restock not found"))
		}
		return nil, err
	}

	var rows []struct {
		ProductID    uint64
		ProductName  string
		Count        int64
		Price        float64
		ProblemCount int64
		ProblemNote  string
	}
	err = db.
		Table("inventory_restock_items ri").
		Joins("LEFT JOIN products p ON p.id = ri.product_id").
		Select("ri.product_id as product_id, coalesce(p.name, '') as product_name, ri.count as count, ri.price as price, ri.problem_count as problem_count, ri.problem_note as problem_note").
		Where("ri.restock_id = ?", restock.ID).
		Order("ri.id ASC").
		Scan(&rows).
		Error
	if err != nil {
		return nil, err
	}

	resp := &inventory_iface.RestockDetailResponse{
		Id:                 restock.ID,
		TeamId:             restock.TeamID,
		WarehouseId:        restock.WarehouseID,
		Supplier:           restock.Supplier,
		Receipt:            restock.Receipt,
		Note:               restock.Note,
		ExternOrderId:      restock.ExternOrderID,
		ShippingId:         restock.ShippingID,
		ShippingCost:       restock.ShippingCost,
		PaymentType:        paymentTypeToProto(restock.PaymentType),
		Status:             restockStatusToProto(restock.Status),
		CreatedById:        restock.CreatedByID,
		CreatedAt:          timestamppb.New(restock.CreatedAt),
		WarehouseAcceptFee: restock.WarehouseAcceptFee,
		Items:              make([]*inventory_iface.RestockDetailItem, 0, len(rows)),
	}
	if restock.InventoryTransactionID != nil {
		resp.InventoryTransactionId = *restock.InventoryTransactionID
	}
	if restock.AcceptedAt != nil {
		resp.AcceptedAt = timestamppb.New(*restock.AcceptedAt)
	}
	if restock.CanceledAt != nil {
		resp.CanceledAt = timestamppb.New(*restock.CanceledAt)
	}
	for _, r := range rows {
		total := float64(r.Count) * r.Price
		resp.ItemCount += r.Count
		resp.Amount += total
		resp.Items = append(resp.Items, &inventory_iface.RestockDetailItem{
			ProductId:    r.ProductID,
			ProductName:  r.ProductName,
			Count:        r.Count,
			Price:        r.Price,
			Total:        total,
			ProblemCount: r.ProblemCount,
			ProblemNote:  r.ProblemNote,
		})
	}

	return connect.NewResponse(resp), nil
}
