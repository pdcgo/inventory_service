package inventory

import (
	"context"

	"connectrpc.com/connect"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/db_models"
)

// TransactionItemByIds implements [inventory_ifaceconnect.InventoryServiceHandler]. It
// bulk-loads transaction items by id, keyed by id; missing ids are omitted. Any
// authenticated caller (enforced by the request policy).
func (s *inventoryServiceImpl) TransactionItemByIds(
	ctx context.Context,
	req *connect.Request[inventory_iface.TransactionItemByIdsRequest],
) (*connect.Response[inventory_iface.TransactionItemByIdsResponse], error) {
	pay := req.Msg
	db := s.db.WithContext(ctx)

	result := &inventory_iface.TransactionItemByIdsResponse{
		Items: map[uint64]*inventory_iface.TransactionItemDetail{},
	}
	if len(pay.Ids) == 0 {
		return connect.NewResponse(result), nil
	}

	var rows []*db_models.InvTxItem
	err := db.
		Model(&db_models.InvTxItem{}).
		Select([]string{"id", "inv_transaction_id", "sku_id", "count", "price", "total"}).
		Where("id IN ?", pay.Ids).
		Find(&rows).
		Error
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		id := uint64(row.ID)
		result.Items[id] = &inventory_iface.TransactionItemDetail{
			Id:               id,
			InvTransactionId: uint64(row.InvTransactionID),
			SkuId:            string(row.SkuID),
			Count:            int64(row.Count),
			Price:            row.Price,
			Total:            row.Total,
		}
	}

	return connect.NewResponse(result), nil
}
