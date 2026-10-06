import { IconAlertTriangle, IconCircleCheck, IconPlus, IconRobot, IconSettings, IconWorld } from "@tabler/icons-react"

import { useApp, type Screen } from "@/app/app-context"
import { BrandLogo } from "@/components/brand-logo"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSkeleton,
} from "@/components/ui/sidebar"

const nav: { id: Screen; label: string; icon: typeof IconWorld }[] = [
  { id: "sites", label: "Sites", icon: IconWorld },
  { id: "assistants", label: "Assistants", icon: IconRobot },
  { id: "settings", label: "Settings", icon: IconSettings },
]

export function AppSidebar() {
  const { screen, setScreen, sites, assistants, env, update, addSite, installFFC } = useApp()
  const siteCount = sites.data?.sites?.length ?? 0
  const connected = assistants.data?.assistants?.filter((a) => a.status === "connected").length ?? 0
  const ffc = env.data?.ffc

  return (
    <Sidebar>
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              size="lg"
              onClick={() => setScreen("sites")}
              aria-label="Foxmayn Frappe Desktop, go to Sites"
            >
              <BrandLogo size={28} className="rounded-md" />
              <span className="flex min-w-0 flex-col leading-tight">
                <span className="truncate font-semibold">Foxmayn</span>
                <span className="text-muted-foreground truncate text-xs">Frappe Desktop</span>
              </span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>

      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>Manage</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {nav.map((item) => (
                <SidebarMenuItem key={item.id}>
                  <SidebarMenuButton
                    isActive={screen === item.id}
                    onClick={() => setScreen(item.id)}
                    aria-current={screen === item.id ? "page" : undefined}
                  >
                    <item.icon />
                    <span>{item.label}</span>
                  </SidebarMenuButton>
                  {item.id === "sites" && siteCount > 0 && <SidebarMenuBadge>{siteCount}</SidebarMenuBadge>}
                  {item.id === "assistants" && connected > 0 && <SidebarMenuBadge>{connected}</SidebarMenuBadge>}
                  {item.id === "settings" && update?.available && (
                    <SidebarMenuBadge>
                      <span className="bg-primary size-2 rounded-full" aria-hidden="true" />
                      <span className="sr-only">Update {update.latest} available</span>
                    </SidebarMenuBadge>
                  )}
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
        <SidebarGroup>
          <SidebarGroupLabel>Quick actions</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              <SidebarMenuItem>
                <SidebarMenuButton onClick={addSite}>
                  <IconPlus />
                  <span>Add a site</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>

      <SidebarFooter>
        <SidebarMenu>
          <SidebarMenuItem>
            {!ffc ? (
              <SidebarMenuSkeleton showIcon />
            ) : ffc.found && !ffc.error ? (
              <SidebarMenuButton onClick={() => setScreen("settings")} tooltip={`ffc helper at ${ffc.path}`}>
                <IconCircleCheck />
                <span className="truncate">ffc helper {ffc.version}</span>
              </SidebarMenuButton>
            ) : (
              <SidebarMenuButton
                onClick={ffc.found ? () => setScreen("settings") : installFFC}
                tooltip={ffc.found ? "ffc was found but does not answer" : "Install the ffc helper"}
                className="text-destructive"
              >
                <IconAlertTriangle />
                <span className="truncate">{ffc.found ? "ffc helper needs attention" : "ffc helper missing"}</span>
              </SidebarMenuButton>
            )}
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
    </Sidebar>
  )
}
