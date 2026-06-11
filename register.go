package inventory_service

import (
	"net/http"

	"github.com/pdcgo/schema/services/inventory_iface/v1/inventory_ifaceconnect"
	"github.com/pdcgo/shared/custom_connect"
)

type ServiceReflectNames []string
type RegisterHandler func() ServiceReflectNames

// NewRegister mounts the v1 Connect InventoryService onto mux and returns the
// gRPC-reflection service names.
func NewRegister(
	mux *http.ServeMux,
	defaultInterceptor custom_connect.DefaultInterceptor,
) RegisterHandler {
	return func() ServiceReflectNames {
		grpcReflects := ServiceReflectNames{}

		path, handler := inventory_ifaceconnect.NewInventoryServiceHandler(
			NewInventoryService(),
			defaultInterceptor,
		)
		mux.Handle(path, handler)
		grpcReflects = append(grpcReflects, inventory_ifaceconnect.InventoryServiceName)

		return grpcReflects
	}
}
