package main

import (
	"embed"
	"log"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/services"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/cmd"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// The built frontend (frontend/dist) is embedded into the binary.
//
//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// A jq filter of an MCP tool runs in this executable started again; the
	// child must run the filter, not the app.
	cmd.RunJQChildIfRequested()

	configPath, err := services.ConfigPath()
	if err != nil {
		log.Fatalf("finding the ffc config path: %v", err)
	}

	host := &services.WailsHost{}
	ffc := services.NewFFCLocator()
	assistant := services.NewAssistantService(host, configPath)

	app := application.New(application.Options{
		Name:        "Foxmayn Frappe Desktop",
		Description: "Manage your Frappe and ERPNext sites and connect AI assistants to them.",
		Services: []application.Service{
			application.NewService(services.NewAppService(host, configPath, ffc)),
			application.NewService(services.NewSitesService(host, configPath)),
			application.NewService(services.NewAssistantsService(configPath, ffc)),
			application.NewService(assistant),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	host.App = app

	// The window is created hidden once the app runs, with the system theme's
	// background. The page shows it through AppService.SetWindowTheme after
	// applying the in-app theme, so neither theme flashes the other's colour.
	// If the page never calls (it failed to load), it is shown anyway.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		w := app.Window.NewWithOptions(application.WebviewWindowOptions{
			Title:            "Foxmayn Frappe Desktop",
			Width:            1100,
			Height:           720,
			MinWidth:         900,
			MinHeight:        600,
			BackgroundColour: services.WindowBackground(app.Env.IsDarkMode()),
			Hidden:           true,
			URL:              "/",
			// Files dropped on an element marked data-file-drop-target (the
			// chat composer) reach Go as paths; the composer names its
			// conversation in data-conv-id.
			EnableFileDrop: true,
		})
		w.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
			ctx := e.Context()
			convID := ""
			if t := ctx.DropTargetDetails(); t != nil {
				convID = t.Attributes["data-conv-id"]
			}
			go services.HandleDroppedFiles(assistant, convID, ctx.DroppedFiles())
		})
		time.AfterFunc(3*time.Second, func() {
			if !w.IsVisible() {
				w.Show()
			}
		})
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
