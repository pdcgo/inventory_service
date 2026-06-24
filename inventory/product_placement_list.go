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

// ProductPlacementList implements [inventory_ifaceconnect.InventoryServiceHandler].
// It returns the per-rack placement of a product within a warehouse, optionally
// filtered to a single rack.
func (s *inventoryServiceImpl) ProductPlacementList(
	ctx context.Context,
	req *connect.Request[inventory_iface.ProductPlacementListRequest],
) (*connect.Response[inventory_iface.ProductPlacementListResponse], error) {
	pay := req.Msg
	if pay.Page == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("page is required"))
	}

	result := &inventory_iface.ProductPlacementListResponse{
		Placements: []*inventory_iface.ProductPlacementItem{},
		PageInfo:   &common.PageInfo{},
	}
	db := s.db.WithContext(ctx)

	var rows []*inventory_models.StockPlacement
	paginated, pageInfo, err := db_connect.SetPaginationQuery(db, func() (*gorm.DB, error) {
		return db.Model(&inventory_models.StockPlacement{}).Scopes(func(d *gorm.DB) *gorm.DB {
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
	if err := paginated.Order("rack_id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}

	result.PageInfo = pageInfo
	for _, r := range rows {
		if r == nil {
			continue
		}
		result.Placements = append(result.Placements, &inventory_iface.ProductPlacementItem{
			Id:          r.ID,
			ProductId:   r.ProductID,
			WarehouseId: r.WarehouseID,
			RackId:      r.RackID,
			Count:       r.Count,
			CreatedAt:   timestamppb.New(r.CreatedAt),
			UpdatedAt:   timestamppb.New(r.UpdatedAt),
		})
	}

	return connect.NewResponse(result), nil
}
