package main

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"connectrpc.com/connect"
	"github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/pdcgo/schema/services/inventory_iface/v1/inventory_ifaceconnect"
	"github.com/urfave/cli/v3"
	"gorm.io/gorm"
)

type SyncLegacyFunc cli.ActionFunc

func NewSyncLegacyFunc(
	db *gorm.DB,
) SyncLegacyFunc {
	return func(ctx context.Context, c *cli.Command) error {

		host := c.String("host")
		concurency := c.Int32("concurency")

		slog.Info("creating client", "uri", host)
		client := inventory_ifaceconnect.NewInventoryServiceClient(
			http.DefaultClient,
			host,
		)

		syncitems := make(chan *syncItem, concurency)
		limit := make(chan int, concurency)

		go func() {
			defer close(syncitems)
			query := db.
				Table("skus s").
				// Joins("left join stock_states ss on ss.product_id = s.product_id and ss.warehouse_id = s.warehouse_id").
				// Joins("left join products p on p.id = s.product_id").
				// Where("s.stock_ready != ss.stock_ready").
				Select([]string{
					"s.id",
					"s.product_id",
					"s.warehouse_id",
				})

			rows, err := query.Rows()

			if err != nil {
				panic(err)
			}

			var wg sync.WaitGroup

			for rows.Next() {
				var item syncItem
				err = db.ScanRows(rows, &item)
				if err != nil {
					panic(err)
				}

				limit <- 1
				wg.Add(1)

				go func() {
					defer wg.Done()
					stream, err := client.ProductReconcile(
						ctx,
						connect.NewRequest(&inventory_iface.ProductReconcileRequest{
							ProductId:   item.ProductID,
							WarehouseId: item.WarehouseID,
						}),
					)
					if err != nil {
						slog.Error("error process sync", "err", err.Error())
					} else {
						for stream.Receive() {
							msg := stream.Msg()
							slog.Info(msg.Message)
						}
						if err := stream.Err(); err != nil {
							slog.Error("error process sync", "err", err.Error())
						}
						stream.Close()
					}

					<-limit
					syncitems <- &item

				}()

			}

			wg.Wait()
		}()

		for item := range syncitems {
			slog.Info("sync", "sku_id", item.ID)
		}

		return nil
	}
}

type syncItem struct {
	ID          string
	ProductID   uint64
	WarehouseID uint64
}
