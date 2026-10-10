import { useTranslation } from "react-i18next"
import {
  IconAlertTriangle,
  IconArrowUpCircle,
  IconCircleCheck,
  IconMessageChatbot,
  IconMessageReport,
  IconPlus,
  IconRobot,
  IconSettings,
  IconWorld,
} from "@tabler/icons-react"

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

const nav: { id: Screen; labelKey: string; icon: typeof IconWorld }[] = [
  { id: "sites", labelKey: "shell.nav.sites", icon: IconWorld },
  { id: "assistant", labelKey: "nav.assistant", icon: IconMessageChatbot },
  // The screen id stays "assistants"; the label is "Connect apps". The chat is "assistant".
  { id: "assistants", labelKey: "nav.connectApps", icon: IconRobot },
  { id: "settings", labelKey: "shell.nav.settings", icon: IconSettings },
]

export function AppSidebar() {
  const { t } = useTranslation()
  const { screen, setScreen, sites, assistants, env, update, addSite, installFFC, ffcUpdate, sendFeedback } = useApp()
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
              aria-label={t("shell.sidebar.goToSites")}
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
          <SidebarGroupLabel>{t("shell.sidebar.manage")}</SidebarGroupLabel>
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
                    <span>{t(item.labelKey)}</span>
                  </SidebarMenuButton>
                  {item.id === "sites" && siteCount > 0 && <SidebarMenuBadge>{siteCount}</SidebarMenuBadge>}
                  {item.id === "assistants" && connected > 0 && <SidebarMenuBadge>{connected}</SidebarMenuBadge>}
                  {item.id === "settings" && update?.available && (
                    <SidebarMenuBadge>
                      <span className="bg-primary size-2 rounded-full" aria-hidden="true" />
                      <span className="sr-only">{t("shell.sidebar.updateAvailable", { version: update.latest })}</span>
                    </SidebarMenuBadge>
                  )}
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
        <SidebarGroup>
          <SidebarGroupLabel>{t("shell.sidebar.quickActions")}</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              <SidebarMenuItem>
                <SidebarMenuButton onClick={addSite}>
                  <IconPlus />
                  <span>{t("shell.sidebar.addSite")}</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>

      <SidebarFooter>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton onClick={sendFeedback} tooltip={t("shell.sidebar.feedbackTip")}>
              <IconMessageReport />
              <span>{t("shell.sidebar.feedback")}</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            {!ffc ? (
              <SidebarMenuSkeleton showIcon />
            ) : ffc.found && !ffc.error && ffcUpdate ? (
              <SidebarMenuButton onClick={installFFC} tooltip={t("shell.sidebar.ffcUpdateTip", { current: ffc.version, latest: ffcUpdate.latest })}>
                <IconArrowUpCircle />
                <span className="truncate">{t("shell.sidebar.ffcAvailable", { version: ffcUpdate.latest })}</span>
              </SidebarMenuButton>
            ) : ffc.found && !ffc.error ? (
              <SidebarMenuButton onClick={() => setScreen("settings")} tooltip={t("shell.sidebar.ffcLocation", { path: ffc.path })}>
                <IconCircleCheck />
                <span className="truncate">{t("shell.sidebar.ffcVersion", { version: ffc.version })}</span>
              </SidebarMenuButton>
            ) : (
              <SidebarMenuButton
                onClick={ffc.found ? () => setScreen("settings") : installFFC}
                tooltip={ffc.found ? t("shell.sidebar.ffcNoAnswerTip") : t("shell.sidebar.ffcInstallTip")}
                className="text-destructive"
              >
                <IconAlertTriangle />
                <span className="truncate">{ffc.found ? t("shell.sidebar.ffcNeedsAttention") : t("shell.sidebar.ffcMissing")}</span>
              </SidebarMenuButton>
            )}
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
    </Sidebar>
  )
}
