import {
  IconActivity,
  IconAlertTriangle,
  IconCircleCheck,
  IconClock,
  IconHandStop,
  IconShieldX,
  IconX,
} from "@tabler/icons-react"
import * as React from "react"
import type { TFunction } from "i18next"
import { useTranslation } from "react-i18next"

import { useOptionalApp } from "@/app/app-context"
import { toolLabel } from "@/components/chat/tool-row"
import { LoadError, PageHeader } from "@/components/page"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { intlLocale } from "@/i18n"
import { backend } from "@/lib/backend"
import type { ActivityEntry, ActivityPage, ActivityQuery } from "@/lib/backend-types"
import { appError, type AppError } from "@/lib/errors"
import { messageTime } from "@/screens/assistant/message-footer"

const PAGE = 50
/** Go's zero time: no bound. */
const NO_TIME = "0001-01-01T00:00:00Z"
/** The client name the app's own Assistant uses in the audit log. */
const DESKTOP_CLIENT = "foxmayn-desktop"

type Range = "any" | "today" | "week" | "month"
const RANGES: Range[] = ["any", "today", "week", "month"]

interface Filters {
  site: string
  client: string
  status: string
  range: Range
  runID: string
}
const NONE: Filters = { site: "", client: "", status: "", range: "any", runID: "" }

function filtered(f: Filters) {
  return !!(f.site || f.client || f.status || f.runID || f.range !== "any")
}

/** When a date range starts: midnight today, or that many days back from now. */
function sinceOf(range: Range): string {
  const now = new Date()
  if (range === "today") return new Date(now.getFullYear(), now.getMonth(), now.getDate()).toISOString()
  if (range === "week") return new Date(now.getTime() - 7 * 86_400_000).toISOString()
  if (range === "month") return new Date(now.getTime() - 30 * 86_400_000).toISOString()
  return NO_TIME
}

function queryOf(f: Filters, since: string, offset: number): ActivityQuery {
  return { site: f.site, client: f.client, tool: "", status: f.status, runID: f.runID, since, until: NO_TIME, offset, limit: PAGE }
}

// i18n keys: activity.ago.minutes activity.ago.hours
// i18n keys: activity.status.ok activity.status.error activity.status.denied activity.status.invalid activity.status.confirm_pending activity.status.declined
// i18n keys: activity.confirm.confirmed activity.confirm.declined activity.confirm.unsupported activity.confirm.never
// i18n keys: activity.range.any activity.range.today activity.range.week activity.range.month
const STATUS_KEYS = new Set(["ok", "error", "denied", "invalid", "confirm_pending", "declined"])
const CONFIRM_KEYS = new Set(["confirmed", "declined", "unsupported", "never"])

type BadgeVariant = "default" | "secondary" | "outline" | "destructive"
const STATUS_LOOK: Record<string, { variant: BadgeVariant; icon: typeof IconCircleCheck }> = {
  ok: { variant: "secondary", icon: IconCircleCheck },
  error: { variant: "destructive", icon: IconAlertTriangle },
  denied: { variant: "destructive", icon: IconShieldX },
  invalid: { variant: "outline", icon: IconAlertTriangle },
  confirm_pending: { variant: "outline", icon: IconClock },
  declined: { variant: "outline", icon: IconHandStop },
}

function useLabels() {
  const { t } = useTranslation()
  return {
    status: (s: string) => (STATUS_KEYS.has(s) ? t(`activity.status.${s}`) : s),
    client: (c: string) => (c === DESKTOP_CLIENT ? t("activity.app.desktop") : c),
    confirm: (c: string) => (CONFIRM_KEYS.has(c) ? t(`activity.confirm.${c}`) : c),
  }
}

function StatusBadge({ status }: { status: string }) {
  const labels = useLabels()
  const look = STATUS_LOOK[status] ?? { variant: "outline" as const, icon: IconActivity }
  return (
    <Badge variant={look.variant}>
      <look.icon data-icon="inline-start" aria-hidden="true" />
      <span dir={STATUS_KEYS.has(status) ? undefined : "auto"}>{labels.status(status)}</span>
    </Badge>
  )
}

/** The date and time in full, in numbers (24-hour clock, Latin digits in every language). */
function fullTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ""
  const date = d.toLocaleDateString(intlLocale(), { year: "numeric", month: "2-digit", day: "2-digit" })
  const time = d.toLocaleTimeString(intlLocale(), { hour: "2-digit", minute: "2-digit", hourCycle: "h23" })
  return `${date} ${time}`
}

