import * as React from "react"

import { AppProvider, useApp, type AppActions, type Screen } from "@/app/app-context"
import { AppHeader } from "@/components/app-header"
import { AppSidebar } from "@/components/app-sidebar"
import { CommandPalette } from "@/components/command-palette"
import { InstallFFCDialog } from "@/components/install-ffc-dialog"
import { ScrollArea } from "@/components/ui/scroll-area"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { AddSiteSheet } from "@/screens/add-site-sheet"
import { AssistantScreen } from "@/screens/assistant/assistant-screen"
import { AssistantsScreen } from "@/screens/assistants-screen"
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

  const actions = React.useMemo<AppActions>(
    () => ({
      addSite: () => setAddSiteOpen(true),
      connectAssistant: (client?: string, site?: string) => {
        setScreen("assistants")
        setConnectTarget({ client, site })
      },
      installFFC: () => setInstallOpen(true),
    }),
    [],
  )

  return (
    <AppProvider actions={actions} screen={screen} setScreen={setScreen}>
      <Shell />
      <AddSiteSheet open={addSiteOpen} onOpenChange={setAddSiteOpen} />
      <ConnectDialog target={connectTarget} onClose={() => setConnectTarget(null)} />
      <InstallFFCDialog open={installOpen} onOpenChange={setInstallOpen} />
    </AppProvider>
  )
}

function Shell() {
  const { env, screen, addSite } = useApp()
  const [paletteOpen, setPaletteOpen] = React.useState(false)
  const [onboarded, setOnboarded] = React.useState(readOnboarded)

  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault()
        setPaletteOpen((o) => !o)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  // First run: no config file yet and the welcome not seen.
  if (env.data && !env.data.configExists && !onboarded) {
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
