package inventory_models

import "time"

type InventoryTxType string

const (
	InvTxOrder     InventoryTxType = "order"
	InvTxRestock   InventoryTxType = "restock"
	InvTxReturn    InventoryTxType = "return"
	InvTxFoundBack InventoryTxType = "found_back"
	InvTxProblem   InventoryTxType = "problem"
)

type InventoryTxStatus string

const (
	InvTxActive   InventoryTxStatus = "active"
	InvTxCanceled InventoryTxStatus = "canceled"
)

// InventoryTransaction is an inventory-owned, request-driven stock mutation created
// via InventoryService.TransactionCreate — distinct from the legacy warehouse
// inv_transactions. TransactionCancel flips Status to canceled and reverses stock.
type InventoryTransaction struct {
	ID          uint64 `gorm:"primarykey"`
	TeamID      uint64
	WarehouseID uint64 `gorm:"index"`
	Type        InventoryTxType
	Status      InventoryTxStatus `gorm:"index"`
	CreatedByID uint64
	CreatedAt   time.Time
	CanceledAt  *time.Time
}

type InventoryTransactionItem struct {
	ID            uint64 `gorm:"primarykey"`
	TransactionID uint64 `gorm:"index"`
	ProductID     uint64
	Count         int64
	Price         float64 // per-unit
}
