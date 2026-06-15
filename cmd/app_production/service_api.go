package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/pdcgo/inventory_service"
	"github.com/pdcgo/shared/custom_connect"
	"github.com/urfave/cli/v3"
)

type ServiceApiFunc cli.ActionFunc

func NewServiceApiFunc(
	mux *http.ServeMux,
	register inventory_service.RegisterHandler,
	reflectorRegister custom_connect.RegisterReflectFunc,
) ServiceApiFunc {
	return func(ctx context.Context, c *cli.Command) error {
		reflectorRegister(register())

		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}

		host := os.Getenv("HOST")
		listen := fmt.Sprintf("%s:%s", host, port)
		log.Println("listening on", listen)

		// Serve HTTP/1 and unencrypted HTTP/2 (h2c) via net/http's native
		// protocol support (x/net/http2/h2c is deprecated).
		protocols := new(http.Protocols)
		protocols.SetHTTP1(true)
		protocols.SetUnencryptedHTTP2(true)

		srv := &http.Server{
			Addr:      listen,
			Handler:   custom_connect.WithCORS(mux),
			Protocols: protocols,
		}
		return srv.ListenAndServe()
	}
}
