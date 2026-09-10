package inventory

import (
	"context"

	"connectrpc.com/connect"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
)

func (s *inventoryServiceImpl) StockMovementBreakdown(ctx context.Context, req *connect.Request[inventory_iface.StockMovementBreakdownRequest]) (*connect.Response[inventory_iface.StockMovementBreakdownResponse], error) {
	db := s.db.WithContext(ctx)

	result := &inventory_iface.StockMovementBreakdownResponse{
		Breakdowns: []*inventory_iface.MovementBreakdownItem{},
	}

	query := db.
		Select([]string{
			"s.change_type AS change_type",
			"SUM(s.change) AS change_count",
			"SUM(s.change * s.price) AS change_amount",
			"COUNT(*) AS transaction_count",
		}).
		Table("stock_batch_logs s").
		Where("s.product_id = ?", req.Msg.ProductId).
		Group("s.change_type").
		Order("s.change_type ASC")

	if req.Msg.WarehouseId != 0 {
		query = query.Where("s.warehouse_id = ?", req.Msg.WarehouseId)
	}

	if req.Msg.TimeRange != nil {
		if req.Msg.TimeRange.StartDate != nil {
			query = query.Where("s.created_at >= ?", req.Msg.TimeRange.StartDate.AsTime())
		}
		if req.Msg.TimeRange.EndDate != nil {
			query = query.Where("s.created_at <= ?", req.Msg.TimeRange.EndDate.AsTime())
		}
	}

	err := query.Find(&result.Breakdowns).Error

	if err != nil {
		return nil, err
	}

	return connect.NewResponse(result), nil
}
