package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/pdcgo/inventory_service"
	"github.com/pdcgo/shared/custom_connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type App struct {
	Run func() error
}

func NewApp(
	mux *http.ServeMux,
	register inventory_service.RegisterHandler,
	reflectorRegister custom_connect.RegisterReflectFunc,
) *App {
	return &App{
		Run: func() error {
			reflectorRegister(register())

			port := os.Getenv("PORT")
			if port == "" {
				port = "8080"
			}

			host := os.Getenv("HOST")
			listen := fmt.Sprintf("%s:%s", host, port)
			log.Println("listening on", listen)

			return http.ListenAndServe(
				listen,
				// Use h2c so we can serve HTTP/2 without TLS.
				h2c.NewHandler(
					custom_connect.WithCORS(mux),
					&http2.Server{}),
			)
		},
	}
}

func main() {
	app, err := InitializeApp()
	if err != nil {
		panic(err)
	}

	if err := app.Run(); err != nil {
		panic(err)
	}
}
