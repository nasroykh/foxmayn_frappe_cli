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

const statusText: Record<
  string,
  { label: string; variant: "default" | "secondary" | "outline" | "destructive"; icon: typeof IconCircleCheck }
> = {
  connected: { label: "Connected", variant: "default", icon: IconCircleCheck },
  different: { label: "Other settings", variant: "secondary", icon: IconSettingsExclamation },
  not_connected: { label: "Not connected", variant: "outline", icon: IconCircleDashed },
  error: { label: "Problem", variant: "destructive", icon: IconAlertTriangle },
}

function notDetectedReason(a: Assistant) {
  if (a.id === "claude-code")
    return "The claude command was not found on this computer. Install Claude Code, or connect anyway and it will be used once installed."
  return `No ${a.name} settings were found. Open ${a.name} once after installing it, or connect anyway to create the settings file.`
}

export function AssistantsScreen() {
  const { assistants, reloadAssistants, connectAssistant, sites, addSite } = useApp()
  const [disconnect, setDisconnect] = React.useState<Assistant | null>(null)
  const list = assistants.data?.assistants ?? []
  const noSites = sites.data && (sites.data.sites?.length ?? 0) === 0

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Assistants"
        description="Connect the AI assistants on this computer to your Frappe sites. Each gets a “frappe” entry in its settings that runs ffc."
        actions={
          <Tooltip>
            <TooltipTrigger
              render={
                <Button
                  variant="outline"
                  size="icon"
                  aria-label="Look again"
                  onClick={reloadAssistants}
                  disabled={assistants.loading}
                />
              }
            >
              <IconRefresh />
            </TooltipTrigger>
            <TooltipContent>Look again</TooltipContent>
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
            <EmptyTitle>Add a site first</EmptyTitle>
            <EmptyDescription>
              Assistants work with the sites you add here. Add one, then come back to connect.
            </EmptyDescription>
          </EmptyHeader>
          <EmptyContent>
            <Button onClick={addSite}>Add a site</Button>
          </EmptyContent>
        </Empty>
      )}

      {assistants.error && !assistants.data ? (
        <LoadError title="Your assistants could not be checked" error={assistants.error} onRetry={reloadAssistants} />
      ) : !assistants.data ? (
        <div className="grid grid-cols-2 gap-4 xl:grid-cols-3" aria-busy="true" aria-label="Loading assistants">
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
  const s = statusText[a.status] ?? statusText.error
  const hasEntry = a.status === "connected" || a.status === "different"

  return (
    <Card className={cn(!a.detected && "opacity-70")}>
      <CardHeader>
        <CardTitle>{a.name}</CardTitle>
        <CardDescription>
          {a.detected ? (
            "Found on this computer"
          ) : (
            <Tooltip>
              <TooltipTrigger
                render={<span tabIndex={0} className="cursor-help underline decoration-dotted underline-offset-4" />}
              >
                Not found on this computer
              </TooltipTrigger>
              <TooltipContent className="max-w-64">{notDetectedReason(a)}</TooltipContent>
            </Tooltip>
          )}
        </CardDescription>
        <CardAction>
          <Badge variant={s.variant}>
            <s.icon data-icon="inline-start" />
            {s.label}
          </Badge>
        </CardAction>
      </CardHeader>
      <CardContent className="flex flex-1 flex-col gap-2 text-sm">
        {a.status === "connected" && (
          <>
            <p>
              Uses {a.site ? <span className="font-medium">{a.site}</span> : "your default site"}
              {a.site ? "." : ", whichever it is."}
            </p>
            {a.readOnly && (
              <p className="text-muted-foreground flex items-center gap-1.5">
                <IconEye className="size-4" /> Read-only: it can look but not change anything.
              </p>
            )}
          </>
        )}
        {a.status === "different" && (
          <p className="text-muted-foreground">
            It already has a “frappe” entry that this app did not set up. Connecting replaces it, after showing you the
            change.
          </p>
        )}
        {a.status === "not_connected" && (
          <p className="text-muted-foreground">Not set up to use your Frappe sites yet.</p>
        )}
        {a.status === "error" && (
          <Alert variant="destructive">
            <IconAlertTriangle />
            <AlertTitle>Its settings could not be read</AlertTitle>
            <AlertDescription>{a.error}</AlertDescription>
          </Alert>
        )}
      </CardContent>
      <CardFooter className="gap-2">
        <Button size="sm" onClick={onConnect} disabled={disabled || a.status === "error"}>
          <IconPlugConnected data-icon="inline-start" />
          {hasEntry ? "Update" : "Connect"}
        </Button>
        {hasEntry && (
          <Button size="sm" variant="ghost" onClick={onDisconnect}>
            <IconPlugConnectedX data-icon="inline-start" />
            Disconnect
          </Button>
        )}
      </CardFooter>
    </Card>
  )
}
