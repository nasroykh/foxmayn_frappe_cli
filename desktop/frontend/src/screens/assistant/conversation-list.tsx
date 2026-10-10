import {
  IconArchive,
  IconArchiveOff,
  IconDots,
  IconFileExport,
  IconFileImport,
  IconPin,
  IconPinFilled,
  IconPlus,
  IconTrash,
} from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"
import { cn } from "cn"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { Conversation, ExportFormat } from "@/lib/backend-types"
import { ConversationSearch } from "@/screens/assistant/conversation-search"
import { useProfiles } from "@/screens/assistant/profile-picker"

/**
 * The conversations, pinned first and then newest first, with search,
 * new, import, the archive and, per conversation, pin, archive, export and
 * delete (after a confirmation).
 */
export function ConversationList({
  conversations,
  sites,
  selected,
  onSelect,
  onNew,
  onDelete,
  onPin,
  onArchive,
  onExport,
  onImport,
}: {
  conversations: Conversation[]
  /** The site names, for the search filter. */
  sites: string[]
  selected: string
  onSelect: (id: string) => void
  onNew: () => void
  onDelete: (c: Conversation) => Promise<void>
  onPin: (c: Conversation, pinned: boolean) => Promise<void>
  onArchive: (c: Conversation, archived: boolean) => Promise<void>
  onExport: (c: Conversation, format: ExportFormat) => Promise<void>
  onImport: () => Promise<void>
}) {
  const { t } = useTranslation()
  const { presets, own } = useProfiles()
  const [doomed, setDoomed] = React.useState<Conversation | null>(null)
  const [busy, setBusy] = React.useState(false)
  const [archive, setArchive] = React.useState(false)
  const [searching, setSearching] = React.useState(false)

  async function confirm() {
    if (!doomed) return
    setBusy(true)
    try {
      await onDelete(doomed)
    } finally {
      setBusy(false)
      setDoomed(null)
    }
  }

  const shown = conversations.filter((c) => !!c.archived === archive)
  // Pinned first; the order of the rest (newest first) is kept.
  const ordered = [...shown].sort((a, b) => Number(!!b.pinned) - Number(!!a.pinned))
  const archivedCount = conversations.filter((c) => c.archived).length

  return (
    <nav aria-label={t("chat.list.label")} className="flex h-full min-h-0 w-60 shrink-0 flex-col border-e">
      <div className="flex items-center justify-between gap-1 p-3 pb-2">
        <h2 className="min-w-0 truncate text-sm font-semibold">{archive ? t("chat.list.archiveTitle") : t("chat.list.title")}</h2>
        <div className="flex items-center gap-1">
          <Tooltip>
            <TooltipTrigger
              render={<Button variant="ghost" size="icon-sm" aria-label={t("chat.history.import")} onClick={() => void onImport()} />}
            >
              <IconFileImport />
            </TooltipTrigger>
            <TooltipContent>{t("chat.history.import")}</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger
              render={
                <Button
                  variant={archive ? "secondary" : "ghost"}
                  size="icon-sm"
                  aria-label={archive ? t("chat.list.showActive") : t("chat.list.showArchive", { count: archivedCount })}
                  aria-pressed={archive}
                  onClick={() => setArchive((a) => !a)}
                />
              }
            >
              <IconArchive />
            </TooltipTrigger>
            <TooltipContent>
              {archive ? t("chat.list.showActive") : t("chat.list.showArchive", { count: archivedCount })}
            </TooltipContent>
          </Tooltip>
          <Button size="sm" onClick={onNew}>
            <IconPlus data-icon="inline-start" />
            {t("chat.list.new")}
          </Button>
        </div>
      </div>
      <ConversationSearch
        archived={archive}
        sites={sites}
        profiles={{ presets: presets ?? [], own: own ?? [] }}
        onOpen={(id) => {
          const c = conversations.find((x) => x.id === id)
          if (c && !!c.archived !== archive) setArchive(!!c.archived)
          onSelect(id)
        }}
        onActive={setSearching}
      />
      <ul className={cn("flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto px-2 pb-2", searching && "hidden")}>
        {ordered.length === 0 && (
          <li className="text-muted-foreground px-2 py-1 text-sm">{archive ? t("chat.list.emptyArchive") : t("chat.list.empty")}</li>
        )}
        {ordered.map((c) => {
          const title = c.title || t("chat.list.untitled")
          return (
            <li key={c.id} className="group/conv relative">
              <button
                type="button"
                onClick={() => onSelect(c.id)}
                aria-current={c.id === selected ? "true" : undefined}
                className={cn(
                  "hover:bg-muted focus-visible:ring-ring/50 flex w-full flex-col rounded-lg px-2 py-1.5 pe-14 text-start text-sm outline-none focus-visible:ring-3",
                  c.id === selected && "bg-muted",
                )}
              >
                <span className="flex items-center gap-1 font-medium">
                  {c.pinned && <IconPinFilled className="text-muted-foreground size-3 shrink-0" aria-label={t("chat.list.pinned")} />}
                  <span dir="auto" className="truncate">{title}</span>
                </span>
                <span className="text-muted-foreground truncate text-xs">
                  {c.site}
                  {c.ephemeral ? ` · ${t("chat.list.notKept")}` : ""}
                </span>
              </button>
              <DropdownMenu>
                <DropdownMenuTrigger
                  render={
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      aria-label={t("chat.list.moreLabel", { title })}
                      className="absolute top-1.5 end-7 opacity-0 group-hover/conv:opacity-100 focus-visible:opacity-100 data-[popup-open]:opacity-100"
                    />
                  }
                >
                  <IconDots />
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-52">
                  <DropdownMenuGroup>
                    <DropdownMenuItem onClick={() => void onPin(c, !c.pinned)}>
                      <IconPin />
                      {c.pinned ? t("chat.list.unpin") : t("chat.list.pin")}
                    </DropdownMenuItem>
                    <DropdownMenuItem onClick={() => void onArchive(c, !c.archived)}>
                      {c.archived ? <IconArchiveOff /> : <IconArchive />}
                      {c.archived ? t("chat.list.unarchive") : t("chat.list.archive")}
                    </DropdownMenuItem>
                  </DropdownMenuGroup>
                  <DropdownMenuSeparator />
                  <DropdownMenuGroup>
                    <DropdownMenuItem onClick={() => void onExport(c, "json")}>
                      <IconFileExport />
                      {t("chat.history.exportJSON")}
                    </DropdownMenuItem>
                    <DropdownMenuItem onClick={() => void onExport(c, "md")}>
                      <IconFileExport />
                      {t("chat.history.exportMarkdown")}
                    </DropdownMenuItem>
                  </DropdownMenuGroup>
                </DropdownMenuContent>
              </DropdownMenu>
              <Tooltip>
                <TooltipTrigger
                  render={
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      aria-label={t("chat.list.deleteLabel", { title })}
                      className="absolute top-1.5 end-1 opacity-0 group-hover/conv:opacity-100 focus-visible:opacity-100"
                      onClick={() => setDoomed(c)}
                    />
                  }
                >
                  <IconTrash />
                </TooltipTrigger>
                <TooltipContent>{t("chat.list.delete")}</TooltipContent>
              </Tooltip>
            </li>
          )
        })}
      </ul>

      <AlertDialog open={!!doomed} onOpenChange={(o) => !o && !busy && setDoomed(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("chat.list.deleteTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("chat.list.deleteBody", { title: doomed?.title || t("chat.list.untitled") })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" disabled={busy} onClick={() => void confirm()}>
              {t("chat.list.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </nav>
  )
}
