package inventory_models

import "time"

type RestockStatus string

const (
	RestockPending  RestockStatus = "pending"
	RestockAccepted RestockStatus = "accepted"
	RestockProblem  RestockStatus = "problem"
	RestockCanceled RestockStatus = "canceled"
)

// InventoryRestock is the restock workflow document (InventoryService Restock*
// RPCs). Creating one has NO stock effect: stock enters only when it is accepted,
// which mints an InventoryTransaction (type restock) linked via TransactionID.
// Canceling an accepted restock reverses that transaction.
type InventoryRestock struct {
	ID          uint64 `gorm:"primarykey"`
	TeamID      uint64
	WarehouseID uint64 `gorm:"index"`
	Supplier    string
	Receipt     string
	Note        string
	// Purchase/order info (selling-team create-restock flow).
	ExternOrderID string
	ShippingID    uint64  // courier (common shipment id)
	ShippingCost  float64 // ongkir
	PaymentType   string  // "" | "shopeepay" | "transfer"
	// Fee the accepting warehouse charges the selling team on accept; posted as a
	// payable via invoice_v2 in the same tx. 0 = none. Set at accept time.
	WarehouseAcceptFee float64
	Status             RestockStatus `gorm:"index"`
	// The inventory transaction minted on accept; nil = not accepted.
	InventoryTransactionID *uint64
	CreatedByID            uint64
	CreatedAt              time.Time
	UpdatedAt              time.Time
	AcceptedAt             *time.Time
	CanceledAt             *time.Time
}

type InventoryRestockItem struct {
	ID        uint64 `gorm:"primarykey"`
	RestockID uint64 `gorm:"index"`
	ProductID uint64
	Count     int64
	Price     float64 // per-unit
	// Per-item problem detail, set via RestockUpdate{problem}.items.
	ProblemCount int64
	ProblemNote  string
}

// InventoryRestockLog is the restock document's audit trail (RestockLogList): one
// row per lifecycle event, written in the same transaction as the event itself.
type InventoryRestockLog struct {
	ID          uint64 `gorm:"primarykey"`
	RestockID   uint64 `gorm:"index"`
	Action      string // "created" | "edited" | "accepted" | "problem" | "canceled"
	Note        string
	CreatedByID uint64
	CreatedAt   time.Time
}
