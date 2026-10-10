import * as React from "react"
import { useTranslation } from "react-i18next"

import { AppProvider, useApp, type AppActions, type Screen } from "@/app/app-context"
import { AppHeader } from "@/components/app-header"
import { AppSidebar } from "@/components/app-sidebar"
import { CommandPalette } from "@/components/command-palette"
import { ShortcutsDialog } from "@/components/shortcuts-dialog"
import { modalOpen } from "@/lib/modal"
import { InstallFFCDialog } from "@/components/install-ffc-dialog"
import { ScrollArea } from "@/components/ui/scroll-area"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { AddSiteSheet } from "@/screens/add-site-sheet"
import { AssistantScreen } from "@/screens/assistant/assistant-screen"
import { AssistantsScreen } from "@/screens/connect-apps-screen"
import { ConnectDialog, type ConnectTarget } from "@/screens/connect-dialog"
import { Onboarding } from "@/screens/onboarding"
import { SettingsScreen } from "@/screens/settings-screen"
import { SitesScreen } from "@/screens/sites-screen"

const ONBOARDED_KEY = "ffd-onboarded"

function readOnboarded() {
  try {
    return localStorage.getItem(ONBOARDED_KEY) === "1"
  } catch {
    return false
  }
}

function writeOnboarded() {
  try {
    localStorage.setItem(ONBOARDED_KEY, "1")
  } catch {
    // Shown again next time; harmless.
  }
}

export default function App() {
  const [screen, setScreen] = React.useState<Screen>("sites")
  const [addSiteOpen, setAddSiteOpen] = React.useState(false)
  const [connectTarget, setConnectTarget] = React.useState<ConnectTarget | null>(null)
  const [installOpen, setInstallOpen] = React.useState(false)
  const [shortcutsOpen, setShortcutsOpen] = React.useState(false)
  const [newConversationPending, setNewConversationPending] = React.useState(false)

  const actions = React.useMemo<AppActions>(
    () => ({
      addSite: () => setAddSiteOpen(true),
      connectAssistant: (client?: string, site?: string) => {
        setScreen("assistants")
        setConnectTarget({ client, site })
      },
      installFFC: () => setInstallOpen(true),
      newConversation: () => {
        setScreen("assistant")
        setNewConversationPending(true)
      },
      takeNewConversation: () => setNewConversationPending(false),
      openShortcuts: () => setShortcutsOpen(true),
    }),
    [],
  )

  return (
    <AppProvider actions={actions} screen={screen} setScreen={setScreen} newConversationPending={newConversationPending}>
      <Shell />
      <ShortcutsDialog open={shortcutsOpen} onOpenChange={setShortcutsOpen} />
      <AddSiteSheet open={addSiteOpen} onOpenChange={setAddSiteOpen} />
      <ConnectDialog target={connectTarget} onClose={() => setConnectTarget(null)} />
      <InstallFFCDialog open={installOpen} onOpenChange={setInstallOpen} />
    </AppProvider>
  )
}

function Shell() {
  const { t } = useTranslation()
  const { env, screen, setScreen, addSite, newConversation, openShortcuts } = useApp()
  const [paletteOpen, setPaletteOpen] = React.useState(false)
  const [onboarded, setOnboarded] = React.useState(readOnboarded)

  // First run: no config file yet and the welcome not seen.
  const welcome = !!env.data && !env.data.configExists && !onboarded

  // App-wide shortcuts. Letters by e.code (the physical key): with an Arabic
  // or other non-Latin layout e.key is that layout's letter. Punctuation by
  // e.key or e.code: on AZERTY "," is another key and "/" needs Shift.
  React.useEffect(() => {
    if (welcome) return
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.altKey) return
      if (e.code === "KeyK" && !e.shiftKey) {
        e.preventDefault()
        setPaletteOpen((o) => !o)
        return
      }
      // The others wait while a dialog or sheet is open (the palette closes first).
      if (modalOpen()) return
      if (e.code === "KeyN" && !e.shiftKey) {
        e.preventDefault()
        newConversation()
      } else if ((e.key === "," || e.code === "Comma") && !e.shiftKey) {
        e.preventDefault()
        setScreen("settings")
      } else if (e.key === "/" || e.code === "Slash") {
        e.preventDefault()
        openShortcuts()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [welcome, newConversation, openShortcuts, setScreen])

  // A new screen takes the focus to its heading, so keyboard and screen
  // reader users start at its top (not on the sidebar button they pressed).
  const firstScreen = React.useRef(true)
  React.useEffect(() => {
    if (firstScreen.current) {
      firstScreen.current = false
      return
    }
    const focusHeading = () => {
      const h = document.querySelector<HTMLElement>('[data-slot="sidebar-inset"] h1')
      if (!h) return
      h.tabIndex = -1
      h.focus({ preventScroll: true })
    }
    const frame = requestAnimationFrame(focusHeading)
    // A dialog that closed with the change (the palette) gives the focus back
    // to its trigger once its exit animation ends: take it again then, unless
    // the person already moved on inside the screen.
    const later = setTimeout(() => {
      const inset = document.querySelector('[data-slot="sidebar-inset"]')
      const active = document.activeElement
      const lost = !active || active === document.body || !inset?.contains(active) || active.tagName === "H1"
      if (lost && !modalOpen()) focusHeading()
    }, 350)
    return () => {
      cancelAnimationFrame(frame)
      clearTimeout(later)
    }
  }, [screen])

  if (welcome) {
    return (
      <Onboarding
        onDone={(add) => {
          writeOnboarded()
          setOnboarded(true)
          if (add) addSite()
        }}
      />
    )
  }

  return (
    <SidebarProvider style={{ "--sidebar-width": "14rem" } as React.CSSProperties} className="h-svh">
      <AppSidebar />
      <SidebarInset className="min-w-0 overflow-hidden">
        <AppHeader onOpenPalette={() => setPaletteOpen(true)} />
        {screen === "assistant" ? (
          // The chat scrolls its own panes and fills the window.
          <div className="min-h-0 flex-1">
            <h1 className="sr-only outline-none">{t("nav.assistant")}</h1>
            <AssistantScreen />
          </div>
        ) : (
          <ScrollArea className="min-h-0 flex-1">
            <div className="mx-auto w-full max-w-5xl p-6">
              {screen === "sites" && <SitesScreen />}
              {screen === "assistants" && <AssistantsScreen />}
              {screen === "settings" && <SettingsScreen />}
            </div>
          </ScrollArea>
        )}
      </SidebarInset>
      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
    </SidebarProvider>
  )
}
