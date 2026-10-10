import { useTranslation } from "react-i18next"
import { IconDeviceDesktop, IconMoon, IconSearch, IconSun } from "@tabler/icons-react"

import { useApp, useOptionalApp } from "@/app/app-context"
import { useTheme, type Theme } from "@/app/theme"
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Kbd, KbdGroup } from "@/components/ui/kbd"
import { Separator } from "@/components/ui/separator"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"


export function useModKey() {
  const env = useOptionalApp()?.env
  const mac = env?.data ? env.data.os === "darwin" : /Mac/i.test(navigator.platform)
  return mac ? "⌘" : "Ctrl"
}

export function AppHeader({ onOpenPalette }: { onOpenPalette: () => void }) {
  const { t } = useTranslation()
  const { screen, setScreen } = useApp()
  const titles = { sites: t("shell.nav.sites"), assistant: t("nav.assistant"), assistants: t("connectApps.title"), settings: t("shell.nav.settings") }
  const { theme, setTheme } = useTheme()
  const mod = useModKey()
  const ThemeIcon = theme === "light" ? IconSun : theme === "dark" ? IconMoon : IconDeviceDesktop

  return (
    <header className="bg-background/95 flex h-12 shrink-0 items-center gap-2 border-b px-3 backdrop-blur">
      <Tooltip>
        <TooltipTrigger render={<SidebarTrigger />} />
        <TooltipContent>{t("shell.header.toggleSidebar")}</TooltipContent>
      </Tooltip>
      <Separator orientation="vertical" className="mx-1 data-[orientation=vertical]:h-4" />
      <Breadcrumb>
        <BreadcrumbList>
          <BreadcrumbItem>
            <BreadcrumbLink render={<button type="button" onClick={() => setScreen("sites")} />}>
              Frappe Desktop
            </BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem>
            <BreadcrumbPage>{titles[screen]}</BreadcrumbPage>
          </BreadcrumbItem>
        </BreadcrumbList>
      </Breadcrumb>

      <div className="ms-auto flex items-center gap-2">
        <Button variant="outline" size="sm" onClick={onOpenPalette} className="text-muted-foreground">
          <IconSearch data-icon="inline-start" />
          {t("shell.header.search")}
          <KbdGroup>
            <Kbd>{mod}</Kbd>
            <Kbd>K</Kbd>
          </KbdGroup>
        </Button>
        <DropdownMenu>
          <Tooltip>
            <TooltipTrigger
              render={<DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={t("theme.title")} />} />}
            >
              <ThemeIcon />
            </TooltipTrigger>
            <TooltipContent>{t("theme.title")}</TooltipContent>
          </Tooltip>
          <DropdownMenuContent align="end" className="w-44">
            <DropdownMenuGroup>
              <DropdownMenuLabel>{t("theme.title")}</DropdownMenuLabel>
              <DropdownMenuRadioGroup value={theme} onValueChange={(v) => setTheme(v as Theme)}>
                <DropdownMenuRadioItem value="light">
                  <IconSun />
                  {t("theme.light")}
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="dark">
                  <IconMoon />
                  {t("theme.dark")}
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="system">
                  <IconDeviceDesktop />
                  {t("theme.system")}
                </DropdownMenuRadioItem>
              </DropdownMenuRadioGroup>
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  )
}
