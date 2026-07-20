package inventory

import (
	"context"

	"connectrpc.com/connect"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
)

type problemItemRow struct {
	ID          uint64
	TxItemID    uint64
	SkuID       string
	ProblemType string
	ProblemNote string
	Count       int64
}

var problemTypes = map[string]inventory_iface.ProblemType{
	"broken_s": inventory_iface.ProblemType_PROBLEM_TYPE_BROKEN_IN_SHIPPING,
	"lost_s":   inventory_iface.ProblemType_PROBLEM_TYPE_LOST_IN_SHIPPING,
	"diff_s":   inventory_iface.ProblemType_PROBLEM_TYPE_PRODUCT_DIFFERENT,
	"broken_w": inventory_iface.ProblemType_PROBLEM_TYPE_BROKEN_IN_WAREHOUSE,
	"lost_w":   inventory_iface.ProblemType_PROBLEM_TYPE_LOST_IN_WAREHOUSE,
	"disaster": inventory_iface.ProblemType_PROBLEM_TYPE_DISASTER,
	"sample":   inventory_iface.ProblemType_PROBLEM_TYPE_SAMPLE,
}

func (s *inventoryServiceImpl) TransactionProblemItemByTxItemIds(
	ctx context.Context,
	req *connect.Request[inventory_iface.TransactionProblemItemByTxItemIdsRequest],
) (*connect.Response[inventory_iface.TransactionProblemItemByTxItemIdsResponse], error) {
	pay := req.Msg
	db := s.db.WithContext(ctx)

	result := &inventory_iface.TransactionProblemItemByTxItemIdsResponse{
		Items: map[uint64]*inventory_iface.TransactionProblemItemDetail{},
	}
	if len(pay.TxItemIds) == 0 {
		return connect.NewResponse(result), nil
	}

	var rows []problemItemRow
	err := db.
		Table("inv_item_problems").
		Select([]string{"id", "tx_item_id", "sku_id", "problem_type", "problem_note", "count"}).
		Where("tx_item_id IN ?", pay.TxItemIds).
		Order("id ASC").
		Scan(&rows).
		Error
	if err != nil {
		return nil, err
	}

	for i := range rows {
		row := rows[i]
		result.Items[row.TxItemID] = &inventory_iface.TransactionProblemItemDetail{
			Id:          row.ID,
			TxItemId:    row.TxItemID,
			SkuId:       row.SkuID,
			ProblemType: problemTypes[row.ProblemType],
			ProblemNote: row.ProblemNote,
			Count:       row.Count,
		}
	}

	return connect.NewResponse(result), nil
}
