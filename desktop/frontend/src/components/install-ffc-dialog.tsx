import { IconAlertTriangle, IconCircleCheck, IconDownload } from "@tabler/icons-react"
import * as React from "react"

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
  const { env, reloadEnv, reloadAssistants } = useApp()
  const [phase, setPhase] = React.useState<Phase>("confirm")
  const [log, setLog] = React.useState<string[]>([])
  const [error, setError] = React.useState<AppError | null>(null)
  const [result, setResult] = React.useState<FFCInfo | null>(null)
  const run = React.useRef<Cancellable<FFCInfo> | null>(null)
  const logEnd = React.useRef<HTMLDivElement>(null)

  const command = env.data?.installCommand ?? ""
  const isMac = env.data?.os === "darwin"

  React.useEffect(() => {
    if (open && phase !== "running") {
      setPhase("confirm")
      setLog([])
      setError(null)
      setResult(null)
    }
    // Reset only when the dialog opens.
  }, [open])

  React.useEffect(() => backend.onInstallerLog((line) => setLog((l) => [...l, line])), [])
  React.useEffect(() => logEnd.current?.scrollIntoView({ block: "end" }), [log])

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
        title: "ffc is installed",
        description: `Version ${info.version || "unknown"} is ready.`,
        type: "success",
      })
      void reloadEnv()
      void reloadAssistants()
    } catch (err) {
      const e = appError(err)
      setError(e)
      setPhase(e.code === "cancelled" ? "confirm" : "failed")
      if (e.code === "cancelled") toast.add({ title: "Installation cancelled", type: "info" })
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
      <DialogContent className="sm:max-w-lg" showCloseButton={!running}>
        <DialogHeader>
          <DialogTitle>Install the ffc helper</DialogTitle>
          <DialogDescription>
            Assistants use ffc, a small program from Foxmayn, to talk to your Frappe sites. This downloads the official
            signed release from GitHub and installs it for your user only.
          </DialogDescription>
        </DialogHeader>

        {phase === "confirm" && (
          <Collapsible>
            <CollapsibleTrigger render={<Button variant="link" size="sm" className="px-0" />}>
              Show the command that will run
            </CollapsibleTrigger>
            <CollapsibleContent className="flex flex-col gap-2 pt-2">
              <p className="text-muted-foreground text-xs">
                {isMac
                  ? "It runs the install script with sh. It installs to /usr/local/bin or ~/.local/bin."
                  : "It runs the install script with PowerShell. It installs to your user's Programs folder and adds it to your PATH."}{" "}
                You can also run it yourself in a terminal.
              </p>
              <CopyField value={command} label="Install command" copiedTitle="Command copied" />
            </CollapsibleContent>
          </Collapsible>
        )}

        {phase !== "confirm" && (
          <div className="flex flex-col gap-3">
            {running && (
              <Progress value={null}>
                <ProgressLabel className="flex items-center gap-2">
                  <Spinner /> Installing…
                </ProgressLabel>
              </Progress>
            )}
            {phase === "done" && result && (
              <Alert>
                <IconCircleCheck />
                <AlertTitle>ffc {result.version} is installed</AlertTitle>
                <AlertDescription className="break-all">{result.path}</AlertDescription>
              </Alert>
            )}
            {phase === "failed" && error && (
              <Alert variant="destructive">
                <IconAlertTriangle />
                <AlertTitle>The installation did not finish</AlertTitle>
                <AlertDescription>{error.message}</AlertDescription>
              </Alert>
            )}
            <ScrollArea className="bg-muted h-40 rounded-lg">
              <div className="p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap" role="log" aria-live="polite">
                {log.length === 0 ? (
                  <span className="text-muted-foreground">Waiting for the installer…</span>
                ) : (
                  log.join("\n")
                )}
                <div ref={logEnd} />
              </div>
            </ScrollArea>
            {phase === "failed" && (
              <div className="flex flex-col gap-2">
                <p className="text-muted-foreground text-xs">You can also run the installer yourself in a terminal:</p>
                <CopyField value={command} label="Install command" copiedTitle="Command copied" />
              </div>
            )}
          </div>
        )}

        <DialogFooter>
          {phase === "confirm" && (
            <>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                Not now
              </Button>
              <Button onClick={start}>
                <IconDownload data-icon="inline-start" />
                Install ffc
              </Button>
            </>
          )}
          {running && (
            <Button variant="outline" onClick={() => run.current?.cancel()}>
              Cancel
            </Button>
          )}
          {phase === "failed" && (
            <>
              <Button variant="outline" onClick={() => onOpenChange(false)}>
                Close
              </Button>
              <Button onClick={start}>Try again</Button>
            </>
          )}
          {phase === "done" && <Button onClick={() => onOpenChange(false)}>Done</Button>}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
