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

	perWarehouse := db.
		Select([]string{
			"(date_trunc('day', s.created_at AT TIME ZONE 'Asia/Jakarta') AT TIME ZONE 'Asia/Jakarta') AS day",
			"s.warehouse_id AS warehouse_id",
			"SUM(CASE WHEN s.change > 0 THEN s.change ELSE 0 END) AS total_in",
			"-SUM(CASE WHEN s.change < 0 THEN s.change ELSE 0 END) AS total_out",
			"SUM(CASE WHEN s.change > 0 THEN s.change * s.price ELSE 0 END) AS amount_in",
			"-SUM(CASE WHEN s.change < 0 THEN s.change * s.price ELSE 0 END) AS amount_out",
			"(array_agg(s.balance_count ORDER BY s.id DESC))[1] AS balance_count",
			"(array_agg(s.balance_amount ORDER BY s.id DESC))[1] AS balance_amount",
		}).
		Table("stock_batch_logs s").
		Where("s.product_id = ?", req.Msg.ProductId).
		Group("day").
		Group("s.warehouse_id")

	if req.Msg.WarehouseId != 0 {
		perWarehouse = perWarehouse.Where("s.warehouse_id = ?", req.Msg.WarehouseId)
	}

	if req.Msg.TimeRange != nil {
		if req.Msg.TimeRange.StartDate != nil {
			perWarehouse = perWarehouse.Where("s.created_at >= ?", req.Msg.TimeRange.StartDate.AsTime())
		}
		if req.Msg.TimeRange.EndDate != nil {
			perWarehouse = perWarehouse.Where("s.created_at <= ?", req.Msg.TimeRange.EndDate.AsTime())
		}
	}

	query := db.
		Select([]string{
			"d.day",
			"SUM(d.total_in) AS total_in",
			"SUM(d.total_out) AS total_out",
			"SUM(d.amount_in) AS amount_in",
			"SUM(d.amount_out) AS amount_out",
			"SUM(d.balance_count) AS balance_count",
			"SUM(d.balance_amount) AS balance_amount",
		}).
		Table("(?) AS d", perWarehouse).
		Group("d.day").
		Order("d.day DESC").
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
