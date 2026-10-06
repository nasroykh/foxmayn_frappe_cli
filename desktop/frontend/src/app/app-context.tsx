import * as React from "react"

import { backend } from "@/lib/backend"
import type { AssistantList, CheckResult, Environment, SiteList } from "@/lib/backend-types"
import { appError, type AppError } from "@/lib/errors"

export type Screen = "sites" | "assistants" | "settings"

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
      checking,
      checkSite,
      screen,
      setScreen,
      ...actions,
    }),
    [env, sites, assistants, reloadEnv, reloadSites, reloadAssistants, checking, checkSite, screen, setScreen, actions],
  )
  return <AppContext.Provider value={value}>{children}</AppContext.Provider>
}

export function useApp() {
  const ctx = React.useContext(AppContext)
  if (!ctx) throw new Error("useApp must be used inside AppProvider")
  return ctx
}
