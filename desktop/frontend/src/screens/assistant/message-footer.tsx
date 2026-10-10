import { IconCheck, IconCopy, IconPencil, IconRefresh, IconTrash } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"
import { cn } from "cn"

import { UsageLine } from "@/components/chat/usage-line"
import { copy } from "@/components/copy-field"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { intlLocale } from "@/i18n"
import type { ChatMessage, UsageTotals } from "@/lib/backend-types"

/** "21:04" today, else the short date and the time; 24-hour digits in every language. */
export function messageTime(iso: string, now = new Date()): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ""
  const time = d.toLocaleTimeString(intlLocale(), { hour: "2-digit", minute: "2-digit", hourCycle: "h23" })
  const sameDay = d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate()
  if (sameDay) return time
  return `${d.toLocaleDateString(intlLocale(), { year: "numeric", month: "2-digit", day: "2-digit" })} ${time}`
}

function Action({ label, onClick, disabled, children }: { label: string; onClick: () => void; disabled?: boolean; children: React.ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger render={<Button variant="ghost" size="icon-xs" aria-label={label} disabled={disabled} onClick={onClick} />}>
        {children}
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

/**
 * Under a message: its time, its tokens and cost (answers), and its actions.
 * Shown while the pointer is over the message or the focus is inside it
 * (the buttons stay reachable with Tab, which shows the row).
 */
export function MessageFooter({
  m,
  usage,
  busy,
  canRetry,
  onEdit,
  onDelete,
  onRetry,
}: {
  m: ChatMessage
  usage?: UsageTotals
  /** A run is active: nothing may change the conversation. */
  busy: boolean
  /** The last answer of the conversation. */
  canRetry: boolean
  onEdit: () => void
  onDelete: () => void
  onRetry: () => void
}) {
  const { t } = useTranslation()
  const [copied, setCopied] = React.useState(false)
  React.useEffect(() => {
    if (!copied) return
    const timer = setTimeout(() => setCopied(false), 1500)
    return () => clearTimeout(timer)
  }, [copied])
  const user = m.role === "user"
  return (
    <div
      data-slot="message-footer"
      className={cn(
        "text-muted-foreground flex min-h-6 flex-wrap items-center gap-x-2 gap-y-1 text-xs opacity-0 transition-opacity group-hover/msg:opacity-100 group-focus-within/msg:opacity-100",
        user && "justify-end",
      )}
    >
      <time dateTime={m.created} className="tabular-nums">
        {messageTime(m.created)}
      </time>
      {usage && <UsageLine usage={usage} />}
      <span className="flex items-center">
        {m.text && (
          <Action label={copied ? t("chat.message.copied") : t("chat.message.copy")} onClick={async () => setCopied(await copy(m.text))}>
            {copied ? <IconCheck /> : <IconCopy />}
          </Action>
        )}
        {user && (
          <Action label={t("chat.message.edit")} onClick={onEdit} disabled={busy}>
            <IconPencil />
          </Action>
        )}
        {user && (
          <Action label={t("chat.message.delete")} onClick={onDelete} disabled={busy}>
            <IconTrash />
          </Action>
        )}
        {!user && canRetry && (
          <Action label={t("chat.message.retry")} onClick={onRetry} disabled={busy}>
            <IconRefresh />
          </Action>
        )}
      </span>
    </div>
  )
}
