package inventory_models

import "time"

type OpnameStatus string

const (
	OpnamePending   OpnameStatus = "pending"
	OpnameCompleted OpnameStatus = "completed"
	OpnameCanceled  OpnameStatus = "canceled"
)

// InventoryOpname is the stock-take session document (InventoryService Opname* RPCs).
// Creating one has NO stock effect: lines are seeded from the warehouse's current
// StockPlacement rows (the expected snapshot), counting fills in counted values, and
// completing mints an InventoryTransaction (type opname) whose ADJUSTMENT mutations
// set each counted (rack, product) placement to its counted value. Canceling a
// pending session discards the counts with no stock effect.
type InventoryOpname struct {
	ID          uint64 `gorm:"primarykey"`
	TeamID      uint64
	WarehouseID uint64 `gorm:"index"`
	Name        string
	Status      OpnameStatus `gorm:"index"`
	// The inventory transaction minted on complete; nil = not completed.
	InventoryTransactionID *uint64
	CreatedByID            uint64
	CompletedByID          uint64
	CreatedAt              time.Time
	UpdatedAt              time.Time
	CompletedAt            *time.Time
	CanceledAt             *time.Time
}

// InventoryOpnameLine is one (rack, product) counting line — the StockPlacement
// grain. ExpectedCount is the placement snapshot at session create; Counted=false
// means not counted yet (left untouched on complete). Reason ("lost" | "broken" |
// "disaster" | "") and Note are optional per-line context for a discrepancy; on
// complete they flow into the ADJUSTMENT placement-log note.
type InventoryOpnameLine struct {
	ID            uint64 `gorm:"primarykey"`
	OpnameID      uint64 `gorm:"index;index:uniq_opname_line,unique"`
	RackID        uint64 `gorm:"index:uniq_opname_line,unique"`
	ProductID     uint64 `gorm:"index:uniq_opname_line,unique"`
	ExpectedCount int64
	CountedCount  int64
	Counted       bool
	Reason        string
	Note          string
}

// InventoryOpnameLog is the opname document's audit trail: one row per lifecycle
// event, written in the same transaction as the event itself.
type InventoryOpnameLog struct {
	ID          uint64 `gorm:"primarykey"`
	OpnameID    uint64 `gorm:"index"`
	Action      string // "created" | "counted" | "completed" | "canceled"
	Note        string
	CreatedByID uint64
	CreatedAt   time.Time
}
