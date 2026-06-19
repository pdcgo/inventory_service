package inventory

import (
	"context"

	"connectrpc.com/connect"
	"github.com/pdcgo/schema/services/common/v1"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"google.golang.org/protobuf/proto"
)

func (s *inventoryServiceImpl) StockMovement(ctx context.Context, req *connect.Request[inventory_iface.StockMovementRequest]) (*connect.Response[inventory_iface.StockMovementResponse], error) {
	db := s.db.WithContext(ctx)

	result := &inventory_iface.StockMovementResponse{
		Movements: []*inventory_iface.MovementItem{},
		PageInfo: &common.PageInfoWithoutCount{
			CurrentPage: req.Msg.Page.Page,
		},
	}

	query := db.
		Select([]string{
			"s.id",
			"s.change_type",
			"s.change",
			"s.transaction_id",
			"s.product_id",
			"s.warehouse_id",
			"s.user_id",
			"s.created_at",
			"s.balance_count",
		}).
		Table("stock_batch_logs s").
		Limit(int(req.Msg.Page.Limit)).
		Order("created_at DESC").
		Where("s.warehouse_id = ?", req.Msg.WarehouseId).
		Where("s.product_id = ?", req.Msg.ProductId)

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
