package inventory_models

import (
	"time"

	"github.com/pdcgo/shared/db_models"
)

type BatchStock struct {
	ID          uint64 `gorm:"primarykey"`
	SkuID       db_models.SkuID
	WarehouseID uint64
	InboundID   uint64

	StartCount int64
	EndCount   int64

	Price float64

	UpdatedAt time.Time
	CreatedAt time.Time
}

type BatchStockLog struct {
	ID          uint64 `gorm:"primarykey"`
	SkuID       db_models.SkuID
	WarehouseID uint64
	BatchID     uint64

	ChangeType StockChangeType

	Change int64
	Price  float64

	BatchCount  int64
	BatchAmount float64

	BalanceCount  int64
	BalanceAmount float64

	CreatedAt time.Time
}
