import { IconPlus, IconTrash } from "@tabler/icons-react"
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
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { Conversation } from "@/lib/backend-types"

/** The conversations, newest first, with new and delete (after a confirmation). */
export function ConversationList({
  conversations,
  selected,
  onSelect,
  onNew,
  onDelete,
}: {
  conversations: Conversation[]
  selected: string
  onSelect: (id: string) => void
  onNew: () => void
  onDelete: (c: Conversation) => Promise<void>
}) {
  const { t } = useTranslation()
  const [doomed, setDoomed] = React.useState<Conversation | null>(null)
  const [busy, setBusy] = React.useState(false)

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

  return (
    <nav aria-label={t("chat.list.label")} className="flex h-full min-h-0 w-60 shrink-0 flex-col border-r">
      <div className="flex items-center justify-between gap-2 p-3">
        <h2 className="text-sm font-semibold">{t("chat.list.title")}</h2>
        <Button size="sm" onClick={onNew}>
          <IconPlus data-icon="inline-start" />
          {t("chat.list.new")}
        </Button>
      </div>
      <ul className="flex min-h-0 flex-1 flex-col gap-0.5 overflow-y-auto px-2 pb-2">
        {conversations.length === 0 && <li className="text-muted-foreground px-2 py-1 text-sm">{t("chat.list.empty")}</li>}
        {conversations.map((c) => (
          <li key={c.id} className="group/conv relative">
            <button
              type="button"
              onClick={() => onSelect(c.id)}
              aria-current={c.id === selected ? "true" : undefined}
              className={cn(
                "hover:bg-muted focus-visible:ring-ring/50 flex w-full flex-col rounded-lg px-2 py-1.5 pr-9 text-left text-sm outline-none focus-visible:ring-3",
                c.id === selected && "bg-muted",
              )}
            >
              <span className="truncate font-medium">{c.title || t("chat.list.untitled")}</span>
              <span className="text-muted-foreground truncate text-xs">{c.site}</span>
            </button>
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    aria-label={t("chat.list.deleteLabel", { title: c.title || t("chat.list.untitled") })}
                    className="absolute top-1.5 right-1 opacity-0 group-hover/conv:opacity-100 focus-visible:opacity-100"
                    onClick={() => setDoomed(c)}
                  />
                }
              >
                <IconTrash />
              </TooltipTrigger>
              <TooltipContent>{t("chat.list.delete")}</TooltipContent>
            </Tooltip>
          </li>
        ))}
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
