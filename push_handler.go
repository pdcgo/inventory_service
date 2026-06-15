package inventory_service

import (
	"context"
	"net/http"

	"github.com/pdcgo/event_source"
	"github.com/pdcgo/inventory_service/inventory"
	"github.com/pdcgo/san_collection/san_config"
	warehouse_iface "github.com/pdcgo/schema/services/warehouse_iface/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"gorm.io/gorm"
)

type InventoryPushHandler event_source.PushHandler

// NewInventoryPushHandler decodes a pushed warehouse StockEvent and applies it to
// stock state in one transaction via inventory.ProcessStockEvent (shared with the
// InventoryService.PushStockEvent RPC). Unknown subscriptions are acked (no-op).
func NewInventoryPushHandler(
	db *gorm.DB,
	projectCfg *san_config.ProjectConfig,
) InventoryPushHandler {
	return func(ctx context.Context, msg *event_source.PushRequest) error {
		switch msg.Subscription {
		case projectCfg.PubsubSubscriberPath("inventory-stock-sub"):
			var event warehouse_iface.StockEvent
			if err := protojson.Unmarshal(msg.Message.Data, &event); err != nil {
				return err
			}
			return db.Transaction(func(tx *gorm.DB) error {
				return inventory.ProcessStockEvent(tx, &event)
			})
		}

		return nil
	}
}

type InventoryPushHttpHandler http.HandlerFunc

func NewInventoryPushHttpHandler(handler InventoryPushHandler) InventoryPushHttpHandler {
	return InventoryPushHttpHandler(event_source.NewMuxPushhandler(event_source.PushHandler(handler)))
}
