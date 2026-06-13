package inventory_models

import (
	"time"

	"github.com/pdcgo/schema/services/inventory_iface/v1"
)

type StockPlacement struct {
	ID          uint64 `gorm:"primarykey"`
	ProductID   uint64 `gorm:"index:uniq_stock_placement,unique"`
	WarehouseID uint64 `gorm:"index:uniq_stock_placement,unique"`
	RackID      uint64 `gorm:"index:uniq_stock_placement,unique"`

	Count int64

	CreatedAt time.Time
	UpdatedAt time.Time
}

type StockPlacementLog struct {
	ID          uint64 `gorm:"primarykey"`
	ProductID   uint64
	WarehouseID uint64
	RackID      uint64
	ChangeType  inventory_iface.StockChangeType

	Change int64

	CreatedAt time.Time
}
