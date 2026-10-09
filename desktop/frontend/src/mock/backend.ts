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
//   ?providers=none|nokey    no AI provider yet / one without a key
//   ?local=ollama     DetectLocal finds Ollama
// The assistant's scripts (what to type to get a read answer, an approval
// card with a diff, ffc's own card, an error, a pause) are described above
// the assistant section below.
import type {
  AddedSite,
  ApplyResult,
  Assistant,
  AssistantList,
  ApprovalOutcome,
  Backend,
  Cancellable,
  ChatApproval,
  ChatApprovalClosed,
  ChatDelta,
  ChatDone,
  ChatError,
  ChatMessage,
  ChatTool,
  ChatToolCall,
  ChatUsage,
  CheckResult,
  ConfigChanged,
  ConnectRequest,
  Conversation,
  Environment,
  FFCInfo,
  KeyStatus,
  Preview,
  Profile,
  ProviderInfo,
  RunStatus,
  SignInProgress,
  Site,
  SiteList,
  SiteSettings,
  ToolStatus,
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

// ---- The assistant ----
// A scripted model. What the user writes picks the script (a conversation in
// "ask" mode may change things; one in "read" mode never has the tools):
//   anything else          reads TD-0001 with get_doc, then answers
//   "update" / "change"    update_doc with an app card (a field diff);
//                          "same" in the text makes the diff empty
//   "delete"               delete_doc with ffc's own card
//   "create" / "add"       create_doc with an app card (no diff)
//   "error" / "fail"       streams a little, then chat:error and done(error)
//   "many" / "pause"       ends "paused" (step limit); continueRun resumes
// Query parameters: ?providers=none (no provider yet), ?providers=nokey (one
// without a key), ?local=ollama (DetectLocal finds Ollama).
const providerParam = params.get("providers")
let providers: ProviderInfo[] =
  providerParam === "none"
    ? []
    : [
        {
          id: "anthropic",
          kind: "anthropic",
          label: "Anthropic",
          baseURL: "",
          defaultModel: "claude-sonnet-5-5",
          keySet: providerParam !== "nokey",
          keyLast4: providerParam !== "nokey" ? "a1B2" : "",
        },
      ]
const keys = new Map<string, string>(providerParam === "none" || providerParam === "nokey" ? [] : [["anthropic", "mock-key-a1B2"]])

interface MockRun {
  id: string
  convID: string
  cancelled: boolean
  settle?: (outcome: ApprovalOutcome) => void
}

interface MockConv {
  conv: Conversation
  messages: ChatMessage[]
  pausedRunID: string
}

const convs = new Map<string, MockConv>()
const runs = new Map<string, MockRun>()
const openCards = new Map<string, ChatApproval>() // by approvalID
const chat = {
  delta: new Set<(v: ChatDelta) => void>(),
  tool: new Set<(v: ChatTool) => void>(),
  approval: new Set<(v: ChatApproval) => void>(),
  closed: new Set<(v: ChatApprovalClosed) => void>(),
  usage: new Set<(v: ChatUsage) => void>(),
  done: new Set<(v: ChatDone) => void>(),
  error: new Set<(v: ChatError) => void>(),
}

function emit<T>(set: Set<(v: T) => void>, v: T) {
  for (const cb of set) cb(v)
}

let idSeq = 0
const newID = (p: string) => `${p}${(++idSeq).toString(16).padStart(4, "0")}`

function findConv(id: string) {
  const c = convs.get(id)
  if (!c) fail("not_found", "That conversation or run no longer exists.")
  return c
}

function activeRun(convID: string) {
  for (const r of runs.values()) if (r.convID === convID) return r
  return undefined
}

function findProvider(id: string) {
  const p = providers.find((x) => x.id === id)
  if (!p) fail("not_found", "That provider is not set up.", { field: "provider" })
  return p
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

// streamText sends text as small deltas; false when the run was stopped.
async function streamText(run: MockRun, text: string) {
  for (const chunk of text.match(/.{1,12}(\s|$)/g) ?? [text]) {
    if (run.cancelled) return false
    emit(chat.delta, { convID: run.convID, runID: run.id, text: chunk })
    await sleep(40)
  }
  return !run.cancelled
}

async function toolCall(run: MockRun, tool: string, site: string, summary: string, outcome: ToolStatus = "ok") {
  const callID = newID("call")
  emit(chat.tool, { convID: run.convID, runID: run.id, callID, tool, site, status: "running", summary })
  await sleep(500)
  const status: ToolStatus = run.cancelled ? "stopped" : outcome
  emit(chat.tool, { convID: run.convID, runID: run.id, callID, tool, site, status, summary })
  return { callID, status }
}

// ask opens a card and waits for the answer (or the stop).
function ask(run: MockRun, card: Omit<ChatApproval, "convID" | "runID" | "approvalID">) {
  const full: ChatApproval = { ...card, convID: run.convID, runID: run.id, approvalID: newID("appr") }
  openCards.set(full.approvalID, full)
  emit(chat.approval, full)
  return new Promise<ApprovalOutcome>((resolve) => {
    run.settle = (outcome) => {
      openCards.delete(full.approvalID)
      emit(chat.closed, { convID: run.convID, runID: run.id, approvalID: full.approvalID, outcome })
      run.settle = undefined
      resolve(outcome)
    }
  })
}

function finish(run: MockRun, c: MockConv, status: RunStatus, paused = false) {
  runs.delete(run.id)
  c.pausedRunID = paused ? run.id : ""
  emit(chat.usage, { convID: run.convID, runID: run.id, turn: 1, input: 1840, output: 212, cached: 1500 })
  emit(chat.done, { convID: run.convID, runID: run.id, status })
}

async function script(run: MockRun, c: MockConv, text: string) {
  const site = c.conv.site
  const lower = text.toLowerCase()
  const writes = c.conv.mode === "ask"
  const msg: ChatMessage = { id: newID("msg"), role: "assistant", text: "", tools: [], created: new Date().toISOString() }
  c.messages.push(msg)
  const say = async (t: string) => {
    msg.text += t
    return streamText(run, t)
  }
  const record = (callID: string, tool: string, status: ToolStatus, approval: ChatToolCall["approval"], summary: string) => {
    msg.tools.push({ id: callID, tool, site, status, approval, summary })
  }
  await sleep(300)

  if (/\b(error|fail)/.test(lower)) {
    if (!(await say("Let me check that. "))) return finish(run, c, "cancelled")
    emit(chat.error, {
      convID: run.convID,
      runID: run.id,
      error: { code: "failed", message: "The AI provider returned an error.", detail: "provider error (HTTP 529): overloaded" },
    })
    return finish(run, c, "error")
  }

  if (/\b(many|pause)/.test(lower)) {
    if (!(await say("This takes many lookups. "))) return finish(run, c, "cancelled")
    for (let i = 1; i <= 3; i++) {
      const t = await toolCall(run, "get_doc", site, `doctype=ToDo name=TD-000${i}`)
      record(t.callID, "get_doc", t.status, "", `doctype=ToDo name=TD-000${i}`)
    }
    return finish(run, c, "paused", true)
  }

  if (/\b(update|change|delete|create|add)\b/.test(lower) && !writes) {
    await say("I can only read in this conversation. Switch it to \"Ask before changes\" and I can propose the change for your approval.")
    return finish(run, c, "done")
  }

  const isDelete = /\bdelete\b/.test(lower)
  const isCreate = /\b(create|add)\b/.test(lower)
  const isUpdate = /\b(update|change)\b/.test(lower)
  if (isDelete || isCreate || isUpdate) {
    if (isUpdate) {
      const t = await toolCall(run, "get_doc", site, "doctype=ToDo name=TD-0001")
      record(t.callID, "get_doc", t.status, "", "doctype=ToDo name=TD-0001")
    }
    if (run.cancelled) return finish(run, c, "cancelled")
    const tool = isDelete ? "delete_doc" : isCreate ? "create_doc" : "update_doc"
    const same = /\bsame\b/.test(lower)
    const args: Record<string, unknown> = isCreate
      ? { doctype: "ToDo", data: { description: "Call the supplier", priority: "High" } }
      : isDelete
        ? { doctype: "ToDo", name: "TD-0001" }
        : {
            doctype: "ToDo",
            name: "TD-0001",
            data: same ? { status: "Open" } : { description: "Call the supplier back", status: "Closed" },
            if_unmodified: "2026-10-08 09:14:03.512",
          }
    const callID = newID("call")
    emit(chat.tool, { convID: run.convID, runID: run.id, callID, tool, site, status: "running", summary: "doctype=ToDo" })
    const outcome = await ask(run, {
      kind: isDelete ? "ffc" : "app",
      tool,
      site,
      doctypes: ["ToDo"],
      names: isCreate ? [] : ["TD-0001"],
      args,
      diff: isUpdate && !same
        ? [
            { field: "description", old: "Call the supplier", new: "Call the supplier back" },
            { field: "status", old: "Open", new: "Closed" },
          ]
        : [],
      noChanges: isUpdate && same,
      message: isDelete ? 'Delete ToDo "TD-0001"? This cannot be undone.' : "",
    })
    const approved = outcome === "approved"
    const status: ToolStatus = outcome === "cancelled" ? "stopped" : approved ? "ok" : "error"
    const approval: ChatToolCall["approval"] = isDelete
      ? approved
        ? "ffc-approved"
        : outcome === "declined"
          ? "ffc-declined"
          : "cancelled"
      : outcome
    const summary = approved ? "doctype=ToDo" : outcome === "declined" ? "The user declined this change. Nothing was changed." : "Stopped by the user before this change ran."
    emit(chat.tool, { convID: run.convID, runID: run.id, callID, tool, site, status, summary })
    record(callID, tool, status, approval, summary)
    if (outcome === "cancelled") return finish(run, c, "cancelled")
    await say(approved ? "Done. The change was saved." : "Understood, I left it as it was.")
    return finish(run, c, run.cancelled ? "cancelled" : "done")
  }

  if (!(await say("Let me look that up. "))) return finish(run, c, "cancelled")
  const t = await toolCall(run, "get_doc", site, "doctype=ToDo name=TD-0001")
  record(t.callID, "get_doc", t.status, "", "doctype=ToDo name=TD-0001")
  if (run.cancelled) return finish(run, c, "cancelled")
  await say("TD-0001 is a ToDo, \"Call the supplier\", status Open, assigned to you and due on 2026-10-12.")
  return finish(run, c, run.cancelled ? "cancelled" : "done")
}

// ---- profiles and site settings ----
// The presets mirror services/profiles.go; a profile only narrows the site.

function preset(p: Partial<Profile> & Pick<Profile, "id" | "name" | "mode">): Profile {
  return {
    preset: true,
    basedOn: "",
    toolsets: [],
    allowTools: [],
    allowDoctypes: [],
    denyDoctypes: [],
    allowMethods: [],
    denyMethods: [],
    denyTools: [],
    callMethod: false,
    stepLimit: 25,
    providerID: "",
    model: "",
    instructions: "",
    keepHistory: true,
    ...p,
  }
}

const presets: Profile[] = [
  preset({
    id: "explore",
    name: "Explore",
    mode: "read",
    instructions: "Explore the site: look things up, explain what you find and point to the documents you used.",
  }),
  preset({
    id: "accounts",
    name: "Accounts helper",
    mode: "ask",
    toolsets: ["core", "lifecycle", "erp"],
    instructions: "Help with invoices, payments and journal entries.",
  }),
  preset({ id: "site-admin", name: "Site admin", mode: "read", toolsets: ["core", "admin"], instructions: "Check the site's health." }),
  preset({
    id: "data-entry",
    name: "Data entry",
    mode: "ask",
    toolsets: ["core"],
    denyTools: ["delete_doc", "bulk_delete"],
    instructions: "Create and update documents the user describes.",
  }),
  preset({
    id: "local-model",
    name: "Local model",
    mode: "read",
    toolsets: ["core"],
    stepLimit: 15,
    instructions: "Use one tool at a time and keep answers short.",
  }),
]
let userProfiles: Profile[] = []
const siteSettings = new Map<string, SiteSettings>()

function findProfile(id: string): Profile | undefined {
  if (id === "") return undefined
  const p = presets.find((x) => x.id === id) ?? userProfiles.find((x) => x.id === id)
  if (!p) fail("not_found", "That profile no longer exists.", { field: "profile" })
  return p
}

function isLocal(p: ProviderInfo) {
  if (p.kind !== "ollama" && p.kind !== "lmstudio" && p.kind !== "custom") return false
  const fallback = p.kind === "ollama" ? "http://localhost:11434/v1" : p.kind === "lmstudio" ? "http://localhost:1234/v1" : ""
  try {
    const host = new URL(p.baseURL || fallback).hostname.toLowerCase()
    return host === "localhost" || host === "[::1]" || /^127\.\d+\.\d+\.\d+$/.test(host)
  } catch {
    return false
  }
}

// Ollama's cloud models ("gpt-oss:120b-cloud") run on ollama.com.
function isCloudModel(model: string) {
  const m = model.trim().toLowerCase()
  const i = Math.max(m.lastIndexOf(":"), m.lastIndexOf("-"))
  return i >= 0 && m.slice(i + 1) === "cloud"
}

function checkLocalOnly(site: string, providerID: string, model = "") {
  const p = findProvider(providerID)
  if (siteSettings.get(site)?.localOnly && (!isLocal(p) || isCloudModel(model || p.defaultModel))) {
    fail(
      "invalid",
      "This site is set to use local models only. Choose a provider that runs on this computer (Ollama, LM Studio or a local server).",
      { field: "provider" },
    )
  }
}

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

  async sendMessage(convID, text) {
    const c = findConv(convID)
    await wait(100)
    if (!text.trim()) fail("invalid", "Write a message first.", { field: "text" })
    if (activeRun(convID)) fail("invalid", "The assistant is still answering in this conversation.")
    c.pausedRunID = ""
    const now = new Date().toISOString()
    c.messages.push({ id: newID("msg"), role: "user", text, tools: [], created: now })
    if (!c.conv.title) c.conv.title = text.slice(0, 60)
    c.conv.updated = now
    const run: MockRun = { id: newID("run"), convID, cancelled: false }
    runs.set(run.id, run)
    void script(run, c, text)
    return run.id
  },
  async cancelRun(runID) {
    const run = runs.get(runID)
    if (!run) return
    run.cancelled = true
    run.settle?.("cancelled")
  },
  async answerApproval(convID, approvalID, approve) {
    const card = openCards.get(approvalID)
    const run = card && runs.get(card.runID)
    if (!card || !run || run.convID !== convID) fail("not_found", "This request was already answered or has ended.")
    run.settle?.(approve ? "approved" : "declined")
  },
  async continueRun(runID) {
    const c = [...convs.values()].find((x) => x.pausedRunID === runID)
    if (!c) fail("invalid", "This run is not paused.")
    c.pausedRunID = ""
    const run: MockRun = { id: runID, convID: c.conv.id, cancelled: false }
    runs.set(run.id, run)
    const msg: ChatMessage = { id: newID("msg"), role: "assistant", text: "", tools: [], created: new Date().toISOString() }
    c.messages.push(msg)
    void (async () => {
      msg.text = "All three are open ToDos and none is overdue."
      await streamText(run, msg.text)
      finish(run, c, run.cancelled ? "cancelled" : "done")
    })()
  },
  async pendingApprovals(convID) {
    return [...openCards.values()].filter((card) => runs.get(card.runID)?.convID === convID)
  },
  async newConversation(site, mode, providerID, model) {
    await wait(150)
    if (mode !== "read" && mode !== "ask") fail("invalid", 'Choose "Read only" or "Ask before changes".', { field: "mode" })
    if (!sites.some((s) => s.name === site)) fail("not_found", "That site is not in your list.", { field: "site" })
    const p = findProvider(providerID)
    checkLocalOnly(site, p.id, model)
    const now = new Date().toISOString()
    const conv: Conversation = {
      id: newID("conv"),
      title: "",
      site,
      mode,
      providerID: p.id,
      model: model.trim() || p.defaultModel,
      profileID: "",
      created: now,
      updated: now,
    }
    convs.set(conv.id, { conv, messages: [], pausedRunID: "" })
    return { ...conv }
  },
  async listConversations() {
    await wait(150)
    return [...convs.values()].map((c) => ({ ...c.conv })).sort((a, b) => b.updated.localeCompare(a.updated))
  },
  async getConversation(id) {
    await wait(150)
    const c = findConv(id)
    return {
      conversation: { ...c.conv },
      messages: c.messages.map((m) => ({ ...m, tools: m.tools.map((t) => ({ ...t })) })),
      activeRunID: activeRun(id)?.id ?? "",
      pausedRunID: c.pausedRunID,
    }
  },
  async deleteConversation(id) {
    findConv(id)
    if (activeRun(id)) fail("invalid", "The assistant is still answering in this conversation. Stop it first.")
    convs.delete(id)
  },
  async setConversationMode(id, mode) {
    const c = findConv(id)
    if (mode !== "read" && mode !== "ask") fail("invalid", 'Choose "Read only" or "Ask before changes".', { field: "mode" })
    if (activeRun(id)) fail("invalid", "The assistant is still answering in this conversation. Stop it first.")
    c.conv.mode = mode
  },

  async listProviders() {
    await wait(200)
    return providers.map((p) => ({ ...p }))
  },
  async saveProvider(p) {
    await wait(200)
    const kinds = ["anthropic", "openai", "gemini", "openrouter", "ollama", "lmstudio", "custom"]
    if (!kinds.includes(p.kind)) fail("invalid", "Choose a provider type.", { field: "kind" })
    const base = p.baseURL.trim()
    if (p.kind === "custom" && !base) fail("invalid", "Enter the server's address.", { field: "baseURL" })
    if (base && !/^https:\/\//.test(base) && !/^http:\/\/(localhost|127\.0\.0\.1|\[::1\])(:|\/|$)/.test(base)) {
      fail("invalid", "Use https, unless the server runs on this computer.", { field: "baseURL" })
    }
    const labels: Record<string, string> = { anthropic: "Anthropic", openai: "OpenAI", gemini: "Google Gemini", openrouter: "OpenRouter", ollama: "Ollama", lmstudio: "LM Studio", custom: "Custom" }
    const bases: Record<string, string> = { openrouter: "https://openrouter.ai/api/v1", ollama: "http://localhost:11434/v1", lmstudio: "http://localhost:1234/v1" }
    let id = p.id.trim() || p.kind
    for (let n = 2; !p.id.trim() && p.kind === "custom" && providers.some((x) => x.id === id); n++) id = `custom-${n}`
    const old = providers.find((x) => x.id === id)
    if (old && old.kind !== p.kind) fail("invalid", "A provider with this name exists with another type.", { field: "kind" })
    const saved: ProviderInfo = {
      id,
      kind: p.kind,
      label: p.label.trim() || labels[p.kind],
      baseURL: base || bases[p.kind] || "",
      defaultModel: p.defaultModel.trim() || (p.kind === "anthropic" ? "claude-sonnet-5-5" : ""),
      keySet: !!keys.get(id),
      keyLast4: old?.keyLast4 ?? "",
    }
    // A new address does not get the old key.
    if (old && keys.has(id) && old.baseURL !== saved.baseURL) {
      keys.delete(id)
      saved.keySet = false
      saved.keyLast4 = ""
      saved.keyCleared = true
    }
    providers = old ? providers.map((x) => (x.id === id ? saved : x)) : [...providers, saved]
    return { ...saved }
  },
  async deleteProvider(id) {
    findProvider(id)
    keys.delete(id)
    providers = providers.filter((p) => p.id !== id)
  },
  async setKey(providerID, key) {
    const p = findProvider(providerID)
    await wait(700)
    const k = key.trim()
    if (!k) fail("invalid", "Paste the API key.", { field: "key" })
    // A key starting with "bad" is refused, as the provider would.
    if (k.startsWith("bad")) fail("auth", "The provider did not accept the API key.", { field: "key" })
    keys.set(p.id, k)
    p.keySet = true
    p.keyLast4 = k.length >= 12 ? k.slice(-4) : ""
  },
  async keyStatus(providerID) {
    const p = findProvider(providerID)
    return { set: p.keySet, last4: p.keyLast4 || undefined } as KeyStatus
  },
  async detectLocal() {
    await wait(600)
    if (params.get("local") !== "ollama") return []
    return [{ id: "ollama", kind: "ollama", label: "Ollama", baseURL: "http://localhost:11434/v1", defaultModel: "", keySet: false, keyLast4: "" }]
  },
  async listModels(providerID) {
    const p = findProvider(providerID)
    await wait(500)
    if (p.kind === "anthropic") {
      if (!p.keySet) fail("auth", `Add the API key for ${p.label} first.`, { field: "key" })
      return [
        { id: "claude-opus-5-5", label: "Claude Opus 5.5", default: false },
        { id: "claude-sonnet-5-5", label: "Claude Sonnet 5.5", default: true },
        { id: "claude-haiku-5-5", label: "Claude Haiku 5.5", default: false },
      ]
    }
    if (p.kind === "openai" || p.kind === "gemini") {
      if (!p.keySet) fail("auth", `Add the API key for ${p.label} first.`, { field: "key" })
      const ids = p.kind === "openai" ? ["gpt-5.5", "gpt-5.5-mini", "gpt-5"] : ["gemini-3-pro", "gemini-3-flash", "gemini-2.5-pro"]
      return ids.map((id) => ({ id, label: id, default: false }))
    }
    return [
      { id: "llama3.1:8b", label: "llama3.1:8b", default: false },
      { id: "qwen2.5:14b", label: "qwen2.5:14b", default: false },
    ]
  },

  async listPresets() {
    return presets.map((p) => ({ ...p }))
  },
  async listProfiles() {
    await wait(100)
    return userProfiles.map((p) => ({ ...p })).sort((a, b) => a.name.localeCompare(b.name))
  },
  async saveProfile(p) {
    await wait(150)
    if (p.preset || presets.some((x) => x.id === p.id))
      fail("invalid", "Built-in profiles cannot be changed. Duplicate it to edit.", { field: "id" })
    if (p.id && !userProfiles.some((x) => x.id === p.id)) fail("not_found", "That profile no longer exists.", { field: "profile" })
    const name = p.name.trim()
    if (!name) fail("invalid", "Give the profile a name.", { field: "name" })
    const stepLimit = p.stepLimit || 25
    if (stepLimit < 1 || stepLimit > 100) fail("invalid", "Choose a step limit from 1 to 100.", { field: "stepLimit" })
    const clean = (l: string[]) => [...new Set(l.map((x) => x.trim()).filter(Boolean))].sort()
    const saved: Profile = {
      ...p,
      id: p.id || newID("prof"),
      name,
      preset: false,
      mode: p.mode === "ask" ? "ask" : "read",
      stepLimit,
      toolsets: clean(p.toolsets) as Profile["toolsets"],
      allowTools: clean(p.allowTools),
      allowDoctypes: clean(p.allowDoctypes),
      denyDoctypes: clean(p.denyDoctypes),
      allowMethods: clean(p.allowMethods),
      denyMethods: clean(p.denyMethods),
      denyTools: clean(p.denyTools),
    }
    userProfiles = [...userProfiles.filter((x) => x.id !== saved.id), saved]
    return { ...saved }
  },
  async deleteProfile(id) {
    if (presets.some((x) => x.id === id)) fail("invalid", "Built-in profiles cannot be deleted.", { field: "id" })
    findProfile(id)
    userProfiles = userProfiles.filter((x) => x.id !== id)
    for (const c of convs.values()) if (c.conv.profileID === id) c.conv.profileID = "explore"
  },
  async setConversationProfile(convID, profileID) {
    await wait(100)
    const c = findConv(convID)
    if (activeRun(convID)) fail("invalid", "The assistant is still answering in this conversation. Stop it first.")
    const p = findProfile(profileID)
    checkLocalOnly(c.conv.site, p?.providerID || c.conv.providerID, p?.providerID ? p.model : c.conv.model)
    if (p?.providerID) {
      c.conv.providerID = p.providerID
      c.conv.model = p.model || findProvider(p.providerID).defaultModel
    }
    c.conv.profileID = profileID
    return { ...c.conv }
  },
  async getSiteSettings(site) {
    const s = sites.find((x) => x.name === site)
    if (!s) fail("not_found", "That site is not in your list.", { field: "site" })
    return { ...(siteSettings.get(site) ?? { site, url: s.url, instructions: "", localOnly: false }) }
  },
  async saveSiteSettings(ss) {
    await wait(150)
    const s = sites.find((x) => x.name === ss.site)
    if (!s) fail("not_found", "That site is not in your list.", { field: "site" })
    if (ss.instructions.length > 4000) fail("invalid", "Those instructions are too long.", { field: "instructions" })
    const saved: SiteSettings = { site: ss.site, url: s.url, instructions: ss.instructions.trim(), localOnly: ss.localOnly }
    siteSettings.set(ss.site, saved)
    return { ...saved }
  },
  async promptPreview(convID) {
    await wait(200)
    const c = findConv(convID)
    const p = findProfile(c.conv.profileID)
    const mode = (p?.mode ?? "ask") === "ask" && c.conv.mode === "ask" ? "ask" : "read"
    const reads = ["list_sites", "ping", "get_doc", "list_docs", "count_docs", "get_schema", "search", "whoami"]
    const writes = ["create_doc", "update_doc", "delete_doc"]
    const deny = p?.denyTools ?? []
    const tools = [...reads, ...(mode === "ask" ? writes : [])].filter((t) => !deny.includes(t))
    const ss = siteSettings.get(c.conv.site)
    const parts = [
      'You are the Foxmayn Frappe assistant, working on the Frappe site "' + c.conv.site + '" for the person using this app.',
      "(ffc's instructions for the tools)",
    ]
    if (p?.instructions) parts.push("Instructions of the profile the user chose:\n" + p.instructions)
    if (ss?.instructions) parts.push("The user's instructions for this site:\n" + ss.instructions)
    return {
      system: parts.join("\n\n"),
      tools,
      mode,
      stepLimit: p?.stepLimit ?? 25,
      profileName: p?.name ?? "",
      localOnly: ss?.localOnly ?? false,
      siteContextPending: true,
    }
  },

  onChatDelta: (cb) => on(chat.delta, cb),
  onChatTool: (cb) => on(chat.tool, cb),
  onChatApproval: (cb) => on(chat.approval, cb),
  onChatApprovalClosed: (cb) => on(chat.closed, cb),
  onChatUsage: (cb) => on(chat.usage, cb),
  onChatDone: (cb) => on(chat.done, cb),
  onChatError: (cb) => on(chat.error, cb),

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
