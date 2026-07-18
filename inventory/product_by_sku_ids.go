package inventory

import (
	"context"

	"connectrpc.com/connect"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/shared/db_models"
)

// ProductBySkuIds implements [inventory_ifaceconnect.InventoryServiceHandler]. It
// resolves each SKU id to its product, keyed by sku_id. The product id is decoded from
// the SkuID itself (no skus-table lookup). A sku_id that fails to decode, or whose
// product is missing/deleted, is omitted from the map.
func (s *inventoryServiceImpl) ProductBySkuIds(
	ctx context.Context,
	req *connect.Request[inventory_iface.ProductBySkuIdsRequest],
) (*connect.Response[inventory_iface.ProductBySkuIdsResponse], error) {
	pay := req.Msg
	db := s.db.WithContext(ctx)

	result := &inventory_iface.ProductBySkuIdsResponse{
		Products: map[string]*inventory_iface.ProductBySkuDetail{},
	}
	if len(pay.GetSkuIds()) == 0 {
		return connect.NewResponse(result), nil
	}

	// Decode each sku_id to its product id; keep the sku_id → product_id mapping so the
	// response can be keyed back by sku_id. Undecodable sku ids are dropped.
	skuToProduct := map[string]uint{}
	productIDSet := map[uint]struct{}{}
	for _, skuID := range pay.GetSkuIds() {
		data, err := db_models.SkuID(skuID).Extract()
		if err != nil || data.ProductID == 0 {
			continue
		}
		skuToProduct[skuID] = data.ProductID
		productIDSet[data.ProductID] = struct{}{}
	}
	if len(productIDSet) == 0 {
		return connect.NewResponse(result), nil
	}

	productIDs := make([]uint, 0, len(productIDSet))
	for id := range productIDSet {
		productIDs = append(productIDs, id)
	}

	var products []*db_models.Product
	err := db.
		Model(&db_models.Product{}).
		Select([]string{"id", "name", "ref_id", "image"}).
		Where("deleted = ?", false).
		Where("id IN ?", productIDs).
		Find(&products).
		Error
	if err != nil {
		return nil, err
	}

	byID := make(map[uint]*db_models.Product, len(products))
	for _, p := range products {
		byID[p.ID] = p
	}

	for skuID, productID := range skuToProduct {
		p := byID[productID]
		if p == nil {
			continue // product missing or deleted
		}
		image := ""
		if len(p.Image) > 0 {
			image = p.Image[0]
		}
		result.Products[skuID] = &inventory_iface.ProductBySkuDetail{
			Id:    uint64(p.ID),
			Name:  p.Name,
			RefId: string(p.RefID),
			Image: image,
		}
	}

	return connect.NewResponse(result), nil
}
