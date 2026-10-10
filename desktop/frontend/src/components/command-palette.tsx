import {
  IconDeviceDesktop,
  IconDownload,
  IconLanguage,
  IconMoon,
  IconPlugConnected,
  IconPlus,
  IconMessageChatbot,
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
import { LANGUAGES, setLanguage } from "@/i18n"
import { appError, errorTitle, localizedMessage } from "@/lib/errors"

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
        title: res.ok ? t("palette.connected", { name }) : t("palette.notAsExpected", { name }),
        description: localizedMessage(res),
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
      title={t("palette.title")}
      description={t("palette.description")}
    >
      <Command>
        <CommandInput placeholder={t("palette.placeholder")} />
        <CommandList>
          <CommandEmpty>{t("palette.empty")}</CommandEmpty>
          <CommandGroup heading={t("palette.goTo")}>
            <CommandItem value={`sites ${t("shell.nav.sites")}`} onSelect={run(() => setScreen("sites"))}>
              <IconWorld />
              {t("shell.nav.sites")}
            </CommandItem>
            <CommandItem value={`assistant ${t("nav.assistant")}`} onSelect={run(() => setScreen("assistant"))}>
              <IconMessageChatbot />
              {t("nav.assistant")}
            </CommandItem>
            <CommandItem value={`connect apps ${t("nav.connectApps")}`} onSelect={run(() => setScreen("assistants"))}>
              <IconRobot />
              {t("nav.connectApps")}
            </CommandItem>
            <CommandItem value={`settings ${t("shell.nav.settings")}`} onSelect={run(() => setScreen("settings"))}>
              <IconSettings />
              {t("shell.nav.settings")}
            </CommandItem>
          </CommandGroup>
          <CommandSeparator />
          <CommandGroup heading={t("palette.actions")}>
            <CommandItem value={`add a site ${t("palette.addSite")}`} onSelect={run(addSite)}>
              <IconPlus />
              {t("palette.addSite")}
            </CommandItem>
            <CommandItem value={`connect an app ${t("palette.connectApp")}`} onSelect={run(() => connectAssistant())}>
              <IconPlugConnected />
              {t("palette.connectApp")}
            </CommandItem>
            {env.data && !env.data.ffc.found && (
              <CommandItem value={`install the ffc helper ${t("palette.installFfc")}`} onSelect={run(installFFC)}>
                <IconDownload />
                {t("palette.installFfc")}
              </CommandItem>
            )}
          </CommandGroup>
          {list.length > 0 && (
            <>
              <CommandSeparator />
              <CommandGroup heading={t("palette.checkConnection")}>
                {list.map((s) => (
                  <CommandItem key={s.name} value={`check ${s.name} ${s.url} ${t("palette.checkConnection")}`} onSelect={run(() => void check(s.name))}>
                    <IconWorldCheck />
                    {s.name}
                  </CommandItem>
                ))}
              </CommandGroup>
            </>
          )}
          <CommandSeparator />
          <CommandGroup heading={t("language.label")}>
            {LANGUAGES.map((l) => (
              <CommandItem
                key={l.code}
                value={`language ${l.code} ${l.name} ${t(`language.names.${l.code}`)}`}
                onSelect={run(() => setLanguage(l.code))}
              >
                <IconLanguage />
                <span lang={l.code}>{l.name}</span>
              </CommandItem>
            ))}
          </CommandGroup>
          <CommandSeparator />
          <CommandGroup heading={t("theme.title")}>
            <CommandItem value={`theme light ${t("theme.light")}`} onSelect={run(() => setTheme("light"))}>
              <IconSun />
              {t("theme.light")}
            </CommandItem>
            <CommandItem value={`theme dark ${t("theme.dark")}`} onSelect={run(() => setTheme("dark"))}>
              <IconMoon />
              {t("theme.dark")}
            </CommandItem>
            <CommandItem value={`theme system ${t("theme.system")}`} onSelect={run(() => setTheme("system"))}>
              <IconDeviceDesktop />
              {t("theme.system")}
            </CommandItem>
          </CommandGroup>
        </CommandList>
      </Command>
    </CommandDialog>
  )
}
