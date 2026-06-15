//go:build wireinject
// +build wireinject

package main

import (
	"net/http"

	"github.com/google/wire"
	"github.com/pdcgo/inventory_service"
	"github.com/pdcgo/shared/configs"
	"github.com/pdcgo/shared/custom_connect"
	"github.com/urfave/cli/v3"
)

func InitializeApp() (*cli.Command, error) {
	wire.Build(
		configs.NewProductionConfig,
		http.NewServeMux,
		custom_connect.NewDefaultInterceptor,
		custom_connect.NewRegisterReflect,
		NewDatabase,
		NewProjectConfig,
		inventory_service.NewInventoryPushHandler,
		inventory_service.NewInventoryPushHttpHandler,
		inventory_service.NewRegister,
		NewServiceApiFunc,
		NewSyncLegacyFunc,
		NewApp,
	)

	return &cli.Command{}, nil
}
