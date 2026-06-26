package inventory_models

import "time"

// InventoryExactlyOnceLog is the message-id inbox for exactly-once processing of
// pushed events: one row per (Pub/Sub MessageID, subscription), written inside the
// event's transaction so a redelivery is skipped. The struct name maps to table
// inventory_exactly_once_logs by GORM's default naming (no TableName override).
type InventoryExactlyOnceLog struct {
	ID           string    `gorm:"primaryKey;type:varchar(400)"`
	Subscription string    `gorm:"primaryKey;type:varchar(400)"`
	CreatedAt    time.Time `gorm:"type:timestamptz;not null;default:now()"`
}
