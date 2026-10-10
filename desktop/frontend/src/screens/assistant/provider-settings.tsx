// Settings > Assistant providers: where the Assistant's models run.
import { IconAlertTriangle, IconKey, IconPencil, IconPlus, IconRadar, IconTrash } from "@tabler/icons-react"
import type { TFunction } from "i18next"
import * as React from "react"
import { useTranslation } from "react-i18next"

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
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { toast } from "@/components/ui/toast"
import { backend } from "@/lib/backend"
import type { ProviderInfo, ProviderKind } from "@/lib/backend-types"
import { appError, errorTitle, type AppError } from "@/lib/errors"
import {
  blankProvider,
  KeyForm,
  ModelPicker,
  needsKey,
  OpenRouterSignIn,
  SavedLine,
  usable,
} from "@/screens/assistant/provider-parts"

const KINDS: ProviderKind[] = ["anthropic", "openai", "gemini", "openrouter", "ollama", "lmstudio", "custom"]

function kindName(kind: string, t: TFunction): string {
  switch (kind) {
    case "anthropic":
      return "Anthropic"
    case "openai":
      return "OpenAI"
    case "gemini":
      return "Google Gemini"
    case "openrouter":
      return "OpenRouter"
    case "ollama":
      return "Ollama"
    case "lmstudio":
      return "LM Studio"
    default:
      return t("provider.kindCustom")
  }
}

type Dialog_ = { type: "add" } | { type: "edit"; p: ProviderInfo } | { type: "key"; p: ProviderInfo; cleared?: boolean }

