package inventory

import (
	"context"

	"connectrpc.com/connect"
	"github.com/pdcgo/schema/services/common/v1"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
)

func (s *inventoryServiceImpl) StockMovementDaily(ctx context.Context, req *connect.Request[inventory_iface.StockMovementDailyRequest]) (*connect.Response[inventory_iface.StockMovementDailyResponse], error) {
	db := s.db.WithContext(ctx)

	result := &inventory_iface.StockMovementDailyResponse{
		Days: []*inventory_iface.DailyMovementItem{},
		PageInfo: &common.PageInfoWithoutCount{
			CurrentPage: req.Msg.Page.Page,
		},
	}

	flow := db.
		Select([]string{
			"(date_trunc('day', s.created_at AT TIME ZONE 'Asia/Jakarta') AT TIME ZONE 'Asia/Jakarta') AS day",
			"(SUM(CASE WHEN s.change > 0 THEN s.change ELSE 0 END))::bigint AS total_in",
			"(-SUM(CASE WHEN s.change < 0 THEN s.change ELSE 0 END))::bigint AS total_out",
			"SUM(CASE WHEN s.change > 0 THEN s.change * s.price ELSE 0 END) AS amount_in",
			"-SUM(CASE WHEN s.change < 0 THEN s.change * s.price ELSE 0 END) AS amount_out",
			"SUM(s.change) AS net_count",
			"SUM(s.change * s.price) AS net_amount",
		}).
		Table("stock_batch_logs s").
		Where("s.product_id = ?", req.Msg.ProductId).
		Group("day")

	opening := db.
		Select([]string{
			"COALESCE(SUM(s.change), 0) AS open_count",
			"COALESCE(SUM(s.change * s.price), 0) AS open_amount",
		}).
		Table("stock_batch_logs s").
		Where("s.product_id = ?", req.Msg.ProductId)

	if req.Msg.WarehouseId != 0 {
		flow = flow.Where("s.warehouse_id = ?", req.Msg.WarehouseId)
		opening = opening.Where("s.warehouse_id = ?", req.Msg.WarehouseId)
	}

	hasStart := false
	if req.Msg.TimeRange != nil {
		if req.Msg.TimeRange.StartDate != nil {
			hasStart = true
			startDate := req.Msg.TimeRange.StartDate.AsTime()
			flow = flow.Where("s.created_at >= ?", startDate)
			opening = opening.Where("s.created_at < ?", startDate)
		}
		if req.Msg.TimeRange.EndDate != nil {
			flow = flow.Where("s.created_at <= ?", req.Msg.TimeRange.EndDate.AsTime())
		}
	}
	if !hasStart {
		opening = opening.Where("FALSE")
	}

	query := db.
		Select([]string{
			"f.day",
			"f.total_in",
			"f.total_out",
			"f.amount_in",
			"f.amount_out",
			"(o.open_count + SUM(f.net_count) OVER (ORDER BY f.day))::bigint AS balance_count",
			"o.open_amount + SUM(f.net_amount) OVER (ORDER BY f.day) AS balance_amount",
		}).
		Table("(?) AS f", flow).
		Joins("CROSS JOIN (?) AS o", opening).
		Order("f.day DESC").
		Limit(int(req.Msg.Page.Limit))

	if req.Msg.Page.Page > 0 {
		query = query.Offset(int((req.Msg.Page.Page - 1) * req.Msg.Page.Limit))
	}

	err := query.Find(&result.Days).Error

	if err != nil {
		return nil, err
	}

	for _, day := range result.Days {
		if day.BalanceCount != 0 {
			day.Price = day.BalanceAmount / float64(day.BalanceCount)
		}
	}

	return connect.NewResponse(result), nil
}
