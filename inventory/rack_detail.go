package inventory

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// RackDetail implements [inventory_ifaceconnect.InventoryServiceHandler].
//
// Returns one rack (scoped by warehouse_id) with its current stock count (sum of placements),
// distinct product count (placements with count > 0), and warehouse name.
func (s *inventoryServiceImpl) RackDetail(
	ctx context.Context,
	req *connect.Request[inventory_iface.RackDetailRequest],
) (*connect.Response[inventory_iface.RackDetailResponse], error) {
	pay := req.Msg
	if pay.GetId() == 0 || pay.GetWarehouseId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id and warehouse_id are required"))
	}
	db := s.db.WithContext(ctx)

	var rack inventory_models.Rack
	err := db.Where("id = ? AND warehouse_id = ? AND deleted = false", pay.GetId(), pay.GetWarehouseId()).First(&rack).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("rack not found"))
		}
		return nil, err
	}

	var counts struct {
		StockCount   int64
		ProductCount int64
	}
	if err := db.
		Table("stock_placements").
		Where("rack_id = ? AND warehouse_id = ?", pay.GetId(), pay.GetWarehouseId()).
		Select("coalesce(sum(count),0) as stock_count, count(distinct product_id) filter (where count > 0) as product_count").
		Scan(&counts).Error; err != nil {
		return nil, err
	}

	var wh struct{ Name string }
	if err := db.Table("warehouses").Select("name").Where("id = ?", pay.GetWarehouseId()).Limit(1).Scan(&wh).Error; err != nil {
		return nil, err
	}

	return connect.NewResponse(&inventory_iface.RackDetailResponse{
		Id:            uint64(rack.ID),
		WarehouseId:   uint64(rack.WarehouseID),
		Name:          rack.Name,
		IsSystem:      rack.IsSystem,
		StockCount:    counts.StockCount,
		ProductCount:  counts.ProductCount,
		WarehouseName: wh.Name,
		CreatedAt:     timestamppb.New(rack.CreatedAt),
	}), nil
}
