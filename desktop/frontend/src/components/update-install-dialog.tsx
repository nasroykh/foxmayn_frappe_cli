import { IconAlertTriangle, IconDownload } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { CopyField } from "@/components/copy-field"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Progress, ProgressLabel } from "@/components/ui/progress"
import { Spinner } from "@/components/ui/spinner"
import { toast } from "@/components/ui/toast"
import { intlLocale } from "@/i18n"
import { backend } from "@/lib/backend"
import type { Cancellable, UpdateInfo, UpdateInstall, UpdateProgress } from "@/lib/backend-types"
import { appError, type AppError } from "@/lib/errors"
import { hasUnsent } from "@/lib/unsent"

type Phase = "confirm" | "running" | "failed"

function megabytes(n: number) {
  return new Intl.NumberFormat(intlLocale(), { maximumFractionDigits: 1 }).format(n / 1e6)
}

/**
 * Asks, warns, then runs AppService.InstallUpdate: Go downloads, verifies,
 * installs and restarts the app, so a success never returns here (the app
 * quits). Cancel works until the install step; a failure shows the keyed
 * message and the copy-command fallback.
 */
export function UpdateInstallDialog({
  info,
  onClose,
}: {
  /** The update to offer; null keeps the dialog closed. */
  info: UpdateInfo | null
  onClose: () => void
}) {
  const { t } = useTranslation()
  const open = info !== null
  const [phase, setPhase] = React.useState<Phase>("confirm")
  const [progress, setProgress] = React.useState<UpdateProgress>({ step: "checking", done: 0, total: 0 })
  const [error, setError] = React.useState<AppError | null>(null)
  const [warn, setWarn] = React.useState({ run: false, draft: false })
  const run = React.useRef<Cancellable<UpdateInstall> | null>(null)

  // Reset when it opens, and look for work the restart would lose.
  React.useEffect(() => {
    if (!open) return
    setPhase("confirm")
    setError(null)
    setProgress({ step: "checking", done: 0, total: 0 })
    setWarn({ run: false, draft: hasUnsent() })
    let alive = true
    backend.activeRuns().then(
      (n) => alive && setWarn((w) => ({ ...w, run: n > 0 })),
      () => {},
    )
    return () => {
      alive = false
    }
  }, [open])

  React.useEffect(() => backend.onUpdateProgress(setProgress), [])

  async function start() {
    setPhase("running")
    setError(null)
    setProgress({ step: "checking", done: 0, total: 0 })
    const p = backend.installUpdate()
    run.current = p
    try {
      await p
      // Go started the new copy and quits the app; nothing else to do.
    } catch (err) {
      const e = appError(err)
      if (e.code === "cancelled") {
        setPhase("confirm")
        toast.add({ title: t("updateInstall.cancelled"), type: "info" })
      } else {
        setError(e)
        setPhase("failed")
      }
    } finally {
      run.current = null
    }
  }

  const running = phase === "running"
  // After this step the call cannot be stopped (and the app restarts).
  const cancellable = running && progress.step !== "installing" && progress.step !== "restarting"
  const known = progress.step === "downloading" && progress.total > 0
  const pct = known ? Math.min(100, Math.round((progress.done / progress.total) * 100)) : null
  const command = info?.installCommand ?? ""

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        // Closing while it runs would hide the progress; cancel first.
        if (!o && !running) onClose()
      }}
    >
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg" showCloseButton={!running}>
        <DialogHeader>
          <DialogTitle>{t("updateInstall.title", { version: info?.latest ?? "" })}</DialogTitle>
          <DialogDescription>{t("updateInstall.body")}</DialogDescription>
        </DialogHeader>

        {phase === "confirm" && (warn.run || warn.draft) && (
          <Alert>
            <IconAlertTriangle />
            <AlertTitle>{t("updateInstall.warnTitle")}</AlertTitle>
            <AlertDescription className="flex flex-col gap-1">
              {warn.run && <span>{t("updateInstall.warnRun")}</span>}
              {warn.draft && <span>{t("updateInstall.warnDraft")}</span>}
            </AlertDescription>
          </Alert>
        )}

        {running && (
          <div className="flex flex-col gap-2" role="status" aria-live="polite" aria-label={t("updateInstall.progress")}>
            <Progress value={pct}>
              <ProgressLabel className="flex items-center gap-2">
                <Spinner /> {t(`updateInstall.step.${progress.step}`, { defaultValue: progress.step })}
                {known && (
                  <span className="text-muted-foreground text-xs">
                    {t("updateInstall.megabytes", { done: megabytes(progress.done), total: megabytes(progress.total) })}
                  </span>
                )}
              </ProgressLabel>
            </Progress>
            {(progress.step === "installing" || progress.step === "restarting") && (
              <p className="text-muted-foreground text-xs">{t("updateInstall.restartNote")}</p>
            )}
          </div>
        )}

        {phase === "failed" && error && (
          <div className="flex flex-col gap-3">
            <Alert variant="destructive">
              <IconAlertTriangle />
              <AlertTitle>{t("updateInstall.failed")}</AlertTitle>
              <AlertDescription>{error.message}</AlertDescription>
            </Alert>
            {command && (
              <div className="flex flex-col gap-2">
                <p className="text-muted-foreground text-xs">{t("updateInstall.alsoTerminal")}</p>
                <CopyField wrap value={command} label={t("settings.installCommand")} copiedTitle={t("shell.update.commandCopied")} />
              </div>
            )}
          </div>
        )}

        <DialogFooter>
          {phase === "confirm" && (
            <>
              <Button variant="outline" onClick={onClose}>
                {t("updateInstall.notNow")}
              </Button>
              <Button onClick={() => void start()}>
                <IconDownload data-icon="inline-start" />
                {t("updateInstall.install")}
              </Button>
            </>
          )}
          {running && cancellable && (
            <Button variant="outline" onClick={() => run.current?.cancel()}>
              {t("common.cancel")}
            </Button>
          )}
          {phase === "failed" && (
            <>
              <Button variant="outline" onClick={onClose}>
                {t("common.close")}
              </Button>
              <Button onClick={() => void start()}>{t("common.retry")}</Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// i18n keys: updateInstall.step.checking, updateInstall.step.downloading, updateInstall.step.verifying,
// updateInstall.step.installing, updateInstall.step.restarting
