package main

import (
	"embed"
	"log"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/services"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// The built frontend (frontend/dist) is embedded into the binary.
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	configPath, err := services.DefaultConfigPath()
	if err != nil {
		log.Fatalf("finding the ffc config path: %v", err)
	}

	host := &services.WailsHost{}
	ffc := services.NewFFCLocator()

	app := application.New(application.Options{
		Name:        "Foxmayn Frappe Desktop",
		Description: "Manage your Frappe and ERPNext sites and connect AI assistants to them.",
		Services: []application.Service{
			application.NewService(services.NewAppService(host, configPath, ffc)),
			application.NewService(services.NewSitesService(host, configPath)),
			application.NewService(services.NewAssistantsService(configPath, ffc)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	host.App = app

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Foxmayn Frappe Desktop",
		Width:            1100,
		Height:           720,
		MinWidth:         900,
		MinHeight:        600,
		BackgroundColour: application.NewRGB(255, 255, 255),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