/** "4 min ago" or "2 h ago" within today (just now under a minute), else the short date and the time. */
function whenText(t: TFunction, iso: string, now = new Date()): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ""
  const sameDay = d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate()
  if (!sameDay) return messageTime(iso, now)
  const minutes = Math.max(0, Math.round((now.getTime() - d.getTime()) / 60_000))
  const nf = new Intl.NumberFormat(intlLocale())
  if (minutes < 1) return t("time.justNow")
  if (minutes < 60) return t("activity.ago.minutes", { value: nf.format(minutes) })
  return t("activity.ago.hours", { value: nf.format(Math.floor(minutes / 60)) })
}

function durationText(t: TFunction, ms: number): string {
  const nf = (max: number) => new Intl.NumberFormat(intlLocale(), { maximumFractionDigits: max })
  if (ms < 1000) return t("activity.duration.ms", { value: nf(0).format(ms) })
  return t("activity.duration.s", { value: nf(1).format(ms / 1000) })
}

/** The documents of a call in one short line: "ToDo TD-0001", "3 documents" or the types. */
function docsText(e: ActivityEntry, t: TFunction): string {
  const total = Math.max(e.namesTotal, e.names.length)
  if (total > 1) return t("activity.docs.count", { count: total })
  if (total === 1 && e.names.length === 1) return [e.doctypes.length === 1 ? e.doctypes[0] : "", e.names[0]].filter(Boolean).join(" ")
  return e.doctypes.join(", ")
}

