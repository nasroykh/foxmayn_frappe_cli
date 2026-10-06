import { IconAlertTriangle, IconDownload, IconInfoCircle } from "@tabler/icons-react"
import * as React from "react"

import { useApp } from "@/app/app-context"
import { CopyField } from "@/components/copy-field"
import { DiffView } from "@/components/diff-view"
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "@/components/ui/accordion"
import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldContent, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { toast } from "@/components/ui/toast"
import { backend } from "@/lib/backend"
import type { Preview } from "@/lib/backend-types"
import { appError, errorTitle, type AppError } from "@/lib/errors"

const FOLLOW = "__default__"

export interface ConnectTarget {
  client?: string
  site?: string
}

export function ConnectDialog({ target, onClose }: { target: ConnectTarget | null; onClose: () => void }) {
  const { assistants, sites, reloadAssistants, installFFC } = useApp()
  const list = assistants.data?.assistants ?? []
  const siteList = sites.data?.sites ?? []
  const defaultSite = sites.data?.defaultSite ?? ""

  const [client, setClient] = React.useState("")
  const [site, setSite] = React.useState(FOLLOW)
  const [readOnly, setReadOnly] = React.useState(false)
  const [preview, setPreview] = React.useState<Preview | null>(null)
  const [previewError, setPreviewError] = React.useState<AppError | null>(null)
  const [loading, setLoading] = React.useState(false)
  const [busy, setBusy] = React.useState(false)

  // Start from the assistant's current settings when it is connected (again
  // once the list arrives, if the dialog opened before it).
  const ready = list.length > 0
  React.useEffect(() => {
    if (!target || !ready) return
    const first = target.client ?? list.find((a) => a.detected && a.status !== "connected")?.id ?? list[0]?.id ?? ""
    const current = list.find((a) => a.id === first)
    setClient(first)
    setSite(target.site ?? (current?.status === "connected" && current.site ? current.site : FOLLOW))
    setReadOnly(current?.status === "connected" ? current.readOnly : false)
    setPreview(null)
    setPreviewError(null)
  }, [target, ready])

  React.useEffect(() => {
    if (!target || !client) return
    let stale = false
    setLoading(true)
    const t = setTimeout(async () => {
      try {
        const p = await backend.preview({ client, site: site === FOLLOW ? "" : site, readOnly })
        if (!stale) {
          setPreview(p)
          setPreviewError(null)
        }
      } catch (err) {
        if (!stale) {
          setPreview(null)
          setPreviewError(appError(err))
        }
      } finally {
        if (!stale) setLoading(false)
      }
    }, 150)
    return () => {
      stale = true
      clearTimeout(t)
    }
  }, [target, client, site, readOnly])

  const name = list.find((a) => a.id === client)?.name ?? "the assistant"

  const clientItems = list.map((a) => ({ value: a.id, label: a.detected ? a.name : `${a.name} (not found)` }))
  const siteItems = [
    { value: FOLLOW, label: defaultSite ? `Default site (${defaultSite})` : "Default site" },
    ...siteList.map((s) => ({ value: s.name, label: s.name })),
  ]

  async function connect() {
    setBusy(true)
    try {
      const res = await backend.connect({ client, site: site === FOLLOW ? "" : site, readOnly })
      await reloadAssistants()
      toast.add({
        title: `${name} is connected`,
        description: [res.hint, res.backup ? "The old settings were backed up next to the file." : ""]
          .filter(Boolean)
          .join(" "),
        type: "success",
      })
      onClose()
    } catch (err) {
      const e = appError(err)
      toast.add({ title: errorTitle(e), description: e.message, type: "error" })
    } finally {
      setBusy(false)
    }
  }

  const summary = !preview
    ? ""
    : !preview.changed
      ? `${name} is already set up exactly like this.`
      : preview.replaces
        ? `This replaces the existing “${preview.entryName}” entry in ${name}'s settings.`
        : preview.createsFile
          ? `This creates ${name}'s settings file with a “${preview.entryName}” entry.`
          : `This adds a “${preview.entryName}” entry to ${name}'s settings.`

  return (
    <Dialog open={!!target} onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent className="max-h-[calc(100dvh-2rem)] grid-rows-[auto_minmax(0,1fr)_auto] sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Connect {name}</DialogTitle>
          <DialogDescription>
            Choose which site it uses and what it may do. Nothing changes until you confirm.
          </DialogDescription>
        </DialogHeader>

        {/* The body scrolls on short windows so the title and buttons stay in view. */}
        <div className="-mx-4 flex min-h-0 flex-col gap-4 overflow-y-auto px-4 py-1">
          <FieldGroup>
            <div className="grid grid-cols-2 gap-4">
              <Field>
                <FieldLabel htmlFor="connect-client">Assistant</FieldLabel>
                <Select items={clientItems} value={client} onValueChange={(v) => v && setClient(v as string)}>
                  <SelectTrigger id="connect-client" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {clientItems.map((i) => (
                        <SelectItem key={i.value} value={i.value}>
                          {i.label}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </Field>
              <Field>
                <FieldLabel htmlFor="connect-site">Site</FieldLabel>
                <Select items={siteItems} value={site} onValueChange={(v) => v && setSite(v as string)}>
                  <SelectTrigger id="connect-site" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {siteItems.map((i) => (
                        <SelectItem key={i.value} value={i.value}>
                          {i.label}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </Field>
            </div>
            <Field orientation="horizontal">
              <FieldContent>
                <FieldLabel htmlFor="connect-readonly">Read-only</FieldLabel>
                <FieldDescription>
                  It can look at your data but cannot create, change or delete anything.
                </FieldDescription>
              </FieldContent>
              <Switch id="connect-readonly" checked={readOnly} onCheckedChange={setReadOnly} />
            </Field>
          </FieldGroup>

          {previewError?.code === "ffc_missing" ? (
            <Alert className="has-data-[slot=alert-action]:pr-28">
              <IconAlertTriangle />
              <AlertTitle>Install the ffc helper first</AlertTitle>
              <AlertDescription>{name} runs ffc to reach your sites.</AlertDescription>
              <AlertAction>
                <Button size="sm" onClick={installFFC}>
                  <IconDownload data-icon="inline-start" />
                  Install
                </Button>
              </AlertAction>
            </Alert>
          ) : previewError ? (
            <Alert variant="destructive">
              <IconAlertTriangle />
              <AlertTitle>The change could not be prepared</AlertTitle>
              <AlertDescription>{previewError.message}</AlertDescription>
            </Alert>
          ) : !preview ? (
            <Skeleton className="h-10 w-full" />
          ) : (
            <div className="flex flex-col gap-3">
              {preview.canApply ? (
                <Alert>
                  <IconInfoCircle />
                  <AlertTitle>{summary}</AlertTitle>
                  <AlertDescription>{preview.hint}</AlertDescription>
                </Alert>
              ) : (
                <Alert variant="destructive">
                  <IconAlertTriangle />
                  <AlertTitle>This app cannot make the change</AlertTitle>
                  <AlertDescription>
                    <p>{preview.problem}</p>
                    {(preview.commands?.length ?? 0) > 0 && <p>You can run this in a terminal instead:</p>}
                  </AlertDescription>
                </Alert>
              )}
              {!preview.canApply &&
                (preview.commands ?? []).map((c) => (
                  <CopyField key={c} value={c} label="Command" copiedTitle="Command copied" />
                ))}
              <Accordion>
                <AccordionItem value="details">
                  <AccordionTrigger>Technical details</AccordionTrigger>
                  <AccordionContent>
                    <Tabs defaultValue="summary">
                      <TabsList>
                        <TabsTrigger value="summary">Summary</TabsTrigger>
                        <TabsTrigger value="diff" disabled={!preview.diff}>
                          Changes
                        </TabsTrigger>
                      </TabsList>
                      <TabsContent value="summary">
                        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 pt-2 text-xs">
                          <dt className="text-muted-foreground">Entry name</dt>
                          <dd className="font-mono">{preview.entryName}</dd>
                          <dt className="text-muted-foreground">Settings file</dt>
                          <dd className="font-mono break-all">{preview.path}</dd>
                          <dt className="text-muted-foreground">Runs</dt>
                          <dd className="font-mono break-all">{(preview.server ?? []).join(" ") || "—"}</dd>
                          {(preview.commands?.length ?? 0) > 0 && (
                            <>
                              <dt className="text-muted-foreground">Through</dt>
                              <dd className="font-mono break-all">{(preview.commands ?? []).join("\n")}</dd>
                            </>
                          )}
                        </dl>
                      </TabsContent>
                      <TabsContent value="diff">
                        <DiffView diff={preview.diff} />
                      </TabsContent>
                    </Tabs>
                  </AccordionContent>
                </AccordionItem>
              </Accordion>
            </div>
          )}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={connect} disabled={busy || loading || !preview || !preview.canApply || !preview.changed}>
            {busy && <Spinner data-icon="inline-start" />}
            {preview && !preview.changed ? "Already connected" : preview?.replaces ? "Replace and connect" : "Connect"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
