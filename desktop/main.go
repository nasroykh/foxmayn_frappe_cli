package main

import (
	"embed"
	"log"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/services"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/cmd"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/desktopinstall"
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

	if pid, ok := afterUpdatePID(os.Args[1:]); ok {
		finishUpdate(pid)
	}

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
			application.NewService(services.NewActivityService(configPath)),
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
			// Off: Wails would pass dropped paths through the web view, where
			// a script could forge them. The composer reads dropped files with
			// the File API instead (services.AddDroppedFile).
			EnableFileDrop: false,
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

// afterUpdatePID reads "--after-update <pid>", the only argument the app
// accepts (a copy that InstallUpdate started). The pid must be a plain
// integer; every other argument is ignored.
func afterUpdatePID(args []string) (int, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "--after-update" {
			continue
		}
		pid, err := strconv.Atoi(args[i+1])
		if err != nil || pid <= 0 || strconv.Itoa(pid) != args[i+1] {
			return 0, false
		}
		return pid, true
	}
	return 0, false
}

// finishUpdate waits (at most 15 seconds) for the old copy to exit, then
// removes the files the update left beside this one (<name>.old, <name>.new).
func finishUpdate(pid int) {
	deadline := time.Now().Add(15 * time.Second)
	for services.ProcessRunning(pid) && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if t, err := desktopinstall.LocateSelf(runtime.GOOS, exe); err == nil {
		desktopinstall.CleanupOld(t)
	}
}
