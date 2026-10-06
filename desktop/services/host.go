// Package services holds the Go side of Foxmayn Frappe Desktop: the services
// the Wails runtime binds to the frontend (AppService, SitesService,
// AssistantsService) and the events they emit.
//
// The services never hand secrets to the frontend: tokens, API secrets and
// passwords stay in Go and in the config file. Errors that the UI acts on are
// *Error values (a code, a message for people, an optional technical detail).
package services

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Event names. Their payload types are registered in init, so the binding
// generator types them for the frontend.
const (
	// EventConfigChanged fires when config.yaml changes on disk, whoever
	// changed it (this app, the ffc CLI, an editor).
	EventConfigChanged = "config:changed"
	// EventSignInProgress reports the steps of a browser sign-in.
	EventSignInProgress = "signin:progress"
	// EventInstallerLog carries one line of the ffc installer's output.
	EventInstallerLog = "installer:log"
)

func init() {
	application.RegisterEvent[ConfigChanged](EventConfigChanged)
	application.RegisterEvent[SignInProgress](EventSignInProgress)
	application.RegisterEvent[InstallerLine](EventInstallerLog)
}

// ConfigChanged is the payload of EventConfigChanged.
type ConfigChanged struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
}

// SignInProgress is the payload of EventSignInProgress.
type SignInProgress struct {
	// Attempt is BrowserSignInRequest.Attempt of the sign-in it belongs to.
	Attempt string `json:"attempt,omitempty"`
	// Step is one of "starting", "registering", "browser", "finishing",
	// "saving", "done".
	Step string `json:"step"`
	// Message says what happens, for people.
	Message string `json:"message"`
	// AuthURL is the site's authorization page (step "browser"), so the UI
	// can offer to copy it when the browser did not open. It carries no
	// secret: the PKCE verifier stays in Go.
	AuthURL string `json:"authURL,omitempty"`
	// BrowserError is set when the browser could not be opened.
	BrowserError string `json:"browserError,omitempty"`
}

// InstallerLine is the payload of EventInstallerLog.
type InstallerLine struct {
	Line string `json:"line"`
}

// Host is what the services need from the desktop shell: emitting events and
// opening things with the operating system. main wires it to the Wails app;
// tests use a fake.
type Host interface {
	Emit(name string, data any)
	OpenURL(url string) error
	OpenFile(path string) error
}

// WailsHost is the Host of the running app. App is set after
// application.New, before the app runs.
type WailsHost struct {
	App *application.App
}

// Emit sends an event to the frontend.
func (h *WailsHost) Emit(name string, data any) {
	if h.App != nil {
		h.App.Event.Emit(name, data)
	}
}

// OpenURL opens url in the default browser.
func (h *WailsHost) OpenURL(url string) error {
	return h.App.Browser.OpenURL(url)
}

// OpenFile opens a file or folder with the operating system.
func (h *WailsHost) OpenFile(path string) error {
	return h.App.Browser.OpenFile(path)
}
