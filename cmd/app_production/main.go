package main

import (
	"context"
	"os"

	"github.com/pdcgo/san_collection/san_config"
	"github.com/pdcgo/shared/configs"
	"github.com/pdcgo/shared/db_connect"
	"github.com/urfave/cli/v3"
	"gorm.io/gorm"
)

func NewDatabase(cfg *configs.AppConfig) (*gorm.DB, error) {
	return db_connect.NewProductionDatabase("inventory_service", &cfg.Database)
}

func NewProjectConfig() *san_config.ProjectConfig {
	return &san_config.ProjectConfig{ProjectID: os.Getenv("GOOGLE_CLOUD_PROJECT")}
}

func NewApp(
	serviceApiFunc ServiceApiFunc,
	syncLegacyFunc SyncLegacyFunc,
) *cli.Command {
	return &cli.Command{
		Name:           "inventory",
		DefaultCommand: "run",
		Commands: []*cli.Command{
			{
				Name:   "run",
				Action: cli.ActionFunc(serviceApiFunc),
			},
			{
				Name:   "sync-legacy",
				Action: cli.ActionFunc(syncLegacyFunc),
			},
		},
	}
}

func main() {
	app, err := InitializeApp()
	if err != nil {
		panic(err)
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		panic(err)
	}
}
