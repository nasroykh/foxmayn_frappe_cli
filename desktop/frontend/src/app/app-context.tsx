import * as React from "react"

import { backend } from "@/lib/backend"
import { copy } from "@/components/copy-field"
import { toast } from "@/components/ui/toast"
import type { AssistantList, CheckResult, Environment, FFCUpdate, SiteList, UpdateInfo } from "@/lib/backend-types"
import i18n from "@/i18n"
import { appError, errorTitle, type AppError } from "@/lib/errors"

const UPDATE_CHECKED_KEY = "ffd-update-checked"
const UPDATE_INTERVAL_MS = 24 * 60 * 60 * 1000

// One startup check per page load, even when React mounts the provider twice.
let startupCheckStarted = false

/** True when the startup check is due: never ran, ran over 24 h ago, or storage is unusable. */
function updateCheckDue() {
  // The mock preview checks on every load so its scenarios are always visible.
  if (import.meta.env.MODE === "mock") return true
  try {
    const last = Number(localStorage.getItem(UPDATE_CHECKED_KEY))
    const age = Date.now() - last
    return !Number.isFinite(last) || last <= 0 || age < 0 || age >= UPDATE_INTERVAL_MS
  } catch {
    return true
  }
}

function markUpdateChecked() {
  try {
    localStorage.setItem(UPDATE_CHECKED_KEY, String(Date.now()))
  } catch {
    // Checked again next start; harmless.
  }
}

export type Screen = "sites" | "assistant" | "assistants" | "settings"

/** A value loaded from the backend: null until the first answer. */
export interface Loaded<T> {
  data: T | null
  error: AppError | null
  loading: boolean
}

function useLoader<T>(load: () => Promise<T>) {
  const [state, setState] = React.useState<Loaded<T>>({ data: null, error: null, loading: true })
  const seq = React.useRef(0)
  const reload = React.useCallback(async () => {
    const n = ++seq.current
    setState((s) => ({ ...s, loading: true }))
    try {
      const data = await load()
      if (n === seq.current) setState({ data, error: null, loading: false })
      return data
    } catch (err) {
      if (n === seq.current) setState((s) => ({ data: s.data, error: appError(err), loading: false }))
      return null
    }
  }, [load])
  return [state, reload] as const
}

interface AppContextValue {
  env: Loaded<Environment>
  sites: Loaded<SiteList>
  assistants: Loaded<AssistantList>
  reloadEnv: () => Promise<Environment | null>
  reloadSites: () => Promise<SiteList | null>
  reloadAssistants: () => Promise<AssistantList | null>

  /** The latest update check that succeeded: null until one has. */
  update: UpdateInfo | null
  /** True while a check runs. */
  checkingUpdate: boolean
  /** Asks GitHub for a newer release. Rejects with the error; the caller shows it. */
  checkUpdate: () => Promise<UpdateInfo>
  /** Opens the release page in the browser. */
  downloadUpdate: (info: UpdateInfo) => Promise<void>
  /**
   * A newer ffc release than the installed ffc, from the latest check: null
   * when there is none, or once the installed version changed (updated).
   */
  ffcUpdate: FFCUpdate | null

  /** Sites whose connection check is running. */
  checking: ReadonlySet<string>
  checkSite: (name: string) => Promise<CheckResult | null>

  screen: Screen
  setScreen: (s: Screen) => void
  /** Opens the add-site sheet. */
  addSite: () => void
  /** Opens the connect dialog of an assistant, optionally pinned to a site. */
  connectAssistant: (client?: string, site?: string) => void
  /** Opens the ffc install dialog. */
  installFFC: () => void
}

const AppContext = React.createContext<AppContextValue | null>(null)

export interface AppActions {
  addSite: () => void
  connectAssistant: (client?: string, site?: string) => void
  installFFC: () => void
}

