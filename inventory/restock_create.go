package inventory

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	warehouse_iface "github.com/pdcgo/schema/services/warehouse_iface/v1"
	"gorm.io/gorm"
)

// RestockCreate implements [inventory_ifaceconnect.InventoryServiceHandler]. It
// records a PENDING restock document (header + items). It has NO stock effect:
// stock enters only when the restock is accepted via RestockUpdate{accept}, which
// mints the internal inventory transaction.
func (s *inventoryServiceImpl) RestockCreate(
	ctx context.Context,
	req *connect.Request[inventory_iface.RestockCreateRequest],
) (*connect.Response[inventory_iface.RestockCreateResponse], error) {
	pay := req.Msg
	if pay.GetWarehouseId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("warehouse_id is required"))
	}
	err := validateRestockItems(pay.GetItems())
	if err != nil {
		return nil, err
	}

	resp := &inventory_iface.RestockCreateResponse{}
	now := time.Now()

	err = s.db.
		WithContext(ctx).
		Transaction(func(tx *gorm.DB) error {
			restock := inventory_models.InventoryRestock{
				TeamID:        pay.GetTeamId(),
				WarehouseID:   pay.GetWarehouseId(),
				Supplier:      pay.GetSupplier(),
				Receipt:       pay.GetReceipt(),
				Note:          pay.GetNote(),
				ExternOrderID: pay.GetExternOrderId(),
				ShippingID:    pay.GetShippingId(),
				ShippingCost:  pay.GetShippingCost(),
				PaymentType:   paymentTypeToModel(pay.GetPaymentType()),
				Status:        inventory_models.RestockPending,
				CreatedAt:     now,
				UpdatedAt:     now,
			}
			err := tx.Create(&restock).Error
			if err != nil {
				return err
			}

			err = createRestockItems(tx, restock.ID, pay.GetItems())
			if err != nil {
				return err
			}

			err = appendRestockLog(tx, restock.ID, "created", "")
			if err != nil {
				return err
			}

			resp.RestockId = restock.ID
			return nil
		})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}

// validateRestockItems checks the shared item constraints (min 1; product + count>0).
func validateRestockItems(items []*inventory_iface.RestockItem) error {
	if len(items) == 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("items are required"))
	}
	for _, it := range items {
		if it.GetProductId() == 0 || it.GetCount() <= 0 {
			return connect.NewError(connect.CodeInvalidArgument, errors.New("each item needs product_id and count > 0"))
		}
	}
	return nil
}

// paymentTypeToModel / paymentTypeToProto map the warehouse payment enum to the
// stored lowercase name and back.
func paymentTypeToModel(t warehouse_iface.PaymentType) string {
	switch t {
	case warehouse_iface.PaymentType_PAYMENT_TYPE_SHOPEEPAY:
		return "shopeepay"
	case warehouse_iface.PaymentType_PAYMENT_TYPE_TRANSFER:
		return "transfer"
	default:
		return ""
	}
}

func paymentTypeToProto(s string) warehouse_iface.PaymentType {
	switch s {
	case "shopeepay":
		return warehouse_iface.PaymentType_PAYMENT_TYPE_SHOPEEPAY
	case "transfer":
		return warehouse_iface.PaymentType_PAYMENT_TYPE_TRANSFER
	default:
		return warehouse_iface.PaymentType_PAYMENT_TYPE_UNSPECIFIED
	}
}

// appendRestockLog writes one audit-trail row for a restock lifecycle event, in the
// same transaction as the event itself (RestockLogList reads these back).
func appendRestockLog(tx *gorm.DB, restockID uint64, action, note string) error {
	log := inventory_models.InventoryRestockLog{
		RestockID: restockID,
		Action:    action,
		Note:      note,
		CreatedAt: time.Now(),
	}
	return tx.Create(&log).Error
}

// createRestockItems inserts the restock's item rows.
func createRestockItems(tx *gorm.DB, restockID uint64, items []*inventory_iface.RestockItem) error {
	for _, it := range items {
		row := inventory_models.InventoryRestockItem{
			RestockID: restockID,
			ProductID: it.GetProductId(),
			Count:     it.GetCount(),
			Price:     it.GetPrice(),
		}
		err := tx.Create(&row).Error
		if err != nil {
			return err
		}
	}
	return nil
}
