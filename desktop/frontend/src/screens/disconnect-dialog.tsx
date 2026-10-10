import { IconAlertTriangle, IconPlugConnectedX } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { useApp } from "@/app/app-context"
import { CopyField } from "@/components/copy-field"
import { DiffView } from "@/components/diff-view"
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "@/components/ui/accordion"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { toast } from "@/components/ui/toast"
import { backend } from "@/lib/backend"
import type { Assistant, Preview } from "@/lib/backend-types"
import { appHint } from "@/lib/labels"
import { appError, errorTitle, type AppError } from "@/lib/errors"

export function DisconnectDialog({ assistant, onClose }: { assistant: Assistant | null; onClose: () => void }) {
  const { t } = useTranslation()
  const { reloadAssistants } = useApp()
  const [kept, setKept] = React.useState<Assistant | null>(null)
  const [preview, setPreview] = React.useState<Preview | null>(null)
  const [error, setError] = React.useState<AppError | null>(null)
  const [busy, setBusy] = React.useState(false)

  React.useEffect(() => {
    if (!assistant) return
    setKept(assistant)
    setPreview(null)
    setError(null)
    let stale = false
    backend
      .previewDisconnect(assistant.id)
      .then((p) => !stale && setPreview(p))
      .catch((err) => !stale && setError(appError(err)))
    return () => {
      stale = true
    }
  }, [assistant])

  async function disconnect() {
    if (!kept) return
    setBusy(true)
    try {
      const res = await backend.disconnect(kept.id)
      await reloadAssistants()
      toast.add(
        res.changed
          ? { title: t("disconnect.done", { name: kept.name }), description: appHint(kept.id, "disconnect", res.hint), type: "success" }
          : { title: t("disconnect.wasNot", { name: kept.name }), description: t("disconnect.nothing"), type: "info" },
      )
      onClose()
    } catch (err) {
      const e = appError(err)
      if (e.code === "unavailable") setError(e)
      else toast.add({ title: errorTitle(e), description: e.message, type: "error" })
    } finally {
      setBusy(false)
    }
  }

  const name = kept?.name ?? t("disconnect.fallbackName")

  return (
    <AlertDialog open={!!assistant} onOpenChange={(o) => !o && !busy && onClose()}>
      <AlertDialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg">
        <AlertDialogHeader>
          <AlertDialogMedia>
            <IconPlugConnectedX />
          </AlertDialogMedia>
          <AlertDialogTitle>{t("disconnect.title", { name })}</AlertDialogTitle>
          <AlertDialogDescription>{t("disconnect.description", { name })}</AlertDialogDescription>
        </AlertDialogHeader>

        {error ? (
          <div className="flex flex-col gap-2">
            <Alert variant={error.code === "unavailable" ? "default" : "destructive"}>
              <IconAlertTriangle />
              <AlertTitle>
                {error.code === "unavailable" ? t("disconnect.unavailable") : t("connect.prepareFailed")}
              </AlertTitle>
              <AlertDescription>{error.message}</AlertDescription>
            </Alert>
            {kept?.configPath && error.code === "unavailable" && (
              <CopyField value={kept.configPath} label={t("disconnect.settingsFile")} copiedTitle={t("disconnect.pathCopied")} />
            )}
          </div>
        ) : !preview ? (
          <Skeleton className="h-9 w-full" />
        ) : (
          <Accordion>
            <AccordionItem value="details">
              <AccordionTrigger>{t("common.technicalDetails")}</AccordionTrigger>
              <AccordionContent>
                <p className="text-muted-foreground font-mono text-xs break-all">{preview.path}</p>
                <DiffView diff={preview.diff} />
                {(preview.commands ?? []).map((c) => (
                  <p key={c} className="text-muted-foreground pt-2 font-mono text-xs break-all">
                    {c}
                  </p>
                ))}
              </AccordionContent>
            </AccordionItem>
          </Accordion>
        )}

        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>{error?.code === "unavailable" ? t("common.close") : t("common.cancel")}</AlertDialogCancel>
          <Button
            variant="destructive"
            onClick={disconnect}
            disabled={busy || !preview || !!error || !preview.canApply}
          >
            {busy && <Spinner data-icon="inline-start" />}
            {t("disconnect.button")}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
