package inventory

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
)

// RackByIds implements [inventory_ifaceconnect.InventoryServiceHandler]. It bulk-loads
// non-deleted racks by id, keyed by id, for preloading rack names in other UIs (so raw
// ids are never shown). Missing and deleted ids are omitted from the map. Any
// authenticated caller.
func (s *inventoryServiceImpl) RackByIds(
	ctx context.Context,
	req *connect.Request[inventory_iface.RackByIdsRequest],
) (*connect.Response[inventory_iface.RackByIdsResponse], error) {
	pay := req.Msg
	if pay.Filter == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("filter is required"))
	}

	result := &inventory_iface.RackByIdsResponse{
		Items: map[uint64]*inventory_iface.RackByIdsItemList{},
	}
	if len(pay.Filter.Ids) == 0 {
		return connect.NewResponse(result), nil
	}

	db := s.db.WithContext(ctx)

	var racks []*inventory_models.Rack
	err := db.
		Where("deleted = ?", false).
		Where("id IN ?", pay.Filter.Ids).
		Find(&racks).
		Error
	if err != nil {
		return nil, err
	}

	for _, rack := range racks {
		list := &inventory_iface.RackByIdsItemList{Items: []*inventory_iface.RackByIdsItem{}}
		for _, dt := range pay.DataRequest {
			switch dt {
			case inventory_iface.RackByIdsDataType_RACK_BY_IDS_DATA_TYPE_GENERAL:
				list.Items = append(list.Items, &inventory_iface.RackByIdsItem{
					D: &inventory_iface.RackByIdsItem_General{
						General: &inventory_iface.RackByIdsGeneralItem{
							Id:   rack.ID,
							Name: rack.Name,
						},
					},
				})
			}
		}
		result.Items[rack.ID] = list
	}

	return connect.NewResponse(result), nil
}
