package inventory

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	common "github.com/pdcgo/schema/services/common/v1"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/db_connect"
	"gorm.io/gorm"
)

// RackProductList implements [inventory_ifaceconnect.InventoryServiceHandler]. It powers
// the Rack Detail "Products" tab: the products currently placed on a rack (count > 0),
// with their names (joined from products) and on-rack counts, most-stocked first.
func (s *inventoryServiceImpl) RackProductList(
	ctx context.Context,
	req *connect.Request[inventory_iface.RackProductListRequest],
) (*connect.Response[inventory_iface.RackProductListResponse], error) {
	pay := req.Msg
	if pay.Page == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("page is required"))
	}

	result := &inventory_iface.RackProductListResponse{
		Items:    []*inventory_iface.RackProductItem{},
		PageInfo: &common.PageInfo{},
	}
	db := s.db.WithContext(ctx)

	var rows []struct {
		ProductID   uint64
		ProductName string
		Count       int64
	}
	paginated, pageInfo, err := db_connect.SetPaginationQuery(db, func() (*gorm.DB, error) {
		return db.
			Table("stock_placements sp").
			Joins("LEFT JOIN products p ON p.id = sp.product_id").
			Where("sp.rack_id = ? AND sp.warehouse_id = ? AND sp.count > 0", pay.RackId, pay.WarehouseId).
			Select("sp.product_id as product_id, COALESCE(p.name, '') as product_name, sp.count as count"), nil
	}, pay.Page)
	if err != nil {
		return nil, err
	}

	err = paginated.
		Order("sp.count DESC").
		Scan(&rows).
		Error
	if err != nil {
		return nil, err
	}

	result.PageInfo = pageInfo
	for _, r := range rows {
		result.Items = append(result.Items, &inventory_iface.RackProductItem{
			ProductId:   r.ProductID,
			ProductName: r.ProductName,
			Count:       r.Count,
		})
	}

	return connect.NewResponse(result), nil
}
