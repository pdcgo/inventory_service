package inventory_service

import (
	"net/http"

	"github.com/pdcgo/inventory_service/inventory"
	"github.com/pdcgo/schema/services/inventory_iface/v1/inventory_ifaceconnect"
	"github.com/pdcgo/shared/custom_connect"
)

type ServiceReflectNames []string
type RegisterHandler func() ServiceReflectNames

// NewRegister mounts the v1 Connect InventoryService onto mux and returns the
// gRPC-reflection service names. It also mounts the Pub/Sub push endpoint.
func NewRegister(
	mux *http.ServeMux,
	defaultInterceptor custom_connect.DefaultInterceptor,
	pushHttpHandler InventoryPushHttpHandler,
) RegisterHandler {
	return func() ServiceReflectNames {
		grpcReflects := ServiceReflectNames{}

		path, handler := inventory_ifaceconnect.NewInventoryServiceHandler(
			inventory.NewInventoryService(),
			defaultInterceptor,
		)
		mux.Handle(path, handler)
		grpcReflects = append(grpcReflects, inventory_ifaceconnect.InventoryServiceName)

		mux.HandleFunc("/inventory/push", pushHttpHandler)

		return grpcReflects
	}
}
