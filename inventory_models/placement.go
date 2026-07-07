package inventory_models

import (
	"time"

	"github.com/pdcgo/schema/services/inventory_iface/v1"
)

// Rack duplicates the legacy db_models.Rack (see docs/database-schema.md) so
// inventory_service owns its own model. Table `racks` may pre-exist from legacy —
// the migration creates it only if absent. Soft-deleted via the Deleted flag.
type Rack struct {
	ID          uint64 `gorm:"primarykey"`
	WarehouseID uint64
	Name        string
	IsSystem    bool
	CreatedAt   time.Time `gorm:"autoCreateTime:milli"`
	Deleted     bool      `gorm:"index"`
}

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
	ID            uint64 `gorm:"primarykey"`
	ProductID     uint64
	WarehouseID   uint64
	RackID        uint64
	TransactionID uint64
	UserID        uint64

	ChangeType   inventory_iface.StockChangeType
	Change       int64
	BalanceCount int64
	Note         string

	CreatedAt time.Time
}
