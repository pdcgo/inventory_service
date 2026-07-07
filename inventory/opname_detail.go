package inventory

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// OpnameDetail implements [inventory_ifaceconnect.InventoryServiceHandler]. It returns
// the session header + every (rack, product) counting line with rack and product names
// joined server-side (raw ids are never shown in the UI).
func (s *inventoryServiceImpl) OpnameDetail(
	ctx context.Context,
	req *connect.Request[inventory_iface.OpnameDetailRequest],
) (*connect.Response[inventory_iface.OpnameDetailResponse], error) {
	pay := req.Msg
	if pay.GetOpnameId() == 0 || pay.GetWarehouseId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("opname_id and warehouse_id are required"))
	}
	db := s.db.WithContext(ctx)

	opname, err := fetchOpname(db, pay.GetOpnameId(), pay.GetWarehouseId())
	if err != nil {
		return nil, err
	}

	resp := &inventory_iface.OpnameDetailResponse{
		Id:          opname.ID,
		TeamId:      opname.TeamID,
		WarehouseId: opname.WarehouseID,
		Name:        opname.Name,
		Status:      opnameStatusToProto(opname.Status),
		CreatedById: opname.CreatedByID,
		CreatedAt:   timestamppb.New(opname.CreatedAt),
		Lines:       []*inventory_iface.OpnameLineItem{},
	}
	if opname.InventoryTransactionID != nil {
		resp.InventoryTransactionId = *opname.InventoryTransactionID
	}
	if opname.CompletedAt != nil {
		resp.CompletedById = opname.CompletedByID
		resp.CompletedAt = timestamppb.New(*opname.CompletedAt)
	}
	if opname.CanceledAt != nil {
		resp.CanceledAt = timestamppb.New(*opname.CanceledAt)
	}

	var rows []struct {
		ID            uint64
		RackID        uint64
		RackName      string
		ProductID     uint64
		ProductName   string
		ExpectedCount int64
		CountedCount  int64
		Counted       bool
		Reason        string
		Note          string
	}
	err = db.
		Table("inventory_opname_lines l").
		Joins("LEFT JOIN racks r ON r.id = l.rack_id").
		Joins("LEFT JOIN products p ON p.id = l.product_id").
		Where("l.opname_id = ?", opname.ID).
		Select(`l.id, l.rack_id, COALESCE(r.name, '') as rack_name,
			l.product_id, COALESCE(p.name, '') as product_name,
			l.expected_count, l.counted_count, l.counted, l.reason, l.note`).
		Order("l.rack_id ASC, l.product_id ASC").
		Scan(&rows).
		Error
	if err != nil {
		return nil, err
	}

	for _, r := range rows {
		resp.Lines = append(resp.Lines, &inventory_iface.OpnameLineItem{
			Id:            r.ID,
			RackId:        r.RackID,
			RackName:      r.RackName,
			ProductId:     r.ProductID,
			ProductName:   r.ProductName,
			ExpectedCount: r.ExpectedCount,
			CountedCount:  r.CountedCount,
			Counted:       r.Counted,
			Reason:        opnameReasonToProto(r.Reason),
			Note:          r.Note,
		})
	}

	return connect.NewResponse(resp), nil
}
