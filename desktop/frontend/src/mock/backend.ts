// A fake backend for `npm run dev:mock`: the UI in a plain browser, with
// realistic data and no Go. vite.config.ts aliases "@/lib/backend" to this
// file only in mock mode, so it is never part of a production build.
//
// Query parameters pick a scenario:
//   ?sites=none       no config file and no sites (first run, onboarding)
//   ?ffc=missing      the ffc helper is not installed
//   ?ffc=broken       ffc is found but does not answer --version
//   ?wsl=1            a WSL ffc config is detected (Windows)
//   ?os=darwin        macOS paths and install command
//   ?signin=noreg     the site cannot register an OAuth client (Frappe v15)
//   ?signin=denied    the browser sign-in is refused
//   ?install=fail     the ffc installer fails
//   ?update=available a newer release exists (toast, sidebar dot, About tab)
//   ?update=fail      the update check fails (shown by Settings > About)
//   ?slow=1           every call takes about 1.5 s (loading states)
//   ?fail=sites       listing sites fails
//   ?fail=assistants  listing assistants fails
//   ?disconnect=unavailable  Disconnect answers "not available yet"
import type {
  AddedSite,
  ApplyResult,
  Assistant,
  AssistantList,
  Backend,
  Cancellable,
  CheckResult,
  ConfigChanged,
  ConnectRequest,
  Environment,
  FFCInfo,
  Preview,
  SignInProgress,
  Site,
  SiteList,
} from "@/lib/backend-types"
import type { AppError } from "@/lib/errors"

const params = new URLSearchParams(window.location.search)
const os = params.get("os") === "darwin" ? "darwin" : "windows"
const slow = params.get("slow") === "1"
const home = os === "darwin" ? "/Users/nas" : "C:\\Users\\nas"
const sep = os === "darwin" ? "/" : "\\"
const configPath = [home, ".config", "ffc", "config.yaml"].join(sep)
const ffcPath = os === "darwin" ? "/Users/nas/.local/bin/ffc" : "C:\\Users\\nas\\AppData\\Local\\Programs\\ffc\\ffc.exe"
const installCommand =
  os === "darwin"
    ? "curl -fsSL https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/v1.11.0/install.sh | sh"
    : 'powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/v1.11.0/install.ps1 | iex"'

function fail(code: AppError["code"], message: string, extra: Partial<AppError> = {}): never {
  const err = new Error(message) as Error & { cause: AppError }
  err.cause = { code, message, ...extra }
  throw err
}

const wait = (ms: number) => new Promise((r) => setTimeout(r, slow ? ms + 1500 : ms))

type Listener<T> = Set<(v: T) => void>
const configListeners: Listener<ConfigChanged> = new Set()
const signInListeners: Listener<SignInProgress> = new Set()
const installListeners: Listener<string> = new Set()

function on<T>(set: Listener<T>, cb: (v: T) => void) {
  set.add(cb)
  return () => {
    set.delete(cb)
  }
}

interface StoredSite extends Site {
  user: string
}

let configExists = params.get("sites") !== "none"
let sites: StoredSite[] = configExists
  ? [
      {
        name: "acme-prod",
        url: "https://erp.acme.example",
        auth: "oauth",
        isDefault: true,
        plainHTTP: false,
        user: "nas@acme.example",
      },
      {
        name: "acme-staging",
        url: "https://staging.acme.example",
        auth: "apikey",
        isDefault: false,
        plainHTTP: false,
        user: "integration@acme.example",
      },
      {
        name: "local-bench",
        url: "http://localhost:8000",
        auth: "password",
        isDefault: false,
        plainHTTP: true,
        username: "Administrator",
        user: "Administrator",
      },
    ]
  : []

let ffc: FFCInfo =
  params.get("ffc") === "missing"
    ? { found: false, path: "", version: "", updatable: false }
    : params.get("ffc") === "broken"
      ? { found: true, path: ffcPath, version: "", error: "exit status 1", updatable: false }
      : { found: true, path: ffcPath, version: "1.10.0", updatable: true }

