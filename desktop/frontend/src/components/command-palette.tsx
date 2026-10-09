import {
  IconDeviceDesktop,
  IconDownload,
  IconMoon,
  IconPlugConnected,
  IconPlus,
  IconRobot,
  IconSettings,
  IconSun,
  IconWorld,
  IconWorldCheck,
} from "@tabler/icons-react"

import { useTranslation } from "react-i18next"

import { useApp } from "@/app/app-context"
import { useTheme } from "@/app/theme"
import {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command"
import { toast } from "@/components/ui/toast"
import { appError, errorTitle } from "@/lib/errors"

export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t } = useTranslation()
  const { setScreen, addSite, connectAssistant, installFFC, sites, env, checkSite } = useApp()
  const { setTheme } = useTheme()

  const run = (fn: () => void) => () => {
    onOpenChange(false)
    fn()
  }

  async function check(name: string) {
    setScreen("sites")
    try {
      const res = await checkSite(name)
      if (!res) return
      toast.add({
        title: res.ok ? `${name} is connected` : `${name} did not answer as expected`,
        description: res.message,
        type: res.ok ? "success" : "error",
      })
    } catch (err) {
      const e = appError(err)
      toast.add({ title: errorTitle(e), description: e.message, type: "error" })
    }
  }

  const list = sites.data?.sites ?? []

  return (
    <CommandDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Command palette"
      description="Search for a page or an action"
    >
      <Command>
        <CommandInput placeholder="Type a command or search…" />
        <CommandList>
          <CommandEmpty>Nothing matches.</CommandEmpty>
          <CommandGroup heading="Go to">
            <CommandItem onSelect={run(() => setScreen("sites"))}>
              <IconWorld />
              Sites
            </CommandItem>
            <CommandItem onSelect={run(() => setScreen("assistants"))}>
              <IconRobot />
              {t("nav.connectApps")}
            </CommandItem>
            <CommandItem onSelect={run(() => setScreen("settings"))}>
              <IconSettings />
              Settings
            </CommandItem>
          </CommandGroup>
          <CommandSeparator />
          <CommandGroup heading="Actions">
            <CommandItem onSelect={run(addSite)}>
              <IconPlus />
              Add a site
            </CommandItem>
            <CommandItem onSelect={run(() => connectAssistant())}>
              <IconPlugConnected />
              Connect an assistant
            </CommandItem>
            {env.data && !env.data.ffc.found && (
              <CommandItem onSelect={run(installFFC)}>
                <IconDownload />
                Install the ffc helper
              </CommandItem>
            )}
          </CommandGroup>
          {list.length > 0 && (
            <>
              <CommandSeparator />
              <CommandGroup heading="Check a connection">
                {list.map((s) => (
                  <CommandItem key={s.name} value={`check ${s.name} ${s.url}`} onSelect={run(() => void check(s.name))}>
                    <IconWorldCheck />
                    {s.name}
                  </CommandItem>
                ))}
              </CommandGroup>
            </>
          )}
          <CommandSeparator />
          <CommandGroup heading="Theme">
            <CommandItem value="theme light" onSelect={run(() => setTheme("light"))}>
              <IconSun />
              Light
            </CommandItem>
            <CommandItem value="theme dark" onSelect={run(() => setTheme("dark"))}>
              <IconMoon />
              Dark
            </CommandItem>
            <CommandItem value="theme system" onSelect={run(() => setTheme("system"))}>
              <IconDeviceDesktop />
              Same as system
            </CommandItem>
          </CommandGroup>
        </CommandList>
      </Command>
    </CommandDialog>
  )
}
