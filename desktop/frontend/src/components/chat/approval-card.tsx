import { IconShieldQuestion } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { Details } from "@/components/page"
import { Button } from "@/components/ui/button"
import type { ChatApproval, DiffField } from "@/lib/backend-types"

function show(v: unknown): string {
  if (typeof v === "string") return v
  if (v === undefined) return ""
  return JSON.stringify(v)
}

/** The field changes as a table: field, value before (struck), value after. */
function FieldChanges({ fields }: { fields: DiffField[] }) {
  const { t } = useTranslation()
  return (
    <table aria-label={t("chat.approval.diffLabel")} className="w-full table-fixed border-collapse overflow-hidden rounded-md text-xs">
      <thead className="text-muted-foreground">
        <tr className="border-b">
          <th scope="col" className="w-1/4 py-1 pe-2 text-start font-medium">
            {t("chat.approval.field")}
          </th>
          <th scope="col" className="py-1 pe-2 text-start font-medium">
            {t("chat.approval.before")}
          </th>
          <th scope="col" className="py-1 text-start font-medium">
            {t("chat.approval.after")}
          </th>
        </tr>
      </thead>
      <tbody data-site-data>
        {fields.map((f) => (
          <tr key={f.field} className="border-b align-top last:border-b-0">
            <th scope="row" dir="ltr" className="py-1.5 pe-2 text-start font-mono font-normal break-all">
              {f.field}
            </th>
            <td dir="auto" className="text-destructive py-1.5 pe-2 break-words whitespace-pre-wrap line-through decoration-1">
              {show(f.old)}
            </td>
            <td dir="auto" className="py-1.5 font-medium break-words whitespace-pre-wrap text-emerald-700 dark:text-emerald-400">
              {show(f.new)}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

/** The field changes as a unified-diff-like text (kept for plain-text use). */
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
  const showsDiff = isUpdate && !card.noChanges && card.diff.length > 0
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
        <dd>
          {t(`chat.tool.names.${card.tool}`, { defaultValue: t("chat.tool.generic") })}{" "}
          <code dir="ltr" className="text-muted-foreground text-xs">
            {card.tool}
          </code>
        </dd>
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
          showsDiff && <FieldChanges fields={card.diff} />
        ))}

      {/* The request is the only detail of a change other than an update: open then. */}
      <Details label={t("chat.approval.args")} defaultOpen={!showsDiff}>
        {JSON.stringify(card.args, null, 2)}
      </Details>

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
