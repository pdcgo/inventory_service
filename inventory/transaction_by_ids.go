package inventory

import (
	"context"

	"connectrpc.com/connect"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/db_models"
)

// transactionTypes maps the stored inv_transactions.type strings onto the wire enum.
// A value that is not listed degrades to UNSPECIFIED rather than failing the call.
var transactionTypes = map[db_models.InvTxType]inventory_iface.TransactionType{
	db_models.InvTxRestock:      inventory_iface.TransactionType_TRANSACTION_TYPE_RESTOCK,
	db_models.InvTxAdjRestock:   inventory_iface.TransactionType_TRANSACTION_TYPE_ADJ_RESTOCK,
	db_models.InvTxOrder:        inventory_iface.TransactionType_TRANSACTION_TYPE_ORDER,
	db_models.InvTxReturn:       inventory_iface.TransactionType_TRANSACTION_TYPE_RETURN,
	db_models.InvTxTransferIn:   inventory_iface.TransactionType_TRANSACTION_TYPE_TRANSFER_IN,
	db_models.InvTxTransferOut:  inventory_iface.TransactionType_TRANSACTION_TYPE_TRANSFER_OUT,
	db_models.InvTxTransit:      inventory_iface.TransactionType_TRANSACTION_TYPE_TRANSIT,
	db_models.InvTxBroken:       inventory_iface.TransactionType_TRANSACTION_TYPE_BROKEN,
	db_models.InvTxChangeSkuOut: inventory_iface.TransactionType_TRANSACTION_TYPE_CHANGE_SKU_OUT,
	db_models.InvTxChangeSkuIn:  inventory_iface.TransactionType_TRANSACTION_TYPE_CHANGE_SKU_IN,
	db_models.InvTxAdjIn:        inventory_iface.TransactionType_TRANSACTION_TYPE_ADJ_IN,
	db_models.InvTxAdjout:       inventory_iface.TransactionType_TRANSACTION_TYPE_ADJ_OUT,
	db_models.InvTxSysErrIn:     inventory_iface.TransactionType_TRANSACTION_TYPE_SYS_ERR_IN,
	db_models.InvTxSysErrOut:    inventory_iface.TransactionType_TRANSACTION_TYPE_SYS_ERR_OUT,
}

// transactionStatuses maps the stored inv_transactions.status strings onto the wire
// enum, with the same UNSPECIFIED fallback as transactionTypes.
var transactionStatuses = map[db_models.InvTxStatus]inventory_iface.TransactionStatus{
	db_models.InvWaiting:            inventory_iface.TransactionStatus_TRANSACTION_STATUS_WAITING,
	db_models.InvTxOngoing:          inventory_iface.TransactionStatus_TRANSACTION_STATUS_ONGOING,
	db_models.InvTxCancel:           inventory_iface.TransactionStatus_TRANSACTION_STATUS_CANCEL,
	db_models.InvTxCompleted:        inventory_iface.TransactionStatus_TRANSACTION_STATUS_COMPLETED,
	db_models.InvTxProductPick:      inventory_iface.TransactionStatus_TRANSACTION_STATUS_PICKING,
	db_models.InvTxProductPicked:    inventory_iface.TransactionStatus_TRANSACTION_STATUS_PICKED,
	db_models.InvTxReadyForPacking:  inventory_iface.TransactionStatus_TRANSACTION_STATUS_PACKING,
	db_models.InvTxReadyForCourrier: inventory_iface.TransactionStatus_TRANSACTION_STATUS_PACKING_COMPLETED,
}

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
		Select([]string{"id", "extern_ord_id", "receipt", "type", "status"}).
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
			Type:          transactionTypes[row.Type],
			Status:        transactionStatuses[row.Status],
		}
	}

	return connect.NewResponse(result), nil
}
