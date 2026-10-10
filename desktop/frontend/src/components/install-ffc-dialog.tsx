import { IconAlertTriangle, IconCircleCheck, IconDownload } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { useApp } from "@/app/app-context"
import { CopyField } from "@/components/copy-field"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Progress, ProgressLabel } from "@/components/ui/progress"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Spinner } from "@/components/ui/spinner"
import { toast } from "@/components/ui/toast"
import { backend } from "@/lib/backend"
import type { Cancellable, FFCInfo } from "@/lib/backend-types"
import { appError, type AppError } from "@/lib/errors"

type Phase = "confirm" | "running" | "done" | "failed"

export function InstallFFCDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t } = useTranslation()
  const { env, reloadEnv, reloadAssistants } = useApp()
  const [phase, setPhase] = React.useState<Phase>("confirm")
  const [log, setLog] = React.useState<string[]>([])
  const [error, setError] = React.useState<AppError | null>(null)
  const [result, setResult] = React.useState<FFCInfo | null>(null)
  const run = React.useRef<Cancellable<FFCInfo> | null>(null)
  const logEnd = React.useRef<HTMLDivElement>(null)

  const command = env.data?.installCommand ?? ""
  const isMac = env.data?.os === "darwin"
  // An updatable ffc (a release build the Go side may replace) is replaced
  // where it is; anything else gets a fresh install in the app's folder. The
  // wording follows. Fixed while the dialog runs.
  const [updating, setUpdating] = React.useState(false)
  const installed = env.data?.ffc
  // A package manager's ffc is updated through it; the app offers its command
  // instead of installing (the Go side refuses too).
  const manager = installed?.found ? installed.manager : undefined

  React.useEffect(() => {
    if (open && phase !== "running") {
      setUpdating(!!installed?.updatable)
      setPhase("confirm")
      setLog([])
      setError(null)
      setResult(null)
    }
    // Reset only when the dialog opens.
  }, [open])

  React.useEffect(() => backend.onInstallerLog((line) => setLog((l) => [...l, line])), [])
  // A block body: newer Chromium returns a promise from scrollIntoView, and an effect must not return one.
  React.useEffect(() => {
    logEnd.current?.scrollIntoView({ block: "end" })
  }, [log])

  async function start() {
    setPhase("running")
    setLog([])
    setError(null)
    const p = backend.installFFC()
    run.current = p
    try {
      const info = await p
      setResult(info)
      setPhase("done")
      toast.add({
        title: updating ? t("installFfc.updated") : t("installFfc.installed"),
        description: updating
          ? t("installFfc.readyRestart", { version: info.version || t("installFfc.unknownVersion") })
          : t("installFfc.ready", { version: info.version || t("installFfc.unknownVersion") }),
        type: "success",
      })
      void reloadEnv()
      void reloadAssistants()
    } catch (err) {
      const e = appError(err)
      setError(e)
      setPhase(e.code === "cancelled" ? "confirm" : "failed")
      if (e.code === "cancelled") toast.add({ title: t("installFfc.cancelled"), type: "info" })
      // Refused because a package manager owns the ffc found now: show it.
      if (e.code === "unavailable") void reloadEnv()
    } finally {
      run.current = null
    }
  }

  const running = phase === "running"

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        // Closing while it runs would hide the progress; cancel first.
        if (!o && running) return
        onOpenChange(o)
      }}
    >
      <DialogContent className="max-h-[calc(100dvh-2rem)] overflow-y-auto sm:max-w-lg" showCloseButton={!running}>
        <DialogHeader>
          <DialogTitle>{manager || updating ? t("installFfc.updateTitle") : t("installFfc.installTitle")}</DialogTitle>
          <DialogDescription>
            {manager
              ? installed?.upgradeCommand
                ? t("installFfc.managerRun", { manager })
                : t("installFfc.manager", { manager })
              : updating
                ? t("installFfc.updateBody", { version: installed?.version || "", path: installed?.path ?? "" })
                : t("installFfc.installBody")}
          </DialogDescription>
        </DialogHeader>

        {phase === "confirm" && manager && installed?.upgradeCommand && (
          <CopyField value={installed.upgradeCommand} label={t("installFfc.updateCommand")} copiedTitle={t("common.commandCopied")} />
        )}

        {phase === "confirm" && !updating && !manager && (
          <Collapsible>
            <CollapsibleTrigger render={<Button variant="link" size="sm" className="px-0" />}>
              {t("installFfc.whereTitle")}
            </CollapsibleTrigger>
            <CollapsibleContent className="flex flex-col gap-2 pt-2">
              <p className="text-muted-foreground text-xs">
                {isMac ? t("installFfc.whereMac") : t("installFfc.whereWindows")} {t("installFfc.whereSignature")}
              </p>
              <CopyField value={command} label={t("installFfc.installCommand")} copiedTitle={t("common.commandCopied")} />
            </CollapsibleContent>
          </Collapsible>
        )}

        {phase !== "confirm" && (
          <div className="flex flex-col gap-3">
            {running && (
              <Progress value={null}>
                <ProgressLabel className="flex items-center gap-2">
                  <Spinner /> {t("installFfc.installing")}
                </ProgressLabel>
              </Progress>
            )}
            {phase === "done" && result && (
              <Alert>
                <IconCircleCheck />
                <AlertTitle>{t("installFfc.versionInstalled", { version: result.version })}</AlertTitle>
                <AlertDescription className="break-all">
                  {result.path}
                  {updating && (
                    <span className="block break-normal">
                      {t("installFfc.restartApps")}
                    </span>
                  )}
                </AlertDescription>
              </Alert>
            )}
            {phase === "failed" && error && (
              <Alert variant="destructive">
                <IconAlertTriangle />
                <AlertTitle>{t("installFfc.failed")}</AlertTitle>
                <AlertDescription>{error.message}</AlertDescription>
              </Alert>
            )}
            <ScrollArea className="bg-muted h-40 rounded-lg">
              <div className="p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap" role="log" aria-live="polite">
                {log.length === 0 ? (
                  <span className="text-muted-foreground">{t("installFfc.starting")}</span>
                ) : (
                  log.join("\n")
                )}
                <div ref={logEnd} />
              </div>
            </ScrollArea>
            {phase === "failed" && (
              <div className="flex flex-col gap-2">
                <p className="text-muted-foreground text-xs">{t("installFfc.alsoTerminal")}</p>
                <CopyField value={command} label={t("installFfc.installCommand")} copiedTitle={t("common.commandCopied")} />
              </div>
            )}
          </div>
        )}

        <DialogFooter>
          {phase === "confirm" && manager && (
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              {t("common.close")}
            </Button>
          )}
          {phase === "confirm" && !manager && (
            <>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                {t("installFfc.notNow")}
              </Button>
              <Button onClick={start}>
                <IconDownload data-icon="inline-start" />
                {updating ? t("installFfc.updateButton") : t("installFfc.installButton")}
              </Button>
            </>
          )}
          {running && (
            <Button variant="outline" onClick={() => run.current?.cancel()}>
              {t("common.cancel")}
            </Button>
          )}
          {phase === "failed" && (
            <>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                {t("common.close")}
              </Button>
              <Button onClick={start}>{t("common.retry")}</Button>
            </>
          )}
          {phase === "done" && <Button onClick={() => onOpenChange(false)}>{t("common.done")}</Button>}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
