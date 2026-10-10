import { IconShieldQuestion } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { DiffView } from "@/components/diff-view"
import { Details } from "@/components/page"
import { Button } from "@/components/ui/button"
import type { ChatApproval, DiffField } from "@/lib/backend-types"

function show(v: unknown): string {
  if (typeof v === "string") return v
  if (v === undefined) return ""
  return JSON.stringify(v)
}

/** The field changes as a unified-diff-like text, for DiffView. */
export function fieldDiffText(fields: DiffField[]): string {
  const out: string[] = []
  for (const f of fields) {
    out.push(`@@ ${f.field} @@`)
    for (const line of show(f.old).split("\n")) out.push(`-${line}`)
    for (const line of show(f.new).split("\n")) out.push(`+${line}`)
  }
  return out.join("\n")
}

/**
 * What the assistant wants to do, and the buttons to allow or refuse it.
 * Decline has the focus, so a stray Enter never approves a change.
 */
export function ApprovalCard({
  card,
  busy,
  onAnswer,
}: {
  card: ChatApproval
  busy?: boolean
  onAnswer: (approve: boolean) => void
}) {
  const { t } = useTranslation()
  const declineRef = React.useRef<HTMLButtonElement>(null)
  // Decline has the focus whenever a card appears (autoFocus alone is lost when another card closes).
  React.useEffect(() => {
    declineRef.current?.focus()
  }, [card.approvalID, busy])
  const isUpdate = card.tool === "update_doc"
  const title = card.kind === "ffc" ? card.message || t("chat.approval.ffcFallback") : t("chat.approval.appTitle")

  return (
    <section
      data-slot="approval-card"
      aria-label={t("chat.approval.label")}
      className="border-primary/40 bg-card flex flex-col gap-3 rounded-xl border p-4 text-sm"
    >
      <div className="flex items-start gap-2">
        <IconShieldQuestion className="text-primary mt-0.5 size-5 shrink-0" aria-hidden="true" />
        <p className="font-medium">{title}</p>
      </div>

      <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1">
        <dt className="text-muted-foreground">{t("chat.approval.tool")}</dt>
        <dd className="font-mono">{card.tool}</dd>
        <dt className="text-muted-foreground">{t("chat.approval.site")}</dt>
        <dd>{card.site}</dd>
        {card.doctypes.length > 0 && (
          <>
            <dt className="text-muted-foreground">{t("chat.approval.doctypes")}</dt>
            <dd>{card.doctypes.join(", ")}</dd>
          </>
        )}
        {card.names.length > 0 && (
          <>
            <dt className="text-muted-foreground">{t("chat.approval.names")}</dt>
            <dd className="break-all">{card.names.join(", ")}</dd>
          </>
        )}
      </dl>

      {isUpdate &&
        (card.noChanges ? (
          <p className="text-muted-foreground">{t("chat.approval.noChanges")}</p>
        ) : (
          card.diff.length > 0 && <DiffView diff={fieldDiffText(card.diff)} label={t("chat.approval.diffLabel")} headers={false} />
        ))}

      <Details label={t("chat.approval.args")} defaultOpen>{JSON.stringify(card.args, null, 2)}</Details>

      <div className="flex justify-end gap-2">
        <Button ref={declineRef} variant="outline" autoFocus disabled={busy} onClick={() => onAnswer(false)}>
          {t("chat.approval.decline")}
        </Button>
        <Button disabled={busy} onClick={() => onAnswer(true)}>
          {t("chat.approval.approve")}
        </Button>
      </div>
    </section>
  )
}
