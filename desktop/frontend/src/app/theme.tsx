import * as React from "react"

import { backend } from "@/lib/backend"

export type Theme = "light" | "dark" | "system"

const STORAGE_KEY = "ffd-theme"

function readTheme(): Theme {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    if (v === "light" || v === "dark" || v === "system") return v
  } catch {
    // Storage can be unavailable; fall back to the system theme.
  }
  return "system"
}

function writeTheme(t: Theme) {
  try {
    localStorage.setItem(STORAGE_KEY, t)
  } catch {
    // Not remembered this time; the choice still applies to this window.
  }
}

const query = () => window.matchMedia("(prefers-color-scheme: dark)")

// apply sets the page theme, then gives the native window the same
// background and shows it (it starts hidden; see main.go).
function apply(t: Theme) {
  const dark = t === "dark" || (t === "system" && query().matches)
  document.documentElement.classList.toggle("dark", dark)
  document.documentElement.style.colorScheme = dark ? "dark" : "light"
  backend.setWindowTheme(dark).catch(() => {
    // Best effort: main.go shows the window anyway after a few seconds.
  })
}

const ThemeContext = React.createContext<{ theme: Theme; setTheme: (t: Theme) => void } | null>(null)

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setThemeState] = React.useState<Theme>(readTheme)

  React.useEffect(() => {
    apply(theme)
    if (theme !== "system") return
    const mq = query()
    const onChange = () => apply("system")
    mq.addEventListener("change", onChange)
    return () => mq.removeEventListener("change", onChange)
  }, [theme])

  const setTheme = React.useCallback((t: Theme) => {
    writeTheme(t)
    setThemeState(t)
  }, [])

  const value = React.useMemo(() => ({ theme, setTheme }), [theme, setTheme])
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}

export function useTheme() {
  const ctx = React.useContext(ThemeContext)
  if (!ctx) throw new Error("useTheme must be used inside ThemeProvider")
  return ctx
}
