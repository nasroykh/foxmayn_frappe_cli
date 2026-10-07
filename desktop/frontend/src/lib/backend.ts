// The real backend: the Wails bindings of the Go services.
import { Clipboard, Events } from "@wailsio/runtime"

import {
  AppService,
  AssistantsService,
  SitesService,
} from "../../bindings/github.com/nasroykh/foxmayn_frappe_cli/desktop/services"
import type { Backend, Cancellable } from "@/lib/backend-types"

// A new promise (never p itself), so setting cancel on it leaves p untouched.
function cancellable<T>(p: Promise<T> & { cancel(): unknown }): Cancellable<T> {
  const out = p.then((x) => x) as Cancellable<T>
  out.cancel = () => {
    void p.cancel()
  }
  return out
}

export const backend: Backend = {
  environment: () => AppService.Environment(),
  refreshFFC: () => AppService.RefreshFFC(),
  installFFC: () => cancellable(AppService.InstallFFC()),
  openWebsite: (url) => AppService.OpenWebsite(url),
  checkForUpdate: () => AppService.CheckForUpdate(),
  openConfigFolder: () => AppService.OpenConfigFolder(),
  openFFCFolder: () => AppService.OpenFFCFolder(),
  setWindowTheme: (dark) => AppService.SetWindowTheme(dark),

  listSites: () => SitesService.List(),
  validate: (name, url) => SitesService.Validate(name, url),
  addWithAPIKey: (req) => SitesService.AddWithAPIKey(req),
  addWithPassword: (req) => SitesService.AddWithPassword(req),
  signInWithBrowser: (req) => SitesService.SignInWithBrowser(req),
  cancelSignIn: () => SitesService.CancelSignIn(),
  reopenSignInPage: () => SitesService.ReopenSignInPage(),
  check: (name) => SitesService.Check(name),
  setDefault: (name) => SitesService.SetDefault(name),
  rename: (oldName, newName) => SitesService.Rename(oldName, newName),
  changeURL: (name, url) => SitesService.ChangeURL(name, url),
  remove: (name) => SitesService.Remove(name),

  listAssistants: () => AssistantsService.List(),
  preview: (req) => AssistantsService.Preview(req),
  connect: (req) => AssistantsService.Connect(req),
  previewDisconnect: (client) => AssistantsService.PreviewDisconnect(client),
  disconnect: (client) => AssistantsService.Disconnect(client),

  onConfigChanged: (cb) => Events.On("config:changed", (ev) => cb(ev.data)),
  onSignInProgress: (cb) => Events.On("signin:progress", (ev) => cb(ev.data)),
  onInstallerLog: (cb) => Events.On("installer:log", (ev) => cb(ev.data.line)),

  copyText: (text) => Clipboard.SetText(text),
}
