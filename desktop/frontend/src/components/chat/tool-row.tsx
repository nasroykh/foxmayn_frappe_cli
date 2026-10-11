import { IconAlertTriangle, IconChevronDown, IconCircleCheck, IconPlayerStop } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"
import { cn } from "cn"

import { Badge } from "@/components/ui/badge"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Spinner } from "@/components/ui/spinner"
import type { ToolApproval, ToolStatus } from "@/lib/backend-types"

// i18n keys: chat.tool.names.* (one plain label per ffc tool name)
const KNOWN_TOOLS = new Set([
  "list_sites", "ping", "get_doc", "list_docs", "count_docs", "get_schema", "list_doctypes", "list_reports",
  "run_report", "search", "get_doc_context", "create_doc", "update_doc", "delete_doc", "bulk_create", "bulk_update",
  "bulk_delete", "call_method", "whoami", "check_permission", "aggregate", "submit_doc", "cancel_doc", "bulk_submit",
  "bulk_cancel", "amend_doc", "copy_doc", "rename_doc", "apply_workflow", "restore_doc", "get_transitions",
  "add_comment", "assign_to", "remove_assignment", "add_tag", "remove_tag", "share_doc", "unshare_doc", "site_health",
  "list_jobs", "list_errors", "scheduler_status", "list_attachments", "attach_file", "get_print_html", "erp_map",
  "erp_payment", "erp_item", "erp_stock", "erp_party",
])

/** One tool call, as a compact row with a plain label; the raw name and summary open on demand. */
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
  const label = KNOWN_TOOLS.has(tool) ? t(`chat.tool.names.${tool}`) : t("chat.tool.generic")
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
        
        className="flex w-full items-center gap-2 px-2.5 py-1.5 text-start"
      >
        {icon}
        <span className="font-medium">{label}</span>
        {site && <span className="text-muted-foreground truncate">{site}</span>}
        <span className="text-muted-foreground ms-auto">{statusText[status]}</span>
        {approvalText[approval] && (
          <Badge variant={declined ? "destructive" : "secondary"}>{approvalText[approval]}</Badge>
        )}
        <IconChevronDown
          className={cn("size-3.5 shrink-0 transition-transform in-aria-expanded:rotate-180")}
          aria-hidden="true"
        />
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="text-muted-foreground flex flex-col gap-1 px-2.5 pb-2">
          <p className="flex items-baseline gap-1.5">
            <span>{t("chat.approval.tool")}</span>
            <span dir="ltr" className="font-mono">
              {tool}
            </span>
          </p>
          {summary && <p className="font-mono break-all whitespace-pre-wrap">{summary}</p>}
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}
