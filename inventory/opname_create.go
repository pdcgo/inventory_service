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

// OpnameCreate implements [inventory_ifaceconnect.InventoryServiceHandler]. It records
// a PENDING stock-take session and seeds one counting line per (rack, product) from the
// warehouse's current StockPlacement rows (count > 0) — the expected snapshot. It has
// NO stock effect: stock changes only when the session is completed.
func (s *inventoryServiceImpl) OpnameCreate(
	ctx context.Context,
	req *connect.Request[inventory_iface.OpnameCreateRequest],
) (*connect.Response[inventory_iface.OpnameCreateResponse], error) {
	pay := req.Msg
	if pay.GetWarehouseId() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("warehouse_id is required"))
	}
	if pay.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}

	resp := &inventory_iface.OpnameCreateResponse{}
	now := time.Now()

	err := s.db.
		WithContext(ctx).
		Transaction(func(tx *gorm.DB) error {
			opname := inventory_models.InventoryOpname{
				TeamID:      pay.GetTeamId(),
				WarehouseID: pay.GetWarehouseId(),
				Name:        pay.GetName(),
				Status:      inventory_models.OpnamePending,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			err := tx.Create(&opname).Error
			if err != nil {
				return err
			}

			// Expected snapshot: every placed product per rack (count > 0).
			var placements []struct {
				RackID    uint64
				ProductID uint64
				Count     int64
			}
			err = tx.
				Table("stock_placements").
				Select("rack_id, product_id, count").
				Where("warehouse_id = ? AND count > 0", pay.GetWarehouseId()).
				Scan(&placements).
				Error
			if err != nil {
				return err
			}

			for _, p := range placements {
				line := inventory_models.InventoryOpnameLine{
					OpnameID:      opname.ID,
					RackID:        p.RackID,
					ProductID:     p.ProductID,
					ExpectedCount: p.Count,
				}
				err = tx.Create(&line).Error
				if err != nil {
					return err
				}
			}

			err = appendOpnameLog(tx, opname.ID, "created", "")
			if err != nil {
				return err
			}

			resp.OpnameId = opname.ID
			return nil
		})
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(resp), nil
}

// appendOpnameLog writes one audit-trail row for an opname lifecycle event, in the
// same transaction as the event itself.
func appendOpnameLog(tx *gorm.DB, opnameID uint64, action, note string) error {
	log := inventory_models.InventoryOpnameLog{
		OpnameID:  opnameID,
		Action:    action,
		Note:      note,
		CreatedAt: time.Now(),
	}
	return tx.Create(&log).Error
}

// fetchOpname loads the session scoped to its warehouse, or NotFound.
func fetchOpname(tx *gorm.DB, opnameID, warehouseID uint64) (*inventory_models.InventoryOpname, error) {
	var opname inventory_models.InventoryOpname
	err := tx.
		Where("id = ? AND warehouse_id = ?", opnameID, warehouseID).
		First(&opname).
		Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("opname not found"))
		}
		return nil, err
	}
	return &opname, nil
}

// requirePendingOpname rejects mutations once the session is completed or canceled.
func requirePendingOpname(opname *inventory_models.InventoryOpname) error {
	if opname.Status == inventory_models.OpnameCompleted {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("opname is already completed"))
	}
	if opname.Status == inventory_models.OpnameCanceled {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("opname is canceled"))
	}
	return nil
}

// opnameStatusToProto / opnameStatusFromProto map the stored status string to the
// proto enum and back.
func opnameStatusToProto(s inventory_models.OpnameStatus) inventory_iface.OpnameStatus {
	switch s {
	case inventory_models.OpnamePending:
		return inventory_iface.OpnameStatus_OPNAME_STATUS_PENDING
	case inventory_models.OpnameCompleted:
		return inventory_iface.OpnameStatus_OPNAME_STATUS_COMPLETED
	case inventory_models.OpnameCanceled:
		return inventory_iface.OpnameStatus_OPNAME_STATUS_CANCELED
	default:
		return inventory_iface.OpnameStatus_OPNAME_STATUS_UNSPECIFIED
	}
}

func opnameStatusFromProto(s inventory_iface.OpnameStatus) inventory_models.OpnameStatus {
	switch s {
	case inventory_iface.OpnameStatus_OPNAME_STATUS_PENDING:
		return inventory_models.OpnamePending
	case inventory_iface.OpnameStatus_OPNAME_STATUS_COMPLETED:
		return inventory_models.OpnameCompleted
	case inventory_iface.OpnameStatus_OPNAME_STATUS_CANCELED:
		return inventory_models.OpnameCanceled
	default:
		return ""
	}
}

// opnameReasonToProto / opnameReasonFromProto map the stored per-line reason string
// ("lost" | "broken" | "disaster" | "") to the proto enum and back.
func opnameReasonToProto(s string) inventory_iface.OpnameReasonType {
	switch s {
	case "lost":
		return inventory_iface.OpnameReasonType_OPNAME_REASON_TYPE_LOST
	case "broken":
		return inventory_iface.OpnameReasonType_OPNAME_REASON_TYPE_BROKEN
	case "disaster":
		return inventory_iface.OpnameReasonType_OPNAME_REASON_TYPE_DISASTER
	default:
		return inventory_iface.OpnameReasonType_OPNAME_REASON_TYPE_UNSPECIFIED
	}
}

func opnameReasonFromProto(r inventory_iface.OpnameReasonType) string {
	switch r {
	case inventory_iface.OpnameReasonType_OPNAME_REASON_TYPE_LOST:
		return "lost"
	case inventory_iface.OpnameReasonType_OPNAME_REASON_TYPE_BROKEN:
		return "broken"
	case inventory_iface.OpnameReasonType_OPNAME_REASON_TYPE_DISASTER:
		return "disaster"
	default:
		return ""
	}
}