export function ActivityScreen() {
  const { t } = useTranslation()
  const app = useOptionalApp()
  const pendingActivityFilter = app?.pendingActivityFilter ?? null
  const takeActivityFilter = app?.takeActivityFilter
  const labels = useLabels()
  const [filters, setFilters] = React.useState<Filters>(() => ({ ...NONE, runID: pendingActivityFilter?.runID ?? "" }))
  const [since, setSince] = React.useState(NO_TIME)
  const [page, setPage] = React.useState<ActivityPage | null>(null)
  const [entries, setEntries] = React.useState<ActivityEntry[]>([])
  const [loading, setLoading] = React.useState(true)
  const [more, setMore] = React.useState(false)
  const [error, setError] = React.useState<AppError | null>(null)
  const [selected, setSelected] = React.useState<ActivityEntry | null>(null)
  const seq = React.useRef(0)

  // A run filter asked for from the chat: taken once, then it is ordinary state.
  React.useEffect(() => {
    if (!pendingActivityFilter || !takeActivityFilter) return
    setFilters({ ...NONE, runID: pendingActivityFilter.runID })
    setSince(NO_TIME)
    takeActivityFilter()
  }, [pendingActivityFilter, takeActivityFilter])

  const load = React.useCallback(async () => {
    const n = ++seq.current
    setLoading(true)
    setError(null)
    try {
      const p = await backend.queryActivity(queryOf(filters, since, 0))
      if (n !== seq.current) return
      setPage(p)
      setEntries(p.entries)
    } catch (err) {
      if (n === seq.current) setError(appError(err))
    } finally {
      if (n === seq.current) setLoading(false)
    }
  }, [filters, since])

  React.useEffect(() => {
    void load()
  }, [load])

  async function loadMore() {
    const n = ++seq.current
    setMore(true)
    try {
      const p = await backend.queryActivity(queryOf(filters, since, entries.length))
      if (n !== seq.current) return
      setPage(p)
      setEntries((old) => [...old, ...p.entries])
    } catch (err) {
      if (n === seq.current) setError(appError(err))
    } finally {
      if (n === seq.current) setMore(false)
    }
  }

  function change(patch: Partial<Filters>) {
    setFilters((f) => ({ ...f, ...patch }))
    if (patch.range) setSince(sinceOf(patch.range))
  }
  function clear() {
    setFilters(NONE)
    setSince(NO_TIME)
  }

  const header = <PageHeader title={t("activity.title")} description={t("activity.description")} />
  if (error && !page) {
    return (
      <div className="flex flex-col gap-6">
        {header}
        <LoadError title={t("activity.loadFailed")} error={error} onRetry={() => void load()} />
      </div>
    )
  }
  if (!page) {
    return (
      <div className="flex flex-col gap-6">
        {header}
        <div className="flex flex-col gap-2" aria-busy="true">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-14 w-full" />
          ))}
        </div>
      </div>
    )
  }

  const shown = filtered(filters)
  const noLog = !page.exists
  return (
    <div className="flex flex-col gap-6">
      {header}

      {noLog ? (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <IconActivity />
            </EmptyMedia>
            <EmptyTitle>{t("activity.empty.title")}</EmptyTitle>
            <EmptyDescription>{t("activity.empty.description")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <>
          <div role="group" aria-label={t("activity.filters.label")} className="flex flex-wrap items-end gap-3">
            <FilterSelect label={t("activity.filters.site")} value={filters.site} onChange={(v) => change({ site: v })} any={t("activity.filters.anySite")}>
              {page.sites.map((s) => (
                <NativeSelectOption key={s} value={s}>
                  {s}
                </NativeSelectOption>
              ))}
            </FilterSelect>
            <FilterSelect label={t("activity.filters.app")} value={filters.client} onChange={(v) => change({ client: v })} any={t("activity.filters.anyApp")}>
              {page.clients.map((c) => (
                <NativeSelectOption key={c} value={c}>
                  {labels.client(c)}
                </NativeSelectOption>
              ))}
            </FilterSelect>
            <FilterSelect label={t("activity.filters.result")} value={filters.status} onChange={(v) => change({ status: v })} any={t("activity.filters.anyResult")}>
              {page.statuses.map((s) => (
                <NativeSelectOption key={s} value={s}>
                  {labels.status(s)}
                </NativeSelectOption>
              ))}
            </FilterSelect>
            <label className="text-muted-foreground flex flex-col gap-1 text-xs">
              {t("activity.filters.when")}
              <NativeSelect size="sm" className="min-w-36" value={filters.range} onChange={(e) => change({ range: e.target.value as Range })}>
                {RANGES.map((r) => (
                  <NativeSelectOption key={r} value={r}>
                    {t(`activity.range.${r}`)}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </label>
            {filters.runID && (
              <Badge variant="secondary" className="mb-1">
                {t("activity.filters.runOnly")}
              </Badge>
            )}
            {shown && (
              <Button variant="ghost" size="sm" onClick={clear}>
                <IconX data-icon="inline-start" />
                {t("activity.filters.clear")}
              </Button>
            )}
          </div>

          {error && <LoadError title={t("activity.loadFailed")} error={error} onRetry={() => void load()} />}

          {entries.length === 0 && !loading ? (
            <Empty className="border border-dashed">
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <IconActivity />
                </EmptyMedia>
                <EmptyTitle>{shown ? t("activity.empty.noMatch") : t("activity.empty.title")}</EmptyTitle>
                {!shown && <EmptyDescription>{t("activity.empty.description")}</EmptyDescription>}
              </EmptyHeader>
              {shown && (
                <EmptyContent>
                  <Button variant="outline" onClick={clear}>
                    {t("activity.filters.clear")}
                  </Button>
                </EmptyContent>
              )}
            </Empty>
          ) : (
            <ul aria-label={t("activity.listLabel")} aria-busy={loading} className="flex flex-col divide-y rounded-xl border">
              {entries.map((e, i) => (
                <li key={`${e.time}-${i}`}>
                  <EntryRow entry={e} onOpen={() => setSelected(e)} />
                </li>
              ))}
            </ul>
          )}

          <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
            {entries.length > 0 && (
              <p className="text-muted-foreground text-sm" aria-live="polite">
                {t("activity.showing", { shown: entries.length, total: page.total })}
              </p>
            )}
            {entries.length < page.total && (
              <Button variant="outline" size="sm" onClick={() => void loadMore()} disabled={more || loading}>
                {more && <Spinner data-icon="inline-start" />}
                {t("activity.loadMore")}
              </Button>
            )}
            {page.skipped > 0 && <p className="text-muted-foreground text-xs">{t("activity.skipped", { count: page.skipped })}</p>}
          </div>
        </>
      )}

      <DetailsSheet entry={selected} onClose={() => setSelected(null)} />
    </div>
  )
}

function FilterSelect({
  label,
  value,
  onChange,
  any,
  children,
}: {
  label: string
  value: string
  onChange: (v: string) => void
  any: string
  children: React.ReactNode
}) {
  return (
    <label className="text-muted-foreground flex flex-col gap-1 text-xs">
      {label}
      <NativeSelect size="sm" className="min-w-36 max-w-56" value={value} onChange={(e) => onChange(e.target.value)}>
        <NativeSelectOption value="">{any}</NativeSelectOption>
        {children}
      </NativeSelect>
    </label>
  )
}

function EntryRow({ entry: e, onOpen }: { entry: ActivityEntry; onOpen: () => void }) {
  const { t } = useTranslation()
  const labels = useLabels()
  const docs = docsText(e, t)
  return (
    <button
      type="button"
      onClick={onOpen}
      className="hover:bg-muted/50 focus-visible:ring-ring/50 flex w-full flex-col gap-1 px-4 py-3 text-start outline-none first:rounded-t-xl last:rounded-b-xl focus-visible:ring-3"
    >
      <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className="min-w-0 text-sm font-medium">{toolLabel(t, e.tool)}</span>
        {docs && (
          <span className="text-muted-foreground min-w-0 text-sm" data-site-data dir="auto">
            {docs}
          </span>
        )}
        <span className="ms-auto">
          <StatusBadge status={e.status} />
        </span>
      </span>
      <span className="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs">
        <time dateTime={e.time} title={fullTime(e.time)} className="tabular-nums">
          {whenText(t, e.time)}
        </time>
        <span data-site-data dir="auto">
          {labels.client(e.client)}
        </span>
        <span data-site-data dir="auto">
          {e.site}
        </span>
        <span className="tabular-nums">{durationText(t, e.durationMS)}</span>
      </span>
    </button>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <dt className="text-muted-foreground text-xs">{label}</dt>
      <dd className="min-w-0 text-sm break-words">{children}</dd>
    </div>
  )
}

function DetailsSheet({ entry: e, onClose }: { entry: ActivityEntry | null; onClose: () => void }) {
  const { t } = useTranslation()
  const labels = useLabels()
  // Kept while the sheet closes, so its content does not vanish mid-animation.
  const last = React.useRef<ActivityEntry | null>(null)
  if (e) last.current = e
  const d = e ?? last.current
  const more = d ? Math.max(0, d.namesTotal - d.names.length) : 0
  return (
    <Sheet open={e !== null} onOpenChange={(o) => !o && onClose()}>
      <SheetContent
        className="w-full gap-0 data-[side=right]:sm:max-w-md"
        // The close button, not the scrollable arguments below (focusing those scrolls the sheet).
        initialFocus={() => document.querySelector<HTMLElement>('[data-slot="sheet-content"] [data-slot="sheet-close"]') ?? true}
      >
        {d && (
          <>
            <SheetHeader className="border-b">
              <SheetTitle>{toolLabel(t, d.tool)}</SheetTitle>
              <SheetDescription>{t("activity.details.title")}</SheetDescription>
            </SheetHeader>
            <div className="min-h-0 flex-1 overflow-y-auto p-4">
              <dl className="flex flex-col gap-4">
                <Field label={t("activity.details.result")}>
                  <StatusBadge status={d.status} />
                </Field>
                <Field label={t("activity.details.when")}>
                  <time dateTime={d.time}>{fullTime(d.time)}</time>
                </Field>
                <Field label={t("activity.details.app")}>
                  <span data-site-data dir="auto">
                    {labels.client(d.client)}
                  </span>
                </Field>
                <Field label={t("activity.details.site")}>
                  <span data-site-data dir="auto">
                    {d.site}
                  </span>
                </Field>
                <Field label={t("activity.details.tool")}>
                  <code className="font-mono text-xs" dir="ltr" data-site-data>
                    {d.tool}
                  </code>
                </Field>
                {d.method && (
                  <Field label={t("activity.details.method")}>
                    <code className="font-mono text-xs break-all" dir="ltr" data-site-data>
                      {d.method}
                    </code>
                  </Field>
                )}
                {d.via && (
                  <Field label={t("activity.details.via")}>
                    <code className="font-mono text-xs" dir="ltr" data-site-data>
                      {d.via}
                    </code>
                  </Field>
                )}
                {d.runID && (
                  <Field label={t("activity.details.run")}>
                    <code className="font-mono text-xs break-all" dir="ltr" data-site-data>
                      {d.runID}
                    </code>
                  </Field>
                )}
                {d.doctypes.length > 0 && (
                  <Field label={t("activity.details.doctypes")}>
                    <span data-site-data dir="auto">
                      {d.doctypes.join(", ")}
                    </span>
                  </Field>
                )}
                {d.names.length > 0 && (
                  <Field label={t("activity.details.documents")}>
                    <span data-site-data dir="auto">
                      {d.names.join(", ")}
                    </span>
                    {more > 0 && <span className="text-muted-foreground"> {t("activity.details.more", { count: more })}</span>}
                  </Field>
                )}
                {d.confirm && (
                  <Field label={t("activity.details.confirmation")}>
                    <span dir={CONFIRM_KEYS.has(d.confirm) ? undefined : "auto"}>{labels.confirm(d.confirm)}</span>
                  </Field>
                )}
                <Field label={t("activity.details.duration")}>
                  <span className="tabular-nums">{durationText(t, d.durationMS)}</span>
                </Field>
                {d.error && (
                  <Field label={t("activity.details.error")}>
                    <span dir="auto" data-site-data>
                      {d.error}
                    </span>
                  </Field>
                )}
                <Field label={t("activity.details.args")}>
                  {d.args ? (
                    <pre
                      dir="ltr"
                      tabIndex={0}
                      className="bg-muted max-h-64 overflow-auto rounded-md p-2 font-mono text-xs whitespace-pre-wrap break-all"
                    >
                      {d.args}
                    </pre>
                  ) : (
                    <span className="text-muted-foreground">{t("activity.details.noArgs")}</span>
                  )}
                </Field>
              </dl>
            </div>
          </>
        )}
      </SheetContent>
    </Sheet>
  )
}
