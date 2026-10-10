import {
  IconAlertTriangle,
  IconCircleCheck,
  IconCircleDashed,
  IconEye,
  IconPlugConnected,
  IconPlugConnectedX,
  IconRefresh,
  IconRobot,
  IconSettingsExclamation,
} from "@tabler/icons-react"
import * as React from "react"
import type { TFunction } from "i18next"
import { Trans, useTranslation } from "react-i18next"

import { useApp } from "@/app/app-context"
import { FFCMissingAlert, LoadError, PageHeader } from "@/components/page"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardAction, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { Assistant } from "@/lib/backend-types"
import { cn } from "cn"
import { DisconnectDialog } from "@/screens/disconnect-dialog"

// i18n keys: connectApps.status.connected connectApps.status.different connectApps.status.notConnected connectApps.status.error
const statusText: Record<
  string,
  { label: string; variant: "default" | "secondary" | "outline" | "destructive"; icon: typeof IconCircleCheck }
> = {
  connected: { label: "connectApps.status.connected", variant: "default", icon: IconCircleCheck },
  different: { label: "connectApps.status.different", variant: "secondary", icon: IconSettingsExclamation },
  not_connected: { label: "connectApps.status.notConnected", variant: "outline", icon: IconCircleDashed },
  error: { label: "connectApps.status.error", variant: "destructive", icon: IconAlertTriangle },
}

function notDetectedReason(a: Assistant, t: TFunction) {
  if (a.id === "claude-code") return t("connectApps.notFoundReasonClaudeCode")
  return t("connectApps.notFoundReason", { name: a.name })
}

export function AssistantsScreen() {
  const { t } = useTranslation()
  const { assistants, reloadAssistants, connectAssistant, sites, addSite } = useApp()
  const [disconnect, setDisconnect] = React.useState<Assistant | null>(null)
  const list = assistants.data?.assistants ?? []
  const noSites = sites.data && (sites.data.sites?.length ?? 0) === 0

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={t("connectApps.title")}
        description={t("connectApps.description")}
        actions={
          <Tooltip>
            <TooltipTrigger
              render={
                <Button
                  variant="outline"
                  size="icon"
                  aria-label={t("connectApps.lookAgain")}
                  onClick={reloadAssistants}
                  disabled={assistants.loading}
                />
              }
            >
              <IconRefresh />
            </TooltipTrigger>
            <TooltipContent>{t("connectApps.lookAgain")}</TooltipContent>
          </Tooltip>
        }
      />
      <FFCMissingAlert />

      {noSites && (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <IconRobot />
            </EmptyMedia>
            <EmptyTitle>{t("connectApps.addSiteFirst")}</EmptyTitle>
            <EmptyDescription>{t("connectApps.noSites")}</EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button onClick={addSite}>{t("connectApps.addSite")}</Button>
          </EmptyContent>
        </Empty>
      )}

      {assistants.error && !assistants.data ? (
        <LoadError title={t("connectApps.loadFailed")} error={assistants.error} onRetry={reloadAssistants} />
      ) : !assistants.data ? (
        <div className="grid grid-cols-2 gap-4 xl:grid-cols-3" aria-busy="true" aria-label={t("connectApps.loading")}>
          {[0, 1, 2, 3, 4].map((i) => (
            <Skeleton key={i} className="h-44 rounded-xl" />
          ))}
        </div>
      ) : (
        <div className="grid grid-cols-2 gap-4 xl:grid-cols-3">
          {list.map((a) => (
            <AssistantCard
              key={a.id}
              a={a}
              disabled={!!noSites}
              onConnect={() => connectAssistant(a.id)}
              onDisconnect={() => setDisconnect(a)}
            />
          ))}
        </div>
      )}

      <DisconnectDialog assistant={disconnect} onClose={() => setDisconnect(null)} />
    </div>
  )
}

function AssistantCard({
  a,
  disabled,
  onConnect,
  onDisconnect,
}: {
  a: Assistant
  disabled: boolean
  onConnect: () => void
  onDisconnect: () => void
}) {
  const { t } = useTranslation()
  const s = statusText[a.status] ?? statusText.error
  const hasEntry = a.status === "connected" || a.status === "different"

  return (
    <Card className={cn(!a.detected && "opacity-70")}>
      <CardHeader>
        <CardTitle>{a.name}</CardTitle>
        <CardDescription>
          {a.detected ? (
            t("connectApps.found")
          ) : (
            <Tooltip>
              <TooltipTrigger
                render={<span tabIndex={0} className="cursor-help underline decoration-dotted underline-offset-4" />}
              >
                {t("connectApps.notFound")}
              </TooltipTrigger>
              <TooltipContent className="max-w-64">{notDetectedReason(a, t)}</TooltipContent>
            </Tooltip>
          )}
        </CardDescription>
        <CardAction>
          <Badge variant={s.variant}>
            <s.icon data-icon="inline-start" />
            {t(s.label)}
          </Badge>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-1 flex-col gap-2 text-sm">
        {a.status === "connected" && (
          <>
            <p>
              {a.site ? (
                <Trans
                  i18nKey="connectApps.usesSite"
                  values={{ site: a.site }}
                  components={{ b: <span className="font-medium" /> }}
                />
              ) : (
                t("connectApps.usesDefault")
              )}
            </p>
            {a.readOnly && (
              <p className="text-muted-foreground flex items-center gap-1.5">
                <IconEye className="size-4" /> {t("connectApps.readOnly")}
              </p>
            )}
          </>
        )}
        {a.status === "different" && (
          <p className="text-muted-foreground">
            {t("connectApps.different")}
          </p>
        )}
        {a.status === "not_connected" && (
          <p className="text-muted-foreground">{t("connectApps.notSetUp")}</p>
        )}
        {a.status === "error" && (
          <Alert variant="destructive">
            <IconAlertTriangle />
            <AlertTitle>{t("connectApps.settingsUnreadable")}</AlertTitle>
            <AlertDescription>{a.error}</AlertDescription>
          </Alert>
        )}
      </CardContent>
      <CardFooter className="gap-2">
        <Button size="sm" onClick={onConnect} disabled={disabled || a.status === "error"}>
          <IconPlugConnected data-icon="inline-start" />
          {hasEntry ? t("connectApps.update") : t("connectApps.connect")}
        </Button>
        {hasEntry && (
          <Button size="sm" variant="ghost" onClick={onDisconnect}>
            <IconPlugConnectedX data-icon="inline-start" />
            {t("connectApps.disconnect")}
          </Button>
        )}
      </CardFooter>
    </Card>
  )
}
