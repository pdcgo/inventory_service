package inventory

import (
	"context"

	"connectrpc.com/connect"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/db_models"
)

// TransactionByIds implements [inventory_ifaceconnect.InventoryServiceHandler]. It
// bulk-loads non-deleted inventory transactions by id, keyed by id; missing/deleted ids
// are omitted. Any authenticated caller (enforced by the request policy).
func (s *inventoryServiceImpl) TransactionByIds(
	ctx context.Context,
	req *connect.Request[inventory_iface.TransactionByIdsRequest],
) (*connect.Response[inventory_iface.TransactionByIdsResponse], error) {
	pay := req.Msg
	db := s.db.WithContext(ctx)

	result := &inventory_iface.TransactionByIdsResponse{
		Transactions: map[uint64]*inventory_iface.TransactionDetail{},
	}
	if len(pay.Ids) == 0 {
		return connect.NewResponse(result), nil
	}

	var rows []*db_models.InvTransaction
	err := db.
		Model(&db_models.InvTransaction{}).
		Select([]string{"id", "extern_ord_id", "receipt"}).
		Where("deleted = ?", false).
		Where("id IN ?", pay.Ids).
		Find(&rows).
		Error
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		id := uint64(row.ID)
		result.Transactions[id] = &inventory_iface.TransactionDetail{
			Id:            id,
			ExternOrderId: row.ExternOrdID,
			Receipt:       row.Receipt,
		}
	}

	return connect.NewResponse(result), nil
}
