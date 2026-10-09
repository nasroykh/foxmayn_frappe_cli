import { IconAdjustmentsHorizontal, IconSearch, IconX } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"
import { cn } from "cn"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { NativeSelect, NativeSelectOptGroup, NativeSelectOption } from "@/components/ui/native-select"
import { Spinner } from "@/components/ui/spinner"
import { backend } from "@/lib/backend"
import type { Profile, SearchFilter, SearchHit } from "@/lib/backend-types"
import { appError, type AppError } from "@/lib/errors"

const SEARCH_DELAY_MS = 250

/** Wraps the words of the query that appear in the snippet in <mark>. */
export function Highlighted({ text, query }: { text: string; query: string }) {
  const words = Array.from(new Set(query.split(/\s+/).filter(Boolean)))
  if (words.length === 0) return <>{text}</>
  const escaped = words.sort((a, b) => b.length - a.length).map((w) => w.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"))
  const parts = text.split(new RegExp(`(${escaped.join("|")})`, "i"))
  return (
    <>
      {parts.map((p, i) =>
        i % 2 === 1 ? (
          <mark key={i} className="bg-primary/20 rounded-sm text-inherit">
            {p}
          </mark>
        ) : (
          <React.Fragment key={i}>{p}</React.Fragment>
        ),
      )}
    </>
  )
}

/**
 * The search box over the conversations, with filters (site, profile, dates).
 * With some text typed it shows the matching messages; choosing one opens its
 * conversation. Ctrl+Shift+F (Cmd+Shift+F) focuses the box.
 */
export function ConversationSearch({
  archived,
  sites,
  profiles,
  onOpen,
  onActive,
}: {
  /** Search the archive instead of the other conversations. */
  archived: boolean
  sites: string[]
  profiles: { presets: Profile[]; own: Profile[] }
  onOpen: (convID: string) => void
  /** Tells the list whether results are on screen (it hides the conversations). */
  onActive: (active: boolean) => void
}) {
  const { t } = useTranslation()
  const inputRef = React.useRef<HTMLInputElement>(null)
  const [query, setQuery] = React.useState("")
  const [filters, setFilters] = React.useState(false)
  const [site, setSite] = React.useState("")
  const [profileID, setProfileID] = React.useState("")
  const [from, setFrom] = React.useState("")
  const [to, setTo] = React.useState("")
  const [hits, setHits] = React.useState<SearchHit[] | null>(null)
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<AppError | null>(null)

  const text = query.trim()
  const filtered = !!(site || profileID || from || to)
  const active = text !== ""
  React.useEffect(() => onActive(active), [active, onActive])

  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() === "f" && e.shiftKey && (e.ctrlKey || e.metaKey)) {
        e.preventDefault()
        inputRef.current?.focus()
        inputRef.current?.select()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  React.useEffect(() => {
    if (!text) {
      setHits(null)
      setError(null)
      setBusy(false)
      return
    }
    let stale = false
    setBusy(true)
    const filter: SearchFilter = { site, profileID, from, to, archived }
    const timer = setTimeout(() => {
      backend
        .search(text, filter, 50)
        .then((r) => {
          if (stale) return
          setHits(r)
          setError(null)
        })
        .catch((err) => {
          if (stale) return
          setHits([])
          setError(appError(err))
        })
        .finally(() => {
          if (!stale) setBusy(false)
        })
    }, SEARCH_DELAY_MS)
    return () => {
      stale = true
      clearTimeout(timer)
    }
  }, [text, site, profileID, from, to, archived])

  function clear() {
    setQuery("")
    inputRef.current?.focus()
  }

  return (
    <div className="flex flex-col gap-2 px-3 pb-2">
      <div className="flex items-center gap-1">
        <div className="relative min-w-0 flex-1">
          <IconSearch className="text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-4 -translate-y-1/2" aria-hidden />
          <Input
            ref={inputRef}
            type="search"
            role="searchbox"
            aria-label={t("chat.search.label")}
            placeholder={t("chat.search.placeholder")}
            className="h-8 pr-7 pl-8"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape" && query) {
                e.preventDefault()
                e.stopPropagation()
                setQuery("")
              }
            }}
          />
          {query && (
            <Button
              variant="ghost"
              size="icon-xs"
              className="absolute top-1/2 right-1 -translate-y-1/2"
              aria-label={t("chat.search.clear")}
              onClick={clear}
            >
              <IconX />
            </Button>
          )}
        </div>
        <Button
          variant={filters || filtered ? "secondary" : "ghost"}
          size="icon-sm"
          aria-label={t("chat.search.filters")}
          aria-expanded={filters}
          onClick={() => setFilters((f) => !f)}
        >
          <IconAdjustmentsHorizontal />
        </Button>
      </div>

      {filters && (
        <div className="flex flex-col gap-1.5" role="group" aria-label={t("chat.search.filters")}>
          <NativeSelect size="sm" className="w-full" aria-label={t("chat.search.site")} value={site} onChange={(e) => setSite(e.target.value)}>
            <NativeSelectOption value="">{t("chat.search.anySite")}</NativeSelectOption>
            {sites.map((s) => (
              <NativeSelectOption key={s} value={s}>
                {s}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          <NativeSelect
            size="sm"
            className="w-full"
            aria-label={t("chat.search.profile")}
            value={profileID}
            onChange={(e) => setProfileID(e.target.value)}
          >
            <NativeSelectOption value="">{t("chat.search.anyProfile")}</NativeSelectOption>
            <NativeSelectOptGroup label={t("profile.picker.presets")}>
              {profiles.presets.map((p) => (
                <NativeSelectOption key={p.id} value={p.id}>
                  {p.name}
                </NativeSelectOption>
              ))}
            </NativeSelectOptGroup>
            {profiles.own.length > 0 && (
              <NativeSelectOptGroup label={t("profile.picker.own")}>
                {profiles.own.map((p) => (
                  <NativeSelectOption key={p.id} value={p.id}>
                    {p.name}
                  </NativeSelectOption>
                ))}
              </NativeSelectOptGroup>
            )}
          </NativeSelect>
          <div className="grid grid-cols-2 gap-1.5">
            <label className="text-muted-foreground flex flex-col gap-0.5 text-xs">
              {t("chat.search.from")}
              <Input type="date" className="h-8" value={from} max={to || undefined} onChange={(e) => setFrom(e.target.value)} />
            </label>
            <label className="text-muted-foreground flex flex-col gap-0.5 text-xs">
              {t("chat.search.to")}
              <Input type="date" className="h-8" value={to} min={from || undefined} onChange={(e) => setTo(e.target.value)} />
            </label>
          </div>
          {filtered && (
            <Button
              variant="ghost"
              size="xs"
              className="self-start"
              onClick={() => {
                setSite("")
                setProfileID("")
                setFrom("")
                setTo("")
              }}
            >
              {t("chat.search.reset")}
            </Button>
          )}
        </div>
      )}

      {active && (
        <div aria-live="polite" className="text-muted-foreground flex items-center gap-1.5 text-xs">
          {busy && <Spinner className="size-3" />}
          {error
            ? error.message
            : busy && !hits
              ? t("chat.search.searching")
              : t("chat.search.count", { count: hits?.length ?? 0 })}
        </div>
      )}
      {active && hits && hits.length > 0 && (
        <ul aria-label={t("chat.search.results")} className={cn("flex flex-col gap-0.5", busy && "opacity-60")}>
          {hits.map((h) => (
            <li key={h.msgID}>
              <button
                type="button"
                onClick={() => onOpen(h.convID)}
                className="hover:bg-muted focus-visible:ring-ring/50 flex w-full flex-col rounded-lg px-2 py-1.5 text-left text-sm outline-none focus-visible:ring-3"
              >
                <span className="truncate font-medium">{h.title || t("chat.list.untitled")}</span>
                <span className="text-muted-foreground line-clamp-2 text-xs break-words">
                  <Highlighted text={h.snippet} query={text} />
                </span>
                <span className="text-muted-foreground truncate text-xs">
                  {h.site} · {new Date(h.updated).toLocaleDateString()}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
