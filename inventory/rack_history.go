package inventory

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	common "github.com/pdcgo/schema/services/common/v1"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/db_connect"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// RackHistory implements [inventory_ifaceconnect.InventoryServiceHandler]. It powers the
// Rack Detail "Histories" tab: the rack's placement-change log across EVERY product (with
// product names), most recent first, optionally within a created_at window (time_range is
// epoch microseconds; zero start/end = unbounded side).
func (s *inventoryServiceImpl) RackHistory(
	ctx context.Context,
	req *connect.Request[inventory_iface.RackHistoryRequest],
) (*connect.Response[inventory_iface.RackHistoryResponse], error) {
	pay := req.Msg
	if pay.Page == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("page is required"))
	}

	result := &inventory_iface.RackHistoryResponse{
		Items:    []*inventory_iface.RackHistoryItem{},
		PageInfo: &common.PageInfo{},
	}
	db := s.db.WithContext(ctx)

	var rows []struct {
		ID            uint64
		ProductID     uint64
		ProductName   string
		ChangeType    inventory_iface.StockChangeType
		Change        int64
		BalanceCount  int64
		UserID        uint64
		TransactionID uint64
		Note          string
		CreatedAt     time.Time
	}
	paginated, pageInfo, err := db_connect.SetPaginationQuery(db, func() (*gorm.DB, error) {
		q := db.
			Table("stock_placement_logs spl").
			Joins("LEFT JOIN products p ON p.id = spl.product_id").
			Where("spl.rack_id = ? AND spl.warehouse_id = ?", pay.RackId, pay.WarehouseId)
		if pay.TimeRange != nil {
			if pay.TimeRange.StartDate > 0 {
				q = q.Where("spl.created_at >= ?", time.UnixMicro(pay.TimeRange.StartDate).Local())
			}
			if pay.TimeRange.EndDate > 0 {
				q = q.Where("spl.created_at <= ?", time.UnixMicro(pay.TimeRange.EndDate).Local())
			}
		}
		return q.Select(
			"spl.id as id, spl.product_id as product_id, COALESCE(p.name, '') as product_name, " +
				"spl.change_type as change_type, spl.change as change, spl.balance_count as balance_count, " +
				"spl.user_id as user_id, spl.transaction_id as transaction_id, spl.note as note, " +
				"spl.created_at as created_at",
		), nil
	}, pay.Page)
	if err != nil {
		return nil, err
	}

	err = paginated.
		Order("spl.id DESC").
		Scan(&rows).
		Error
	if err != nil {
		return nil, err
	}

	result.PageInfo = pageInfo
	for _, r := range rows {
		result.Items = append(result.Items, &inventory_iface.RackHistoryItem{
			Id:            r.ID,
			ProductId:     r.ProductID,
			ProductName:   r.ProductName,
			ChangeType:    r.ChangeType,
			Change:        r.Change,
			BalanceCount:  r.BalanceCount,
			UserId:        r.UserID,
			TransactionId: r.TransactionID,
			Note:          r.Note,
			CreatedAt:     timestamppb.New(r.CreatedAt),
		})
	}

	return connect.NewResponse(result), nil
}
