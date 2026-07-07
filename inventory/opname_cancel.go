package inventory

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"gorm.io/gorm"
)

// OpnameCancel implements [inventory_ifaceconnect.InventoryServiceHandler]. It cancels
// a pending session — a status flip only, the recorded counts are discarded and NO
// stock is touched. A completed session cannot be canceled. Idempotent.
func (s *inventoryServiceImpl) OpnameCancel(
	ctx context.Context,
	req *connect.Request[inventory_iface.OpnameCancelRequest],
) (*connect.Response[inventory_iface.OpnameCancelResponse], error) {
	pay := req.Msg
	if pay.GetOpnameId() == 0 || pay.GetWarehouseId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("opname_id and warehouse_id are required"))
	}

	err := s.db.
		WithContext(ctx).
		Transaction(func(tx *gorm.DB) error {
			opname, err := fetchOpname(tx, pay.GetOpnameId(), pay.GetWarehouseId())
			if err != nil {
				return err
			}
			if opname.Status == inventory_models.OpnameCanceled {
				return nil // idempotent
			}
			if opname.Status == inventory_models.OpnameCompleted {
				return connect.NewError(connect.CodeFailedPrecondition, errors.New("opname is already completed"))
			}

			now := time.Now()
			err = tx.
				Model(&inventory_models.InventoryOpname{}).
				Where("id = ?", opname.ID).
				Updates(map[string]interface{}{
					"status":      inventory_models.OpnameCanceled,
					"canceled_at": now,
					"updated_at":  now,
				}).
				Error
			if err != nil {
				return err
			}

			return appendOpnameLog(tx, opname.ID, "canceled", "")
		})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&inventory_iface.OpnameCancelResponse{}), nil
}
