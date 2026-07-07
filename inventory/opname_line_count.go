package inventory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"gorm.io/gorm"
)

// OpnameLineCount implements [inventory_ifaceconnect.InventoryServiceHandler]. It
// records the counted quantities for ONE rack of a pending session (batch, re-count
// overwrites). A product not in the seeded lines upserts a new line with expected 0
// (found-but-not-expected). No stock effect — counts apply on complete.
func (s *inventoryServiceImpl) OpnameLineCount(
	ctx context.Context,
	req *connect.Request[inventory_iface.OpnameLineCountRequest],
) (*connect.Response[inventory_iface.OpnameLineCountResponse], error) {
	pay := req.Msg
	if pay.GetOpnameId() == 0 || pay.GetWarehouseId() == 0 || pay.GetRackId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("opname_id, warehouse_id and rack_id are required"))
	}
	if len(pay.GetItems()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("items are required"))
	}
	for _, it := range pay.GetItems() {
		if it.GetProductId() == 0 || it.GetCountedCount() < 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("each item needs product_id and counted_count >= 0"))
		}
	}

	err := s.db.
		WithContext(ctx).
		Transaction(func(tx *gorm.DB) error {
			opname, err := fetchOpname(tx, pay.GetOpnameId(), pay.GetWarehouseId())
			if err != nil {
				return err
			}
			err = requirePendingOpname(opname)
			if err != nil {
				return err
			}

			now := time.Now()
			for _, it := range pay.GetItems() {
				res := tx.
					Model(&inventory_models.InventoryOpnameLine{}).
					Where("opname_id = ? AND rack_id = ? AND product_id = ?",
						opname.ID, pay.GetRackId(), it.GetProductId()).
					Updates(map[string]interface{}{
						"counted_count": it.GetCountedCount(),
						"counted":       true,
						"reason":        opnameReasonFromProto(it.GetReason()),
						"note":          it.GetNote(),
					})
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected == 0 {
					// Found-but-not-expected: a product counted on the rack that had no
					// placement at session create.
					line := inventory_models.InventoryOpnameLine{
						OpnameID:      opname.ID,
						RackID:        pay.GetRackId(),
						ProductID:     it.GetProductId(),
						ExpectedCount: 0,
						CountedCount:  it.GetCountedCount(),
						Counted:       true,
						Reason:        opnameReasonFromProto(it.GetReason()),
						Note:          it.GetNote(),
					}
					err = tx.Create(&line).Error
					if err != nil {
						return err
					}
				}
			}

			err = tx.
				Model(&inventory_models.InventoryOpname{}).
				Where("id = ?", opname.ID).
				Update("updated_at", now).
				Error
			if err != nil {
				return err
			}

			return appendOpnameLog(tx, opname.ID, "counted", fmt.Sprintf("rack %d", pay.GetRackId()))
		})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&inventory_iface.OpnameLineCountResponse{}), nil
}