function changed() {
  configExists = true
  for (const cb of configListeners) cb({ path: configPath, exists: true })
}

function publicSite(s: StoredSite): Site {
  const { user: _user, ...rest } = s
  return rest
}

function find(name: string) {
  const s = sites.find((x) => x.name === name)
  if (!s) fail("not_found", `There is no site called "${name}".`)
  return s
}

const nameRE = /^[A-Za-z0-9][A-Za-z0-9._-]*$/

function checkName(name: string, field = "name") {
  if (!name.trim()) fail("invalid", "Enter a name for this site.", { field })
  if (!nameRE.test(name)) {
    fail("invalid", "Use letters, numbers, dots, dashes or underscores, starting with a letter or number.", { field })
  }
}

function normalizeURL(raw: string) {
  let u = raw.trim()
  if (!u) fail("invalid", "Enter the address of your Frappe site.", { field: "url" })
  if (!/^https?:\/\//i.test(u)) u = "https://" + u
  try {
    const parsed = new URL(u)
    if (!parsed.hostname.includes(".") && parsed.hostname !== "localhost") throw new Error()
    return parsed.origin
  } catch {
    fail("invalid", "That does not look like a web address. Try something like erp.example.com.", { field: "url" })
  }
}

function save(added: StoredSite, replace: boolean): AddedSite {
  const exists = sites.some((s) => s.name === added.name)
  if (exists && !replace) fail("exists", `A site called "${added.name}" already exists.`, { field: "name" })
  const isDefault = sites.length === 0 || (exists && sites.find((s) => s.name === added.name)!.isDefault)
  sites = [...sites.filter((s) => s.name !== added.name), { ...added, isDefault }].sort((a, b) =>
    a.name.localeCompare(b.name),
  )
  changed()
  return {
    name: added.name,
    url: added.url,
    auth: added.auth,
    user: added.user,
    isDefault,
    replaced: exists,
    registered: added.auth === "oauth",
  }
}

let connections: Record<string, { site: string; readOnly: boolean } | "foreign"> = {
  "claude-desktop": { site: "", readOnly: false },
  cursor: "foreign",
}

const clients: { id: string; name: string; detected: boolean; path: string }[] = [
  {
    id: "claude-desktop",
    name: "Claude Desktop",
    detected: true,
    path:
      os === "darwin"
        ? "/Users/nas/Library/Application Support/Claude/claude_desktop_config.json"
        : "C:\\Users\\nas\\AppData\\Roaming\\Claude\\claude_desktop_config.json",
  },
  { id: "claude-code", name: "Claude Code", detected: true, path: [home, ".claude.json"].join(sep) },
  { id: "cursor", name: "Cursor", detected: true, path: [home, ".cursor", "mcp.json"].join(sep) },
  {
    id: "vscode",
    name: "VS Code",
    detected: false,
    path:
      os === "darwin"
        ? "/Users/nas/Library/Application Support/Code/User/mcp.json"
        : "C:\\Users\\nas\\AppData\\Roaming\\Code\\User\\mcp.json",
  },
  { id: "codex", name: "Codex", detected: false, path: [home, ".codex", "config.toml"].join(sep) },
]

function hint(id: string) {
  switch (id) {
    case "claude-desktop":
      return "Quit Claude Desktop completely and open it again to load the change."
    case "claude-code":
      return "Start a new Claude Code session to load the change."
    case "cursor":
      return "Cursor picks the change up on its own; reload the window if the tools do not show."
    case "vscode":
      return "Reload the VS Code window to load the change."
    default:
      return "Restart Codex to load the change."
  }
}

function serverArgs(req: { site: string; readOnly: boolean }) {
  const args = [ffcPath, "mcp"]
  if (req.site) args.push("--site", req.site)
  if (req.readOnly) args.push("--read-only")
  return args
}

function entryJSON(args: string[], indent: string) {
  const lines = [
    `${indent}"frappe": {`,
    `${indent}  "command": ${JSON.stringify(args[0])},`,
    `${indent}  "args": [${args
      .slice(1)
      .map((a) => JSON.stringify(a))
      .join(", ")}]`,
    `${indent}}`,
  ]
  return lines
}

function checkClient(id: string) {
  const c = clients.find((x) => x.id === id)
  if (!c) fail("invalid", `Unknown assistant "${id}".`, { field: "client" })
  return c
}

function cancellable<T>(run: (signal: AbortSignal) => Promise<T>): Cancellable<T> {
  const ctl = new AbortController()
  const p = run(ctl.signal) as Cancellable<T>
  p.cancel = () => ctl.abort()
  return p
}

let signIn: AbortController | null = null

export const backend: Backend = {
  async environment(): Promise<Environment> {
    await wait(200)
    const wsl = params.get("wsl") === "1" && os === "windows"
    return {
      appVersion: "0.1.0",
      os,
      configPath,
      configExists,
      ffc,
      installCommand,
      wsl: {
        detected: wsl,
        distros: wsl ? ["Ubuntu-24.04"] : [],
        paths: wsl ? ["\\\\wsl.localhost\\Ubuntu-24.04\\home\\nas\\.config\\ffc\\config.yaml"] : [],
      },
    }
  },
  async refreshFFC() {
    await wait(300)
    return ffc
  },
  installFFC() {
    return cancellable(async (signal) => {
      const lines = [
        "Detecting platform... " + (os === "darwin" ? "darwin/arm64" : "windows/amd64"),
        "Fetching the latest release from GitHub...",
        "Downloading ffc v1.10.0 (" + (os === "darwin" ? "ffc_1.10.0_darwin_arm64.tar.gz" : "ffc_1.10.0_windows_amd64.zip") + ")...",
        "Verified the release signature and the SHA-256 checksum.",
        "Installed ffc v1.10.0 to " + ffcPath,
      ]
      for (const [i, line] of lines.entries()) {
        await wait(500)
        if (signal.aborted) fail("cancelled", "The installation was cancelled.")
        if (params.get("install") === "fail" && i === 2) {
          for (const cb of installListeners) cb("error: downloading: read tcp: connection reset by peer")
          fail("failed", "ffc could not be installed.", { detail: "downloading: read tcp: connection reset by peer" })
        }
        for (const cb of installListeners) cb(line)
      }
      ffc = {
        found: true,
        path: ffcPath,
        version: params.get("ffc-update") === "available" ? "1.12.0" : "1.10.0",
        updatable: true,
      }
      return ffc
    })
  },
  async checkForUpdate() {
    await wait(600)
    if (params.get("update") === "fail") {
      fail("network", "GitHub could not be reached. Check your internet connection.", {
        detail: "dial tcp: lookup api.github.com: no such host",
      })
    }
    const available = params.get("update") === "available"
    // ?ffc-update=available: the installed ffc (1.10.0 until the mock install) is behind 1.12.0.
    const ffcLatest = params.get("ffc-update") === "available" ? "1.12.0" : ffc.version
    return {
      available,
      current: "0.1.0",
      latest: available ? "0.2.0" : "0.1.0",
      url: "https://github.com/nasroykh/foxmayn_frappe_cli/releases/tag/desktop-v" + (available ? "0.2.0" : "0.1.0"),
      publishedAt: "2026-10-20T09:30:00Z",
      installCommand: !available
        ? ""
        : os === "windows"
          ? 'powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/desktop-v0.2.0/install-desktop.ps1 | iex"'
          : "curl -fsSL https://raw.githubusercontent.com/nasroykh/foxmayn_frappe_cli/desktop-v0.2.0/install-desktop.sh | sh",
      ffc: { available: ffc.found && ffcLatest !== ffc.version, current: ffc.version, latest: ffcLatest },
    }
  },
  async openWebsite(url) {
    window.open(url, "_blank", "noopener")
  },
  async openConfigFolder() {
    if (!configExists) fail("not_found", "The settings folder does not exist yet. It is created when you add a site.")
  },
  async openFFCFolder() {
    if (!ffc.found) fail("ffc_missing", "The ffc helper is not installed.")
  },
  async setWindowTheme() {
    // No native window in the browser preview.
  },

  async listSites(): Promise<SiteList> {
    await wait(400)
    if (params.get("fail") === "sites")
      fail("failed", "Could not read the settings file.", {
        detail: "yaml: line 4: mapping values are not allowed in this context",
      })
    return {
      configPath,
      configExists,
      defaultSite: sites.find((s) => s.isDefault)?.name ?? "",
      sites: sites.map(publicSite),
    }
  },
  async validate(name, url) {
    await wait(60)
    let nameError = ""
    let urlError = ""
    let siteURL = ""
    try {
      checkName(name)
    } catch (e) {
      nameError = (e as { cause: AppError }).cause.message
    }
    try {
      siteURL = normalizeURL(url)
    } catch (e) {
      urlError = (e as { cause: AppError }).cause.message
    }
    return {
      name,
      url: siteURL,
      nameError,
      urlError,
      exists: sites.some((s) => s.name === name),
      plainHTTP: siteURL.startsWith("http://"),
      ok: !nameError && !urlError,
    }
  },
  async addWithAPIKey(req) {
    checkName(req.name)
    const url = normalizeURL(req.url)
    if (!req.apiKey) fail("invalid", "Enter the API key.", { field: "apiKey" })
    if (!req.apiSecret) fail("invalid", "Enter the API secret.", { field: "apiSecret" })
    await wait(900)
    if (req.apiSecret === "wrong")
      fail("auth", "The site did not accept this API key and secret.", {
        detail: "401 Unauthorized: AuthenticationError",
      })
    return save(
      {
        name: req.name,
        url,
        auth: "apikey",
        isDefault: false,
        plainHTTP: url.startsWith("http://"),
        user: "api-user@acme.example",
      },
      req.replace,
    )
  },
  async addWithPassword(req) {
    checkName(req.name)
    const url = normalizeURL(req.url)
    if (!req.username) fail("invalid", "Enter your username or email.", { field: "username" })
    if (!req.password) fail("invalid", "Enter your password.", { field: "password" })
    await wait(900)
    if (req.password === "wrong")
      fail("auth", "The username or password is not right.", { detail: "401: Invalid login credentials" })
    return save(
      {
        name: req.name,
        url,
        auth: "password",
        isDefault: false,
        plainHTTP: url.startsWith("http://"),
        username: req.username,
        user: req.username,
      },
      req.replace,
    )
  },
  async signInWithBrowser(req) {
    signIn?.abort()
    const ctl = new AbortController()
    signIn = ctl
    checkName(req.name)
    const url = normalizeURL(req.url)
    if (sites.some((s) => s.name === req.name) && !req.replace)
      fail("exists", `A site called "${req.name}" already exists.`, { field: "name" })
    const emit = (ev: SignInProgress) => {
      if (!ctl.signal.aborted) for (const cb of signInListeners) cb({ ...ev, attempt: req.attempt })
    }
    const step = async (ev: SignInProgress, ms: number) => {
      emit(ev)
      await wait(ms)
      if (ctl.signal.aborted) fail("cancelled", "Sign-in was cancelled.")
    }
    await step({ step: "starting", message: "Getting ready…" }, 400)
    if (!req.clientID) {
      await step({ step: "registering", message: "Setting up the app on your site…" }, 700)
      if (params.get("signin") === "noreg") {
        fail("no_registration", "This site does not let apps register themselves.", {
          unsupported: true,
          redirectURI: "http://127.0.0.1:53682/callback",
          detail: "GET /.well-known/oauth-authorization-server: 404 Not Found",
        })
      }
    }
    await step(
      {
        step: "browser",
        message: "Continue in your browser…",
        authURL: `${url}/api/method/frappe.integrations.oauth2.authorize?client_id=4f1c2a&response_type=code&redirect_uri=http%3A%2F%2F127.0.0.1%3A53682%2Fcallback`,
      },
      3500,
    )
    if (params.get("signin") === "denied")
      fail("auth", "The sign-in was refused in the browser.", { detail: "access_denied" })
    await step({ step: "finishing", message: "Finishing sign-in…" }, 600)
    await step({ step: "saving", message: "Saving the site…" }, 300)
    emit({ step: "done", message: "Signed in." })
    signIn = null
    return save(
      {
        name: req.name,
        url,
        auth: "oauth",
        isDefault: false,
        plainHTTP: url.startsWith("http://"),
        user: "nas@acme.example",
      },
      req.replace,
    )
  },
  async cancelSignIn() {
    signIn?.abort()
    signIn = null
  },
  async reopenSignInPage() {},
  async check(name): Promise<CheckResult> {
    const s = find(name)
    await wait(800)
    const checkedAt = new Date().toISOString()
    const result: CheckResult =
      s.name === "acme-staging"
        ? { ok: false, user: "", message: "The site did not accept the saved API key.", code: "auth", checkedAt }
        : { ok: true, user: s.user, message: `Connected as ${s.user}.`, checkedAt }
    sites = sites.map((x) => (x.name === name ? { ...x, lastCheck: result } : x))
    return result
  },
  async setDefault(name) {
    find(name)
    await wait(150)
    sites = sites.map((s) => ({ ...s, isDefault: s.name === name }))
    changed()
  },
  async rename(oldName, newName) {
    find(oldName)
    checkName(newName)
    if (sites.some((s) => s.name === newName))
      fail("exists", `A site called "${newName}" already exists.`, { field: "name" })
    await wait(200)
    sites = sites
      .map((s) => (s.name === oldName ? { ...s, name: newName } : s))
      .sort((a, b) => a.name.localeCompare(b.name))
    changed()
  },
  async changeURL(name, raw) {
    const s = find(name)
    if (s.auth === "oauth")
      fail("unavailable", "Sites that use browser sign-in cannot change address. Remove the site and add it again.")
    const url = normalizeURL(raw)
    await wait(200)
    sites = sites.map((x) => (x.name === name ? { ...x, url, plainHTTP: url.startsWith("http://") } : x))
    changed()
    return url
  },
  async remove(name) {
    const s = find(name)
    await wait(700)
    const wasDefault = s.isDefault
    sites = sites.filter((x) => x.name !== name)
    let newDefault = ""
    if (wasDefault && sites.length > 0) {
      sites[0] = { ...sites[0], isDefault: true }
      newDefault = sites[0].name
    }
    changed()
    return { revoked: s.auth === "oauth", wasDefault, newDefault }
  },

  async listAssistants(): Promise<AssistantList> {
    await wait(500)
    if (params.get("fail") === "assistants") fail("failed", "Could not read the assistants' settings.")
    const assistants: Assistant[] = clients.map((c) => {
      const conn = connections[c.id]
      return {
        id: c.id,
        name: c.name,
        detected: c.detected,
        status: conn === "foreign" ? "different" : conn ? "connected" : "not_connected",
        site: conn && conn !== "foreign" ? conn.site : "",
        readOnly: conn && conn !== "foreign" ? conn.readOnly : false,
        configPath: c.path,
        hint: hint(c.id),
      }
    })
    return { ffc, entryName: "frappe", assistants }
  },
  async preview(req: ConnectRequest): Promise<Preview> {
    const c = checkClient(req.client)
    if (!ffc.found) fail("ffc_missing", "Install the ffc helper first. Assistants run it to reach your sites.")
    await wait(300)
    const old = connections[c.id]
    const args = serverArgs(req)
    const same = old && old !== "foreign" && old.site === req.site && old.readOnly === req.readOnly
    const top = c.id === "vscode" ? "servers" : "mcpServers"
    const oldLines =
      old === "foreign"
        ? ['    "frappe": {', '      "command": "node",', '      "args": ["frappe-mcp/server.js"]', "    }"]
        : old
          ? entryJSON(serverArgs(old), "    ")
          : []
    const newLines = entryJSON(args, "    ")
    const diff = same
      ? ""
      : [
          `--- ${c.path}`,
          `+++ ${c.path}`,
          "@@ -1,5 +1,5 @@",
          " {",
          `   "${top}": {`,
          ...oldLines.map((l) => "-" + l),
          ...newLines.map((l) => "+" + l),
          "   }",
          " }",
        ].join("\n")
    const claudeCode = c.id === "claude-code"
    return {
      client: c.id,
      entryName: "frappe",
      path: c.path,
      changed: !same,
      replaces: !!old && !same,
      createsFile: !c.detected,
      diff,
      commands: claudeCode
        ? [
            `claude mcp add-json --scope user frappe '${JSON.stringify({ type: "stdio", command: args[0], args: args.slice(1) })}'`,
          ]
        : null,
      server: args,
      canApply: true,
      hint: hint(c.id),
    }
  },
  async connect(req): Promise<ApplyResult> {
    const c = checkClient(req.client)
    if (!ffc.found) fail("ffc_missing", "Install the ffc helper first.")
    if (req.site && !sites.some((s) => s.name === req.site))
      fail("invalid", `There is no site called "${req.site}".`, { field: "site" })
    await wait(600)
    const had = connections[c.id]
    connections = { ...connections, [c.id]: { site: req.site, readOnly: req.readOnly } }
    const stamp = new Date().toISOString().replace(/[-:]/g, "").slice(0, 15).replace("T", "-")
    return {
      changed: true,
      backup: had && c.id !== "claude-code" ? `${c.path}.ffc-${stamp}.bak` : "",
      hint: hint(c.id),
    }
  },
  async previewDisconnect(client): Promise<Preview> {
    const c = checkClient(client)
    await wait(300)
    if (params.get("disconnect") === "unavailable") {
      fail(
        "unavailable",
        'Disconnecting is not available in this version yet. Remove the "frappe" entry from the assistant\'s settings by hand.',
      )
    }
    const old = connections[c.id]
    if (!old) fail("not_found", `${c.name} has no "frappe" entry.`)
    const oldLines =
      old === "foreign"
        ? ['    "frappe": {', '      "command": "node",', '      "args": ["frappe-mcp/server.js"]', "    }"]
        : entryJSON(serverArgs(old), "    ")
    return {
      client: c.id,
      entryName: "frappe",
      path: c.path,
      changed: true,
      replaces: false,
      createsFile: false,
      diff: [
        `--- ${c.path}`,
        `+++ ${c.path}`,
        "@@ -1,7 +1,3 @@",
        " {",
        '   "mcpServers": {',
        ...oldLines.map((l) => "-" + l),
        "   }",
        " }",
      ].join("\n"),
      commands: c.id === "claude-code" ? ["claude mcp remove --scope user frappe"] : null,
      server: null,
      canApply: true,
      hint: hint(c.id),
    }
  },
  async disconnect(client): Promise<ApplyResult> {
    const c = checkClient(client)
    await wait(500)
    if (params.get("disconnect") === "unavailable") {
      fail(
        "unavailable",
        'Disconnecting is not available in this version yet. Remove the "frappe" entry from the assistant\'s settings by hand.',
      )
    }
    const { [c.id]: _gone, ...rest } = connections
    connections = rest
    return { changed: true, backup: "", hint: hint(c.id) }
  },

  onConfigChanged: (cb) => on(configListeners, cb),
  onSignInProgress: (cb) => on(signInListeners, cb),
  onInstallerLog: (cb) => on(installListeners, cb),

  async copyText(text) {
    try {
      await navigator.clipboard.writeText(text)
    } catch {
      // Clipboard access can be refused outside a secure context; the mock
      // has nothing better to do.
    }
  },
}
