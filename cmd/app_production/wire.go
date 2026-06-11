//go:build wireinject
// +build wireinject

package main

import (
	"net/http"

	"github.com/google/wire"
	"github.com/pdcgo/inventory_service"
	"github.com/pdcgo/shared/custom_connect"
)

func InitializeApp() (*App, error) {
	wire.Build(
		http.NewServeMux,
		custom_connect.NewDefaultInterceptor,
		custom_connect.NewRegisterReflect,
		inventory_service.NewRegister,
		NewApp,
	)
	return &App{}, nil
}
