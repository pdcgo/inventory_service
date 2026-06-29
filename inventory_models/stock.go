package inventory_models

import (
	"time"

	"github.com/pdcgo/schema/services/inventory_iface/v1"
)

type StockState struct {
	ID          uint64 `gorm:"primarykey"`
	ProductID   uint64 `gorm:"index:uniq_stock_state,unique"`
	WarehouseID uint64 `gorm:"index:uniq_stock_state,unique"`

	StockReady       int64
	StockReadyAmount float64

	UpdatedAt time.Time
	CreatedAt time.Time
}

type StockBatch struct {
	ID          uint64 `gorm:"primarykey"`
	ProductID   uint64 `gorm:"index:uniq_stock_batch_code,unique,priority:1,where:batch_code <> ''"`
	WarehouseID uint64
	InboundID   uint64

	// BatchCode is a business code for the inbound batch, unique per product
	// (empty codes are exempt — the partial index ignores them).
	BatchCode string `gorm:"index:uniq_stock_batch_code,priority:2"`

	StartCount int64
	EndCount   int64

	Price float64

	UpdatedAt time.Time
	CreatedAt time.Time
}

type StockBatchLog struct {
	ID            uint64 `gorm:"primarykey"`
	ProductID     uint64
	WarehouseID   uint64
	UserID        uint64
	BatchID       uint64
	TransactionID uint64

	ChangeType inventory_iface.StockChangeType

	Change int64
	Price  float64

	BatchCount  int64
	BatchAmount float64

	BalanceCount  int64
	BalanceAmount float64

	CreatedAt time.Time
}