export function AppProvider({
  children,
  actions,
  screen,
  setScreen,
}: {
  children: React.ReactNode
  actions: AppActions
  screen: Screen
  setScreen: (s: Screen) => void
}) {
  const [env, reloadEnv] = useLoader(backend.environment)
  const [sites, reloadSites] = useLoader(backend.listSites)
  const [assistants, reloadAssistants] = useLoader(backend.listAssistants)
  const [checking, setChecking] = React.useState<ReadonlySet<string>>(new Set())

  React.useEffect(() => {
    void reloadEnv()
    void reloadSites()
    void reloadAssistants()
    // Edits made elsewhere (the CLI, another window) show up live.
    return backend.onConfigChanged(() => {
      void reloadSites()
      void reloadAssistants()
      void reloadEnv()
    })
  }, [reloadEnv, reloadSites, reloadAssistants])

  const [update, setUpdate] = React.useState<UpdateInfo | null>(null)
  const [checkingUpdate, setCheckingUpdate] = React.useState(false)

  const checkUpdate = React.useCallback(async () => {
    setCheckingUpdate(true)
    try {
      const info = await backend.checkForUpdate()
      setUpdate(info)
      return info
    } finally {
      setCheckingUpdate(false)
    }
  }, [])

  const downloadUpdate = React.useCallback(async (info: UpdateInfo) => {
    try {
      await backend.openWebsite(info.url)
    } catch (err) {
      const e = appError(err)
      toast.add({ title: errorTitle(e), description: e.message, type: "error" })
    }
  }, [])

  // At start, at most once a day: look quietly, tell only when there is
  // something. Errors are ignored here (the About tab shows them on demand).
  // The time is stored first so a double effect run (StrictMode) asks once.
  React.useEffect(() => {
    if (startupCheckStarted || !updateCheckDue()) return
    startupCheckStarted = true
    markUpdateChecked()
    void checkUpdate().then(
      (info) => {
        if (info.available && info.installCommand) {
          // The terminal command avoids the warnings an unsigned download meets.
          const terminal = info.installCommand.startsWith("curl") ? "Terminal" : "PowerShell"
          toast.add({
            title: i18n.t("shell.update.availableTitle", { version: info.latest }),
            description: i18n.t("shell.update.installHint", { terminal }),
            type: "info",
            timeout: 20000,
            actionProps: {
              children: i18n.t("shell.update.copyCommand"),
              onClick: () => void copy(info.installCommand, i18n.t("shell.update.commandCopied")),
            },
          })
        } else if (info.available) {
          toast.add({
            title: i18n.t("shell.update.availableTitle", { version: info.latest }),
            description: i18n.t("shell.update.githubHint"),
            type: "info",
            timeout: 20000,
            actionProps: { children: i18n.t("common.download"), onClick: () => void downloadUpdate(info) },
          })
        }
        if (info.ffc.available) {
          toast.add({
            title: i18n.t("shell.update.ffcTitle", { version: info.ffc.latest }),
            description: i18n.t("shell.update.ffcBody", { current: info.ffc.current }),
            type: "info",
            timeout: 20000,
            actionProps: { children: i18n.t("common.update"), onClick: actions.installFFC },
          })
        }
      },
      () => {},
    )
  }, [checkUpdate, downloadUpdate, actions.installFFC])

  // The check's answer stays valid only for the ffc it saw: after an update
  // (or a new ffc found) the installed version differs, and nothing is offered.
  const ffcUpdate =
    update?.ffc.available && env.data?.ffc.found && env.data.ffc.version === update.ffc.current ? update.ffc : null

  const checkSite = React.useCallback(
    async (name: string) => {
      setChecking((s) => new Set(s).add(name))
      try {
        const res = await backend.check(name)
        await reloadSites()
        return res
      } finally {
        setChecking((s) => {
          const next = new Set(s)
          next.delete(name)
          return next
        })
      }
    },
    [reloadSites],
  )

  const value = React.useMemo<AppContextValue>(
    () => ({
      env,
      sites,
      assistants,
      reloadEnv,
      reloadSites,
      reloadAssistants,
      update,
      checkingUpdate,
      checkUpdate,
      downloadUpdate,
      ffcUpdate,
      checking,
      checkSite,
      screen,
      setScreen,
      ...actions,
    }),
    [
      env,
      sites,
      assistants,
      reloadEnv,
      reloadSites,
      reloadAssistants,
      update,
      checkingUpdate,
      checkUpdate,
      downloadUpdate,
      ffcUpdate,
      checking,
      checkSite,
      screen,
      setScreen,
      actions,
    ],
  )
  return <AppContext.Provider value={value}>{children}</AppContext.Provider>
}

export function useApp() {
  const ctx = React.useContext(AppContext)
  if (!ctx) throw new Error("useApp must be used inside AppProvider")
  return ctx
}
