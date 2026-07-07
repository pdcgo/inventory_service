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

// TransferDetail implements [inventory_ifaceconnect.InventoryServiceHandler]. It
// returns the transfer document header (with the from/to warehouse names) plus its
// line items with product names.
func (s *inventoryServiceImpl) TransferDetail(
	ctx context.Context,
	req *connect.Request[inventory_iface.TransferDetailRequest],
) (*connect.Response[inventory_iface.TransferDetailResponse], error) {
	pay := req.Msg
	if pay.GetTransferId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("transfer_id is required"))
	}
	db := s.db.WithContext(ctx)

	q := db.Where("id = ?", pay.GetTransferId())
	if pay.GetWarehouseId() > 0 {
		q = q.Where("(from_warehouse_id = ? OR to_warehouse_id = ?)", pay.GetWarehouseId(), pay.GetWarehouseId())
	}
	var transfer inventory_models.InventoryTransfer
	err := q.First(&transfer).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("transfer not found"))
		}
		return nil, err
	}

	var names struct {
		FromName string
		ToName   string
	}
	err = db.
		Table("inventory_transfers t").
		Joins("LEFT JOIN warehouses wf ON wf.id = t.from_warehouse_id").
		Joins("LEFT JOIN warehouses wt ON wt.id = t.to_warehouse_id").
		Select("coalesce(wf.name, '') as from_name, coalesce(wt.name, '') as to_name").
		Where("t.id = ?", transfer.ID).
		Scan(&names).
		Error
	if err != nil {
		return nil, err
	}

	var rows []struct {
		ProductID   uint64
		ProductName string
		Count       int64
		Price       float64
	}
	err = db.
		Table("inventory_transfer_items ti").
		Joins("LEFT JOIN products p ON p.id = ti.product_id").
		Select("ti.product_id as product_id, coalesce(p.name, '') as product_name, ti.count as count, ti.price as price").
		Where("ti.transfer_id = ?", transfer.ID).
		Order("ti.id ASC").
		Scan(&rows).
		Error
	if err != nil {
		return nil, err
	}

	resp := &inventory_iface.TransferDetailResponse{
		Id:                transfer.ID,
		TeamId:            transfer.TeamID,
		FromWarehouseId:   transfer.FromWarehouseID,
		FromWarehouseName: names.FromName,
		ToWarehouseId:     transfer.ToWarehouseID,
		ToWarehouseName:   names.ToName,
		Note:              transfer.Note,
		Status:            transferStatusToProto(transfer.Status),
		OutTransactionId:  transfer.OutTransactionID,
		InTransactionId:   transfer.InTransactionID,
		CreatedById:       transfer.CreatedByID,
		CreatedAt:         timestamppb.New(transfer.CreatedAt),
		Items:             make([]*inventory_iface.TransferDetailItem, 0, len(rows)),
	}
	if transfer.AcceptedAt != nil {
		resp.AcceptedAt = timestamppb.New(*transfer.AcceptedAt)
	}
	if transfer.CanceledAt != nil {
		resp.CanceledAt = timestamppb.New(*transfer.CanceledAt)
	}
	for _, r := range rows {
		total := float64(r.Count) * r.Price
		resp.ItemCount += r.Count
		resp.Amount += total
		resp.Items = append(resp.Items, &inventory_iface.TransferDetailItem{
			ProductId:   r.ProductID,
			ProductName: r.ProductName,
			Count:       r.Count,
			Price:       r.Price,
			Total:       total,
		})
	}

	return connect.NewResponse(resp), nil
}