export function ProviderSettings() {
  const { t } = useTranslation()
  const [providers, setProviders] = React.useState<ProviderInfo[] | null>(null)
  const [error, setError] = React.useState<AppError | null>(null)
  const [dialog, setDialog] = React.useState<Dialog_ | null>(null)
  const [doomed, setDoomed] = React.useState<ProviderInfo | null>(null)
  const [deleting, setDeleting] = React.useState(false)
  const [detecting, setDetecting] = React.useState(false)
  const [found, setFound] = React.useState<ProviderInfo[] | null>(null)

  const reload = React.useCallback(async () => {
    try {
      setProviders(await backend.listProviders())
      setError(null)
    } catch (err) {
      setError(appError(err))
    }
  }, [])
  React.useEffect(() => {
    void reload()
  }, [reload])

  async function detect() {
    setDetecting(true)
    try {
      setFound(await backend.detectLocal())
    } catch (err) {
      const e = appError(err)
      toast.add({ title: errorTitle(e), description: e.message, type: "error" })
    } finally {
      setDetecting(false)
    }
  }

  async function addFound(p: ProviderInfo) {
    try {
      await backend.saveProvider(p)
      setFound((f) => f?.filter((x) => x.id !== p.id) ?? null)
      await reload()
    } catch (err) {
      const e = appError(err)
      toast.add({ title: errorTitle(e), description: e.message, type: "error" })
    }
  }

  async function remove() {
    if (!doomed) return
    setDeleting(true)
    try {
      await backend.deleteProvider(doomed.id)
      await reload()
    } catch (err) {
      const e = appError(err)
      toast.add({ title: errorTitle(e), description: e.message, type: "error" })
    } finally {
      setDeleting(false)
      setDoomed(null)
    }
  }

  const known = new Set((providers ?? []).map((p) => p.id))
  const newFound = (found ?? []).filter((p) => !known.has(p.id))

  return (
    <section aria-labelledby="providers-title" className="flex max-w-2xl flex-col gap-4">
      <div className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-1">
          <h2 id="providers-title" className="font-heading text-base font-semibold">
            {t("provider.title")}
          </h2>
          <p className="text-muted-foreground text-sm">{t("provider.intro")}</p>
        </div>
        <div className="flex shrink-0 gap-2">
          <Button variant="outline" size="sm" onClick={() => void detect()} disabled={detecting}>
            {detecting ? <Spinner data-icon="inline-start" /> : <IconRadar data-icon="inline-start" />}
            {t("provider.detect")}
          </Button>
          <Button size="sm" onClick={() => setDialog({ type: "add" })}>
            <IconPlus data-icon="inline-start" />
            {t("provider.add")}
          </Button>
        </div>
      </div>

      {error && (
        <Alert variant="destructive" role="alert">
          <IconAlertTriangle />
          <AlertDescription>{error.message}</AlertDescription>
        </Alert>
      )}

      {found && (
        <div className="flex flex-col gap-2 rounded-xl border p-3" aria-live="polite">
          {newFound.length === 0 ? (
            <p className="text-muted-foreground text-sm">
              {found.length === 0 ? t("provider.detectNone") : t("provider.detectAlready")}
            </p>
          ) : (
            newFound.map((p) => (
              <div key={p.id} className="flex items-center justify-between gap-2 text-sm">
                <span>{t("provider.detectFound", { name: p.label, url: p.baseURL })}</span>
                <Button size="sm" onClick={() => void addFound(p)}>
                  {t("provider.addFound")}
                </Button>
              </div>
            ))
          )}
        </div>
      )}

      {!providers && !error && <Skeleton className="h-16 w-full rounded-xl" />}
      {providers?.length === 0 && <p className="text-muted-foreground text-sm">{t("provider.none")}</p>}
      <ul className="flex flex-col gap-2">
        {providers?.map((p) => (
          <li key={p.id} className="flex flex-col gap-2 rounded-xl border p-3">
            <div className="flex items-start justify-between gap-2">
              <div className="flex min-w-0 flex-col gap-0.5">
                <p className="flex items-center gap-2 text-sm font-medium">
                  <span className="truncate">{p.label || p.id}</span>
                  <Badge variant="secondary">{kindName(p.kind, t)}</Badge>
                  {!usable(p) && <Badge variant="destructive">{t("provider.needsKey")}</Badge>}
                </p>
                {p.baseURL && <p className="text-muted-foreground truncate text-xs">{p.baseURL}</p>}
                <p className="text-muted-foreground text-xs">
                  {p.defaultModel
                    ? t("provider.defaultModelIs", { model: p.defaultModel })
                    : t("provider.noDefaultModel")}
                </p>
                {(needsKey(p.kind) || p.keySet) && (
                  <SavedLine status={{ set: p.keySet, last4: p.keyLast4 || undefined }} />
                )}
              </div>
              <div className="flex shrink-0 gap-1">
                {(needsKey(p.kind) || p.kind === "custom") && (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setDialog({ type: "key", p })}
                    aria-label={t("provider.rekeyLabel", { name: p.label || p.id })}
                  >
                    <IconKey data-icon="inline-start" />
                    {p.keySet ? t("provider.rekey") : t("provider.addKey")}
                  </Button>
                )}
                <Button
                  variant="ghost"
                  size="icon-sm"
                  onClick={() => setDialog({ type: "edit", p })}
                  aria-label={t("provider.editLabel", { name: p.label || p.id })}
                >
                  <IconPencil />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  onClick={() => setDoomed(p)}
                  aria-label={t("provider.deleteLabel", { name: p.label || p.id })}
                >
                  <IconTrash />
                </Button>
              </div>
            </div>
          </li>
        ))}
      </ul>

      <ProviderDialog
        dialog={dialog}
        onClose={() => setDialog(null)}
        onChanged={() => void reload()}
        onSwitch={setDialog}
      />

      <AlertDialog open={!!doomed} onOpenChange={(o) => !o && !deleting && setDoomed(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("provider.deleteTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("provider.deleteBody", { name: doomed?.label || doomed?.id || "" })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleting}>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" disabled={deleting} onClick={() => void remove()}>
              {t("provider.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}

function ProviderDialog({
  dialog,
  onClose,
  onChanged,
  onSwitch,
}: {
  dialog: Dialog_ | null
  onClose: () => void
  onChanged: () => void
  onSwitch: (d: Dialog_) => void
}) {
  const { t } = useTranslation()
  // Keep the last content while the dialog fades out.
  const [kept, setKept] = React.useState<Dialog_ | null>(null)
  React.useEffect(() => {
    if (dialog) setKept(dialog)
  }, [dialog])
  const d = dialog ?? kept

  return (
    <Dialog open={!!dialog} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        {d?.type === "key" ? (
          <>
            <DialogHeader>
              <DialogTitle>{t("provider.keyDialogTitle", { name: d.p.label || d.p.id })}</DialogTitle>
              <DialogDescription>{t("provider.keyDialogBody")}</DialogDescription>
            </DialogHeader>
            {d.cleared && (
              <Alert role="alert">
                <IconAlertTriangle />
                <AlertDescription>{t("provider.keyCleared")}</AlertDescription>
              </Alert>
            )}
            {d.p.kind === "openrouter" && (
              <OpenRouterSignIn
                providerID={d.p.id}
                onSignedIn={() => {
                  onChanged()
                  onClose()
                }}
              />
            )}
            <KeyForm
              providerID={d.p.id}
              providerLabel={d.p.label || d.p.id}
              optional={!needsKey(d.p.kind)}
              onSaved={onChanged}
            />
          </>
        ) : d ? (
          <ProviderForm
            key={d.type === "edit" ? d.p.id : "new"}
            provider={d.type === "edit" ? d.p : null}
            onChanged={onChanged}
            onClose={onClose}
            onSwitch={onSwitch}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  )
}

function ProviderForm({
  provider,
  onChanged,
  onClose,
  onSwitch,
}: {
  provider: ProviderInfo | null
  onChanged: () => void
  onClose: () => void
  onSwitch: (d: Dialog_) => void
}) {
  const { t } = useTranslation()
  const editing = provider !== null
  const [kind, setKind] = React.useState<ProviderKind>((provider?.kind as ProviderKind) ?? "anthropic")
  const [label, setLabel] = React.useState(provider?.label ?? "")
  const [baseURL, setBaseURL] = React.useState(provider?.baseURL ?? "")
  const [model, setModel] = React.useState(provider?.defaultModel ?? "")
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<AppError | null>(null)
  const hasBase = kind !== "anthropic" && kind !== "openai" && kind !== "gemini"
  const canPickModel = editing && provider !== null && usable(provider)

  async function save(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const saved = await backend.saveProvider({
        ...(provider ?? blankProvider(kind)),
        kind,
        label: label.trim(),
        baseURL: hasBase ? baseURL.trim() : "",
        defaultModel: model,
      })
      onChanged()
      // A new provider that needs its key goes on to the key; so does one whose address
      // changed, since the saved key was removed with the old address.
      if (saved.keyCleared) onSwitch({ type: "key", p: saved, cleared: true })
      else if (!editing && needsKey(saved.kind)) onSwitch({ type: "key", p: saved })
      else onClose()
    } catch (err) {
      setError(appError(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={save} className="flex flex-col gap-4" noValidate>
      <DialogHeader>
        <DialogTitle>{editing ? t("provider.editTitle") : t("provider.addTitle")}</DialogTitle>
        <DialogDescription>{editing ? t("provider.editBody") : t("provider.addBody")}</DialogDescription>
      </DialogHeader>
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="provider-kind">{t("provider.kind")}</FieldLabel>
          <NativeSelect
            id="provider-kind"
            className="w-full"
            value={kind}
            disabled={editing}
            onChange={(e) => setKind(e.target.value as ProviderKind)}
          >
            {KINDS.map((k) => (
              <NativeSelectOption key={k} value={k}>
                {kindName(k, t)}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </Field>
        <Field>
          <FieldLabel htmlFor="provider-label">{t("provider.name")}</FieldLabel>
          <Input id="provider-label" value={label} onChange={(e) => setLabel(e.target.value)} />
        </Field>
        {hasBase && (
          <Field>
            <FieldLabel htmlFor="provider-url">{t("provider.url")}</FieldLabel>
            <Input
              id="provider-url"
              inputMode="url"
              placeholder="https://"
              value={baseURL}
              onChange={(e) => setBaseURL(e.target.value)}
            />
            <FieldDescription>{t("provider.urlHint")}</FieldDescription>
          </Field>
        )}
        {editing && (
          <Field>
            <FieldLabel htmlFor="provider-model">{t("provider.defaultModel")}</FieldLabel>
            {canPickModel && provider ? (
              <ModelPicker id="provider-model" providerID={provider.id} value={model} onChange={setModel} />
            ) : (
              <Input id="provider-model" value={model} onChange={(e) => setModel(e.target.value)} />
            )}
          </Field>
        )}
      </FieldGroup>
      {error && (
        <Alert variant="destructive" role="alert">
          <IconAlertTriangle />
          <AlertDescription>{error.message}</AlertDescription>
        </Alert>
      )}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={onClose} disabled={busy}>
          {t("common.cancel")}
        </Button>
        <Button type="submit" disabled={busy || (kind === "custom" && !baseURL.trim())}>
          {busy && <Spinner data-icon="inline-start" />}
          {editing ? t("provider.saveChanges") : needsKey(kind) ? t("provider.addAndKey") : t("provider.add")}
        </Button>
      </div>
    </form>
  )
}
