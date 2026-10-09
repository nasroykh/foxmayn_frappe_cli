import { IconAlertTriangle, IconChevronDown, IconCircleCheck, IconPlayerStop } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"
import { cn } from "cn"

import { Badge } from "@/components/ui/badge"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Spinner } from "@/components/ui/spinner"
import type { ToolApproval, ToolStatus } from "@/lib/backend-types"

/** One tool call, as a compact row; the summary opens on demand. */
export function ToolRow({
  tool,
  site,
  status,
  approval = "",
  summary,
}: {
  tool: string
  site: string
  status: ToolStatus
  approval?: ToolApproval
  summary: string
}) {
  const { t } = useTranslation()
  const statusText: Record<ToolStatus, string> = {
    running: t("chat.tool.running"),
    ok: t("chat.tool.ok"),
    error: t("chat.tool.error"),
    stopped: t("chat.tool.stopped"),
  }
  const approvalText: Record<ToolApproval, string> = {
    "": "",
    approved: t("chat.tool.approved"),
    declined: t("chat.tool.declined"),
    cancelled: t("chat.tool.cancelled"),
    "ffc-approved": t("chat.tool.approved"),
    "ffc-declined": t("chat.tool.declined"),
  }
  const icon =
    status === "running" ? (
      <Spinner className="size-3.5" />
    ) : status === "ok" ? (
      <IconCircleCheck className="size-3.5" aria-hidden="true" />
    ) : status === "error" ? (
      <IconAlertTriangle className="text-destructive size-3.5" aria-hidden="true" />
    ) : (
      <IconPlayerStop className="size-3.5" aria-hidden="true" />
    )
  const declined = approval === "declined" || approval === "ffc-declined" || approval === "cancelled"

  return (
    <Collapsible className="bg-muted/50 rounded-lg border text-xs">
      <CollapsibleTrigger
        disabled={!summary}
        className="flex w-full items-center gap-2 px-2.5 py-1.5 text-left disabled:cursor-default"
      >
        {icon}
        <span className="font-mono font-medium">{tool}</span>
        {site && <span className="text-muted-foreground truncate">{site}</span>}
        <span className="text-muted-foreground ml-auto">{statusText[status]}</span>
        {approvalText[approval] && (
          <Badge variant={declined ? "destructive" : "secondary"}>{approvalText[approval]}</Badge>
        )}
        {summary && (
          <IconChevronDown
            className={cn("size-3.5 shrink-0 transition-transform in-aria-expanded:rotate-180")}
            aria-hidden="true"
          />
        )}
      </CollapsibleTrigger>
      {summary && (
        <CollapsibleContent>
          <p className="text-muted-foreground px-2.5 pb-2 font-mono break-all whitespace-pre-wrap">{summary}</p>
        </CollapsibleContent>
      )}
    </Collapsible>
  )
}
