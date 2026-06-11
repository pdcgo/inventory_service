package inventory_models

import (
	"time"

	"github.com/pdcgo/shared/db_models"
)

type StockPlacement struct {
	ID          uint64          `gorm:"primarykey"`
	SkuID       db_models.SkuID `gorm:"index:uniq_stock_placement,unique"`
	WarehouseID uint64          `gorm:"index:uniq_stock_placement,unique"`
	RackID      uint64          `gorm:"index:uniq_stock_placement,unique"`

	Count int64

	CreatedAt time.Time
	UpdatedAt time.Time
}

type StockPlacementLog struct {
	ID          uint64 `gorm:"primarykey"`
	SkuID       db_models.SkuID
	WarehouseID uint64
	RackID      uint64
	ChangeType  StockChangeType

	Change int64

	CreatedAt time.Time
}
