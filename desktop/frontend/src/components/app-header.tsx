import { IconDeviceDesktop, IconMoon, IconSearch, IconSun } from "@tabler/icons-react"

import { useApp } from "@/app/app-context"
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

const titles = { sites: "Sites", assistants: "Assistants", settings: "Settings" }

export function useModKey() {
  const { env } = useApp()
  const mac = env.data ? env.data.os === "darwin" : /Mac/i.test(navigator.platform)
  return mac ? "⌘" : "Ctrl"
}

export function AppHeader({ onOpenPalette }: { onOpenPalette: () => void }) {
  const { screen, setScreen } = useApp()
  const { theme, setTheme } = useTheme()
  const mod = useModKey()
  const ThemeIcon = theme === "light" ? IconSun : theme === "dark" ? IconMoon : IconDeviceDesktop

  return (
    <header className="bg-background/95 flex h-12 shrink-0 items-center gap-2 border-b px-3 backdrop-blur">
      <Tooltip>
        <TooltipTrigger render={<SidebarTrigger />} />
        <TooltipContent>Show or hide the sidebar</TooltipContent>
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

      <div className="ml-auto flex items-center gap-2">
        <Button variant="outline" size="sm" onClick={onOpenPalette} className="text-muted-foreground">
          <IconSearch data-icon="inline-start" />
          Search
          <KbdGroup>
            <Kbd>{mod}</Kbd>
            <Kbd>K</Kbd>
          </KbdGroup>
        </Button>
        <DropdownMenu>
          <Tooltip>
            <TooltipTrigger
              render={<DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label="Theme" />} />}
            >
              <ThemeIcon />
            </TooltipTrigger>
            <TooltipContent>Theme</TooltipContent>
          </Tooltip>
          <DropdownMenuContent align="end" className="w-44">
            <DropdownMenuGroup>
              <DropdownMenuLabel>Theme</DropdownMenuLabel>
              <DropdownMenuRadioGroup value={theme} onValueChange={(v) => setTheme(v as Theme)}>
                <DropdownMenuRadioItem value="light">
                  <IconSun />
                  Light
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="dark">
                  <IconMoon />
                  Dark
                </DropdownMenuRadioItem>
                <DropdownMenuRadioItem value="system">
                  <IconDeviceDesktop />
                  Same as system
                </DropdownMenuRadioItem>
              </DropdownMenuRadioGroup>
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  )
}
