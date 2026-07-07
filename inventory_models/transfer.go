package inventory_models

import "time"

type TransferStatus string

const (
	TransferPending  TransferStatus = "pending"
	TransferAccepted TransferStatus = "accepted"
	TransferCanceled TransferStatus = "canceled"
)

// InventoryTransfer is the warehouse-to-warehouse transfer document (InventoryService
// Transfer* RPCs), two-legged like the legacy WarehouseTransfer: creating one applies
// the OUT leg at the source (stock exits immediately — in transit; OutTransactionID),
// accepting applies the IN leg at the destination (InTransactionID + StockBatch).
// Canceling is pre-accept only and restores the source. Item prices are derived from
// the source StockState average at create time, so value is conserved.
type InventoryTransfer struct {
	ID               uint64 `gorm:"primarykey"`
	TeamID           uint64
	FromWarehouseID  uint64 `gorm:"index"`
	ToWarehouseID    uint64 `gorm:"index"`
	Note             string
	Status           TransferStatus `gorm:"index"`
	OutTransactionID uint64
	InTransactionID  uint64 // 0 until accepted
	CreatedByID      uint64
	CreatedAt        time.Time
	UpdatedAt        time.Time
	AcceptedAt       *time.Time
	CanceledAt       *time.Time
}

type InventoryTransferItem struct {
	ID         uint64 `gorm:"primarykey"`
	TransferID uint64 `gorm:"index"`
	ProductID  uint64
	Count      int64
	Price      float64 // per-unit, derived from the source StockState average
}
