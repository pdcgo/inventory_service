package inventory

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	common "github.com/pdcgo/schema/services/common/v1"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/db_connect"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// ProductPlacementLog implements [inventory_ifaceconnect.InventoryServiceHandler].
// It returns the placement change history of a product within a warehouse, most
// recent first, optionally filtered to a single rack.
func (s *inventoryServiceImpl) ProductPlacementLog(
	ctx context.Context,
	req *connect.Request[inventory_iface.ProductPlacementLogRequest],
) (*connect.Response[inventory_iface.ProductPlacementLogResponse], error) {
	pay := req.Msg
	if pay.Page == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("page is required"))
	}

	result := &inventory_iface.ProductPlacementLogResponse{
		Logs:     []*inventory_iface.ProductPlacementLogItem{},
		PageInfo: &common.PageInfo{},
	}
	db := s.db.WithContext(ctx)

	var rows []*inventory_models.StockPlacementLog
	paginated, pageInfo, err := db_connect.SetPaginationQuery(db, func() (*gorm.DB, error) {
		return db.Model(&inventory_models.StockPlacementLog{}).Scopes(func(d *gorm.DB) *gorm.DB {
			d = d.Where("product_id = ? AND warehouse_id = ?", pay.ProductId, pay.WarehouseId)
			if pay.RackId > 0 {
				d = d.Where("rack_id = ?", pay.RackId)
			}
			return d
		}), nil
	}, pay.Page)
	if err != nil {
		return nil, err
	}
	if err := paginated.Order("id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}

	result.PageInfo = pageInfo
	for _, r := range rows {
		if r == nil {
			continue
		}
		result.Logs = append(result.Logs, &inventory_iface.ProductPlacementLogItem{
			Id:            r.ID,
			ProductId:     r.ProductID,
			WarehouseId:   r.WarehouseID,
			RackId:        r.RackID,
			TransactionId: r.TransactionID,
			UserId:        r.UserID,
			ChangeType:    r.ChangeType,
			Change:        r.Change,
			BalanceCount:  r.BalanceCount,
			CreatedAt:     timestamppb.New(r.CreatedAt),
		})
	}

	return connect.NewResponse(result), nil
}
