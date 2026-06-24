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

// ProductBatchList implements [inventory_ifaceconnect.InventoryServiceHandler]. It
// returns the stock batches of a product within a warehouse, most recent first.
func (s *inventoryServiceImpl) ProductBatchList(
	ctx context.Context,
	req *connect.Request[inventory_iface.ProductBatchListRequest],
) (*connect.Response[inventory_iface.ProductBatchListResponse], error) {
	pay := req.Msg
	if pay.Page == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("page is required"))
	}

	result := &inventory_iface.ProductBatchListResponse{
		Batches:  []*inventory_iface.ProductBatchItem{},
		PageInfo: &common.PageInfo{},
	}
	db := s.db.WithContext(ctx)

	var rows []*inventory_models.StockBatch
	paginated, pageInfo, err := db_connect.SetPaginationQuery(db, func() (*gorm.DB, error) {
		return db.Model(&inventory_models.StockBatch{}).Scopes(func(d *gorm.DB) *gorm.DB {
			return d.Where("product_id = ? AND warehouse_id = ?", pay.ProductId, pay.WarehouseId)
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
		result.Batches = append(result.Batches, &inventory_iface.ProductBatchItem{
			Id:          r.ID,
			ProductId:   r.ProductID,
			WarehouseId: r.WarehouseID,
			InboundId:   r.InboundID,
			StartCount:  r.StartCount,
			EndCount:    r.EndCount,
			Price:       r.Price,
			CreatedAt:   timestamppb.New(r.CreatedAt),
			UpdatedAt:   timestamppb.New(r.UpdatedAt),
		})
	}

	return connect.NewResponse(result), nil
}
