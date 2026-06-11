package inventory_service_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/pdcgo/inventory_service"
	inventory_iface "github.com/pdcgo/schema/services/inventory_iface/v1"
	"github.com/stretchr/testify/assert"
)

func TestHello(t *testing.T) {
	svc := inventory_service.NewInventoryService()
	res, err := svc.Hello(context.Background(), connect.NewRequest(&inventory_iface.HelloRequest{}))
	assert.NoError(t, err)
	assert.NotNil(t, res.Msg)
}
