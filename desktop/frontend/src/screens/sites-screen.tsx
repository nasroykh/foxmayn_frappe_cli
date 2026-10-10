import {
  IconAlertTriangle,
  IconDots,
  IconLink,
  IconLock,
  IconLockOpen,
  IconPencil,
  IconPlugConnected,
  IconPlus,
  IconStar,
  IconTrash,
  IconWorld,
  IconWorldCheck,
} from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { useApp } from "@/app/app-context"
import { FFCMissingAlert, LoadError, PageHeader } from "@/components/page"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuGroup,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card"
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemTitle } from "@/components/ui/item"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { toast } from "@/components/ui/toast"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { Site } from "@/lib/backend-types"
import { appError, errorTitle, localizedMessage } from "@/lib/errors"
import { authLabel, hostOf, initials, timeAgo } from "@/lib/labels"
import { RemoveSiteDialog, RenameSiteDialog, SiteURLDialog } from "@/screens/site-dialogs"
import { WSLAlert } from "@/screens/wsl-alert"
import { backend } from "@/lib/backend"

type Pending = { kind: "rename" | "url" | "remove"; site: Site } | null

export function SitesScreen() {
  const { t } = useTranslation()
  const { sites, reloadSites, addSite } = useApp()
  const [pending, setPending] = React.useState<Pending>(null)
  const list = sites.data?.sites ?? []

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t("sites.title")}
        description={t("sites.description")}
        actions={
          list.length > 0 && (
            <Button onClick={addSite}>
              <IconPlus data-icon="inline-start" />
              {t("sites.add")}
            </Button>
          )
        }
      />
      <FFCMissingAlert />
      <WSLAlert />

      {sites.error && !sites.data ? (
        <LoadError title={t("sites.loadFailed")} error={sites.error} onRetry={reloadSites} />
      ) : !sites.data ? (
        <SitesSkeleton />
      ) : list.length === 0 ? (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <IconWorld />
            </EmptyMedia>
            <EmptyTitle>{t("sites.empty.title")}</EmptyTitle>
            <EmptyDescription>
              {t("sites.empty.body")}
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button onClick={addSite}>
              <IconPlus data-icon="inline-start" />
              {t("sites.empty.add")}
            </Button>
          </EmptyContent>
        </Empty>
      ) : (
        <ItemGroup className="gap-2">
          {list.map((site) => (
            <SiteRow key={site.name} site={site} onAction={(kind) => setPending({ kind, site })} />
          ))}
        </ItemGroup>
      )}

      <RenameSiteDialog site={pending?.kind === "rename" ? pending.site : null} onClose={() => setPending(null)} />
      <SiteURLDialog site={pending?.kind === "url" ? pending.site : null} onClose={() => setPending(null)} />
      <RemoveSiteDialog site={pending?.kind === "remove" ? pending.site : null} onClose={() => setPending(null)} />
    </div>
  )
}

function SitesSkeleton() {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col gap-2" aria-busy="true" aria-label={t("sites.loading")}>
      {[0, 1, 2].map((i) => (
        <div key={i} className="flex items-center gap-3 rounded-lg border p-3">
          <Skeleton className="size-8 rounded-full" />
          <div className="flex flex-1 flex-col gap-2">
            <Skeleton className="h-4 w-40" />
            <Skeleton className="h-3 w-64" />
          </div>
          <Skeleton className="h-7 w-28" />
        </div>
      ))}
    </div>
  )
}

