package inventory

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service/inventory_models"
	common "github.com/pdcgo/schema/services/common/v1"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// OpnameList implements [inventory_ifaceconnect.InventoryServiceHandler].
//
// Flexible list (see docs/proto-guideline.md): it first resolves the ordered,
// paginated opname ids for the filter+sort, then fills a map<id, item> for each
// requested data type.
func (s *inventoryServiceImpl) OpnameList(
	ctx context.Context,
	req *connect.Request[inventory_iface.OpnameListRequest],
) (*connect.Response[inventory_iface.OpnameListResponse], error) {
	pay := req.Msg
	if pay.GetFilter() == nil || pay.GetFilter().GetPage() == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("filter.page is required"))
	}
	db := s.db.WithContext(ctx)

	ids, err := opnameListIDs(db, pay.GetFilter(), pay.GetSort())
	if err != nil {
		return nil, err
	}

	resp := &inventory_iface.OpnameListResponse{
		Data: make([]*inventory_iface.OpnameData, 0, len(pay.GetDataTypes())),
		Ids:  ids,
	}
	for _, dt := range pay.GetDataTypes() {
		data, err := fetchOpnameData(db, dt, ids)
		if err != nil {
			return nil, err
		}
		if data != nil {
			resp.Data = append(resp.Data, data)
		}
	}

	return connect.NewResponse(resp), nil
}

// opnameListIDs returns the ordered, paginated opname ids for the filter + sort.
func opnameListIDs(db *gorm.DB, filter *inventory_iface.OpnameListFilter, sort *inventory_iface.OpnameListSort) ([]uint64, error) {
	q := db.Table("inventory_opnames o")
	if filter.GetWarehouseId() > 0 {
		q = q.Where("o.warehouse_id = ?", filter.GetWarehouseId())
	}
	if filter.GetTeamId() > 0 {
		q = q.Where("o.team_id = ?", filter.GetTeamId())
	}
	if filter.GetStatus() != inventory_iface.OpnameStatus_OPNAME_STATUS_UNSPECIFIED {
		q = q.Where("o.status = ?", string(opnameStatusFromProto(filter.GetStatus())))
	}

	dir := "ASC"
	if sort.GetSortType() == common.SortType_SORT_TYPE_DESC {
		dir = "DESC"
	}

	limit, offset := rackPageLimitOffset(filter.GetPage())
	var ids []uint64
	err := q.
		Order("o.created_at "+dir).
		Order("o.id "+dir). // stable tiebreak
		Limit(limit).
		Offset(offset).
		Pluck("o.id", &ids).
		Error
	return ids, err
}

// fetchOpnameData builds one OpnameData (the oneof) for a data type as a map keyed
// by opname id.
func fetchOpnameData(db *gorm.DB, dt inventory_iface.OpnameListDataType, ids []uint64) (*inventory_iface.OpnameData, error) {
	switch dt {
	case inventory_iface.OpnameListDataType_OPNAME_LIST_DATA_TYPE_GENERAL:
		out := map[uint64]*inventory_iface.OpnameGeneralItem{}
		if len(ids) > 0 {
			var rows []struct {
				ID            uint64
				Name          string
				Status        string
				CreatedByID   uint64
				CreatedAt     time.Time
				CompletedByID uint64
				CompletedAt   *time.Time
			}
			err := db.
				Table("inventory_opnames").
				Select("id, name, status, created_by_id, created_at, completed_by_id, completed_at").
				Where("id IN ?", ids).
				Scan(&rows).
				Error
			if err != nil {
				return nil, err
			}
			for _, r := range rows {
				item := &inventory_iface.OpnameGeneralItem{
					Id:            r.ID,
					Name:          r.Name,
					Status:        opnameStatusToProto(inventory_models.OpnameStatus(r.Status)),
					CreatedById:   r.CreatedByID,
					CreatedAt:     timestamppb.New(r.CreatedAt),
					CompletedById: r.CompletedByID,
				}
				if r.CompletedAt != nil {
					item.CompletedAt = timestamppb.New(*r.CompletedAt)
				}
				out[r.ID] = item
			}
		}
		return &inventory_iface.OpnameData{Data: &inventory_iface.OpnameData_General{General: &inventory_iface.OpnameGeneralData{Data: out}}}, nil

	case inventory_iface.OpnameListDataType_OPNAME_LIST_DATA_TYPE_PROGRESS:
		out := map[uint64]*inventory_iface.OpnameProgressItem{}
		if len(ids) > 0 {
			var rows []struct {
				OpnameID         uint64
				LineCount        int64
				CountedCount     int64
				DiscrepancyCount int64
			}
			err := db.
				Table("inventory_opname_lines").
				Select(`opname_id,
					count(*) as line_count,
					coalesce(sum(case when counted then 1 else 0 end), 0) as counted_count,
					coalesce(sum(case when counted and counted_count <> expected_count then 1 else 0 end), 0) as discrepancy_count`).
				Where("opname_id IN ?", ids).
				Group("opname_id").
				Scan(&rows).
				Error
			if err != nil {
				return nil, err
			}
			for _, r := range rows {
				out[r.OpnameID] = &inventory_iface.OpnameProgressItem{
					LineCount:        r.LineCount,
					CountedCount:     r.CountedCount,
					DiscrepancyCount: r.DiscrepancyCount,
				}
			}
		}
		return &inventory_iface.OpnameData{Data: &inventory_iface.OpnameData_Progress{Progress: &inventory_iface.OpnameProgressData{Data: out}}}, nil
	}
	return nil, nil
}
