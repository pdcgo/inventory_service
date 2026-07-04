package inventory_models

import (
	"time"

	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
)

// ProductConfig is the per-(product, warehouse) configuration: how StockBatch price
// consumption is ordered (QueueType) and how StockPlacement picking chooses a rack
// (PlacementPicking). Absent config means the defaults (FIFO + smaller-quantity).
type ProductConfig struct {
	ID               uint64 `gorm:"primarykey"`
	ProductID        uint64 `gorm:"index:uniq_product_config,unique"`
	WarehouseID      uint64 `gorm:"index:uniq_product_config,unique"`
	QueueType        inventory_iface.QueueType
	PlacementPicking inventory_iface.PlacementPickingType

	CreatedAt time.Time
	UpdatedAt time.Time
}