function SiteRow({ site, onAction }: { site: Site; onAction: (kind: "rename" | "url" | "remove") => void }) {
  const { t } = useTranslation()
  const { checking, checkSite, connectAssistant, reloadSites } = useApp()
  const busy = checking.has(site.name)
  const check = site.lastCheck

  async function runCheck() {
    try {
      const res = await checkSite(site.name)
      if (!res) return
      toast.add({
        title: res.ok ? t("sites.toast.connected", { name: site.name }) : t("sites.toast.rejected", { name: site.name }),
        description: localizedMessage(res),
        type: res.ok ? "success" : "error",
      })
    } catch (err) {
      const e = appError(err)
      toast.add({ title: errorTitle(e), description: e.message, type: "error" })
    }
  }

  async function makeDefault() {
    try {
      await backend.setDefault(site.name)
      await reloadSites()
      toast.add({
        title: t("sites.toast.nowDefault", { name: site.name }),
        description: t("sites.toast.nowDefaultHint"),
        type: "success",
      })
    } catch (err) {
      const e = appError(err)
      toast.add({ title: errorTitle(e), description: e.message, type: "error" })
    }
  }

  const actions = [
    { id: "check", label: t("sites.action.check"), icon: IconWorldCheck, run: runCheck, disabled: busy },
    { id: "default", label: t("sites.action.makeDefault"), icon: IconStar, run: makeDefault, disabled: site.isDefault },
    {
      id: "connect",
      label: t("sites.action.connect"),
      icon: IconPlugConnected,
      run: () => connectAssistant(undefined, site.name),
    },
  ]
  const edits = [
    { id: "rename", label: t("sites.action.rename"), icon: IconPencil, run: () => onAction("rename") },
    {
      id: "url",
      label: t("sites.action.changeAddress"),
      icon: IconLink,
      run: () => onAction("url"),
      disabled: site.auth === "oauth",
    },
  ]

  return (
    <Item variant="outline" role="listitem" className="bg-card">
      <ContextMenu>
        <ContextMenuTrigger render={<div className="flex min-w-0 flex-1 items-center gap-2.5" />}>
          <ItemMedia>
            <Avatar>
              <AvatarFallback>{initials(site.name)}</AvatarFallback>
            </Avatar>
          </ItemMedia>
          <ItemContent className="min-w-0">
            <ItemTitle className="flex-wrap">
              <HoverCard>
                <HoverCardTrigger
                  delay={300}
                  render={
                    <button
                      type="button"
                      className="focus-visible:ring-ring/50 truncate rounded-sm outline-none focus-visible:ring-3"
                    />
                  }
                >
                  {site.name}
                </HoverCardTrigger>
                <HoverCardContent align="start" className="w-80">
                  <SiteCard site={site} />
                </HoverCardContent>
              </HoverCard>
              {site.isDefault && <Badge>{t("sites.default")}</Badge>}
              <Badge variant="secondary">{authLabel(site.auth)}</Badge>
              {site.plainHTTP && (
                <Tooltip>
                  <TooltipTrigger render={<Badge variant="destructive" tabIndex={0} />}>
                    <IconLockOpen data-icon="inline-start" />
                    {t("sites.notEncrypted")}
                  </TooltipTrigger>
                  <TooltipContent>{t("sites.notEncryptedHint")}</TooltipContent>
                </Tooltip>
              )}
            </ItemTitle>
            <ItemDescription className="truncate">
              {hostOf(site.url)}
              {check && (
                <>
                  {" · "}
                  {check.ok ? t("sites.connectedAs", { user: check.user }) : t("sites.lastCheckFailed")} · {timeAgo(check.checkedAt)}
                </>
              )}
            </ItemDescription>
          </ItemContent>
        </ContextMenuTrigger>
        <ContextMenuContent className="w-52">
          <ContextMenuGroup>
            {actions.map((a) => (
              <ContextMenuItem key={a.id} onClick={a.run} disabled={a.disabled}>
                <a.icon />
                {a.label}
              </ContextMenuItem>
            ))}
          </ContextMenuGroup>
          <ContextMenuSeparator />
          <ContextMenuGroup>
            {edits.map((a) => (
              <ContextMenuItem key={a.id} onClick={a.run} disabled={a.disabled}>
                <a.icon />
                {a.label}
              </ContextMenuItem>
            ))}
          </ContextMenuGroup>
          <ContextMenuSeparator />
          <ContextMenuGroup>
            <ContextMenuItem variant="destructive" onClick={() => onAction("remove")}>
              <IconTrash />
              {t("sites.action.remove")}
            </ContextMenuItem>
          </ContextMenuGroup>
        </ContextMenuContent>
      </ContextMenu>
      <ItemActions>
        <Button variant="outline" size="sm" onClick={runCheck} disabled={busy}>
          {busy ? <Spinner data-icon="inline-start" /> : <IconWorldCheck data-icon="inline-start" />}
          {busy ? t("sites.checking") : t("sites.check")}
        </Button>
        <DropdownMenu>
          <Tooltip>
            <TooltipTrigger
              render={
                <DropdownMenuTrigger
                  render={<Button variant="ghost" size="icon-sm" aria-label={t("sites.moreFor", { name: site.name })} />}
                />
              }
            >
              <IconDots />
            </TooltipTrigger>
            <TooltipContent>{t("sites.more")}</TooltipContent>
          </Tooltip>
          <DropdownMenuContent align="end" className="w-52">
            <DropdownMenuGroup>
              {actions.map((a) => (
                <DropdownMenuItem key={a.id} onClick={a.run} disabled={a.disabled}>
                  <a.icon />
                  {a.label}
                </DropdownMenuItem>
              ))}
            </DropdownMenuGroup>
            <DropdownMenuSeparator />
            <DropdownMenuGroup>
              {edits.map((a) => (
                <DropdownMenuItem key={a.id} onClick={a.run} disabled={a.disabled}>
                  <a.icon />
                  {a.label}
                </DropdownMenuItem>
              ))}
            </DropdownMenuGroup>
            <DropdownMenuSeparator />
            <DropdownMenuGroup>
              <DropdownMenuItem variant="destructive" onClick={() => onAction("remove")}>
                <IconTrash />
                {t("sites.action.remove")}
              </DropdownMenuItem>
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      </ItemActions>
    </Item>
  )
}

function SiteCard({ site }: { site: Site }) {
  const { t } = useTranslation()
  const check = site.lastCheck
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-3">
        <Avatar size="lg">
          <AvatarFallback>{initials(site.name)}</AvatarFallback>
        </Avatar>
        <div className="flex min-w-0 flex-col">
          <span className="truncate font-medium">{site.name}</span>
          <span className="text-muted-foreground truncate text-xs">{site.url}</span>
        </div>
      </div>
      <Separator />
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
        <dt className="text-muted-foreground">{t("sites.card.signIn")}</dt>
        <dd>{authLabel(site.auth)}</dd>
        {site.username && (
          <>
            <dt className="text-muted-foreground">{t("sites.card.username")}</dt>
            <dd className="truncate">{site.username}</dd>
          </>
        )}
        <dt className="text-muted-foreground">{t("sites.card.signedInAs")}</dt>
        <dd className="truncate">{check?.ok ? check.user : check ? t("sites.card.checkFailed") : t("sites.card.notChecked")}</dd>
        <dt className="text-muted-foreground">{t("sites.card.security")}</dt>
        <dd className="flex items-center gap-1">
          {site.plainHTTP ? <IconLockOpen className="text-destructive size-3.5" /> : <IconLock className="size-3.5" />}
          {site.plainHTTP ? t("sites.card.http") : t("sites.card.https")}
        </dd>
      </dl>
      {check && !check.ok && (
        <Alert variant="destructive">
          <IconAlertTriangle />
          <AlertTitle>{t("sites.lastCheckFailed")}</AlertTitle>
          <AlertDescription>{localizedMessage(check)}</AlertDescription>
        </Alert>
      )}
    </div>
  )
}
