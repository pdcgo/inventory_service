package inventory

import (
	"context"

	"connectrpc.com/connect"
	"github.com/pdcgo/schema/services/common/v1"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

func (s *inventoryServiceImpl) StockMovementSelling(ctx context.Context, req *connect.Request[inventory_iface.StockMovementSellingRequest]) (*connect.Response[inventory_iface.StockMovementSellingResponse], error) {
	db := s.db.WithContext(ctx)

	result := &inventory_iface.StockMovementSellingResponse{
		Movements: []*inventory_iface.MovementItem{},
		PageInfo: &common.PageInfoWithoutCount{
			CurrentPage: req.Msg.Page.Page,
		},
	}

	var query *gorm.DB
	if req.Msg.WarehouseId != 0 {
		query = db.
			Select([]string{
				"p.id",
				"p.change_type",
				"p.change",
				"p.transaction_id",
				"p.product_id",
				"p.warehouse_id",
				"p.user_id",
				"p.created_at",
				"p.price",
				"p.balance_count",
				"p.balance_amount",
			}).
			Table("stock_batch_logs p").
			Where("p.product_id = ?", req.Msg.ProductId).
			Where("p.warehouse_id = ?", req.Msg.WarehouseId)
	} else {
		running := db.
			Select([]string{
				"s.id",
				"s.change_type",
				"s.change",
				"s.transaction_id",
				"s.product_id",
				"s.warehouse_id",
				"s.user_id",
				"s.created_at",
				"s.price",
				"(SUM(s.change) OVER (ORDER BY s.id))::bigint AS balance_count",
				"SUM(s.change * s.price) OVER (ORDER BY s.id) AS balance_amount",
			}).
			Table("stock_batch_logs s").
			Where("s.product_id = ?", req.Msg.ProductId)

		query = db.
			Select("p.*").
			Table("(?) AS p", running)
	}

	if req.Msg.TimeRange != nil {
		if req.Msg.TimeRange.StartDate != nil {
			query = query.Where("p.created_at >= ?", req.Msg.TimeRange.StartDate.AsTime())
		}
		if req.Msg.TimeRange.EndDate != nil {
			query = query.Where("p.created_at <= ?", req.Msg.TimeRange.EndDate.AsTime())
		}
	}

	query = query.
		Order("p.id DESC").
		Limit(int(req.Msg.Page.Limit))

	if req.Msg.Page.Page > 0 {
		query = query.Offset(int((req.Msg.Page.Page - 1) * req.Msg.Page.Limit))
	}

	err := query.Find(&result.Movements).Error

	if err != nil {
		return nil, err
	}

	trxInfoMap := make(map[uint64]*inventory_iface.MovementTransactionInfo)
	txIds := []uint64{}

	for _, log := range result.Movements {
		if trxInfoMap[log.TransactionId] == nil {
			trxInfoMap[log.TransactionId] = &inventory_iface.MovementTransactionInfo{}
			txIds = append(txIds, log.TransactionId)
		}

		log.TransactionInfo = trxInfoMap[log.TransactionId]
	}

	invTrxList := []*inventory_iface.MovementTransactionInfo{}

	err = db.
		Select([]string{
			"i.id as transaction_id",
			"i.receipt",
			"t.name as team_name",
			"t.team_code as team_code",
			"u.username as user_username",
			"u.name as user_name",
		}).
		Table("inv_transactions i").
		Joins("LEFT JOIN teams t ON t.id = i.team_id").
		Joins("LEFT JOIN users u ON u.id = i.create_by_id").
		Where("i.id IN ?", txIds).
		Find(&invTrxList).
		Error

	if err != nil {
		return nil, err
	}

	for _, trxInfo := range invTrxList {
		if trxInfoMap[trxInfo.TransactionId] != nil {
			proto.Merge(trxInfoMap[trxInfo.TransactionId], trxInfo)
		}
	}

	return connect.NewResponse(result), nil
}
