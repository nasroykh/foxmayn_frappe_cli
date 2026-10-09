// Small pieces shared by the assistant onboarding, the new-conversation form
// and the provider settings: the key form, the model picker and the helpers
// that say what a provider needs.
import { IconAlertTriangle, IconCircleCheck } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import { Spinner } from "@/components/ui/spinner"
import { backend } from "@/lib/backend"
import type { KeyStatus, Model, ProviderInfo, ProviderKind } from "@/lib/backend-types"
import { appError, type AppError } from "@/lib/errors"

/** Anthropic and OpenRouter cannot be used without a key; the others may run without one. */
export function needsKey(kind: string): boolean {
  return kind === "anthropic" || kind === "openrouter"
}

/** A provider a conversation can start with. */
export function usable(p: ProviderInfo): boolean {
  return !needsKey(p.kind) || p.keySet
}

export function blankProvider(kind: ProviderKind): ProviderInfo {
  return { id: "", kind, label: "", baseURL: "", defaultModel: "", keySet: false, keyLast4: "" }
}

/** "Saved, ends in ••••1234" (or just "Saved" when the key is too short to show an end). */
export function SavedLine({ status }: { status: KeyStatus }) {
  const { t } = useTranslation()
  if (!status.set) return <p className="text-muted-foreground text-xs">{t("provider.key.notSet")}</p>
  return (
    <p className="text-muted-foreground flex items-center gap-1.5 text-xs">
      <IconCircleCheck className="size-3.5" aria-hidden="true" />
      {status.last4 ? t("provider.key.savedEnds", { last4: status.last4 }) : t("provider.key.saved")}
    </p>
  )
}

/**
 * Paste an API key. The field is a password field and is cleared after a
 * save; the key is never shown again, only whether one is saved and its end.
 */
export function KeyForm({
  providerID,
  providerLabel,
  onSaved,
  optional,
}: {
  providerID: string
  providerLabel: string
  onSaved?: (status: KeyStatus) => void
  optional?: boolean
}) {
  const { t } = useTranslation()
  const [key, setKey] = React.useState("")
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<AppError | null>(null)
  const [status, setStatus] = React.useState<KeyStatus | null>(null)

  React.useEffect(() => {
    let stale = false
    backend
      .keyStatus(providerID)
      .then((s) => !stale && setStatus(s))
      .catch(() => {})
    return () => {
      stale = true
    }
  }, [providerID])

  async function save(e: React.FormEvent) {
    e.preventDefault()
    if (!key.trim()) {
      setError({ code: "invalid", message: t("provider.key.empty") })
      return
    }
    setBusy(true)
    setError(null)
    try {
      await backend.setKey(providerID, key)
      const s = await backend.keyStatus(providerID)
      setKey("")
      setStatus(s)
      onSaved?.(s)
    } catch (err) {
      setError(appError(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={save} className="flex flex-col gap-3" noValidate>
      <Field data-invalid={error ? true : undefined}>
        <FieldLabel htmlFor={`key-${providerID}`}>{t("provider.key.label", { name: providerLabel })}</FieldLabel>
        <div className="flex gap-2">
          <Input
            id={`key-${providerID}`}
            type="password"
            autoComplete="off"
            spellCheck={false}
            value={key}
            onChange={(e) => setKey(e.target.value)}
            aria-invalid={error ? true : undefined}
            disabled={busy}
          />
          <Button type="submit" disabled={busy || !key}>
            {busy && <Spinner data-icon="inline-start" />}
            {busy ? t("provider.key.checking") : t("provider.key.save")}
          </Button>
        </div>
        <FieldDescription>
          {optional ? t("provider.key.optionalHint") : t("provider.key.hint")}
        </FieldDescription>
        {status && <SavedLine status={status} />}
      </Field>
      {error && (
        <Alert variant="destructive" role="alert">
          <IconAlertTriangle />
          <AlertTitle>{t("provider.key.rejected")}</AlertTitle>
          <AlertDescription>{error.message}</AlertDescription>
        </Alert>
      )}
    </form>
  )
}

/** The models a provider offers; the default one is flagged and picked first. */
export function ModelPicker({
  id,
  providerID,
  value,
  onChange,
}: {
  id: string
  providerID: string
  value: string
  onChange: (model: string) => void
}) {
  const { t } = useTranslation()
  const [models, setModels] = React.useState<Model[] | null>(null)
  const [error, setError] = React.useState<AppError | null>(null)
  const onChangeRef = React.useRef(onChange)
  onChangeRef.current = onChange
  const valueRef = React.useRef(value)
  valueRef.current = value

  React.useEffect(() => {
    let stale = false
    setModels(null)
    setError(null)
    backend
      .listModels(providerID)
      .then((list) => {
        if (stale) return
        setModels(list)
        if (!list.some((m) => m.id === valueRef.current)) {
          const pick = list.find((m) => m.default) ?? list[0]
          if (pick) onChangeRef.current(pick.id)
        }
      })
      .catch((err) => !stale && setError(appError(err)))
    return () => {
      stale = true
    }
  }, [providerID])

  if (error) {
    return (
      <Alert variant="destructive" role="alert">
        <IconAlertTriangle />
        <AlertTitle>{t("provider.models.failed")}</AlertTitle>
        <AlertDescription>{error.message}</AlertDescription>
      </Alert>
    )
  }
  if (!models) {
    return (
      <p className="text-muted-foreground flex items-center gap-2 text-sm" role="status">
        <Spinner /> {t("provider.models.loading")}
      </p>
    )
  }
  if (models.length === 0) return <p className="text-muted-foreground text-sm">{t("provider.models.none")}</p>
  return (
    <NativeSelect id={id} className="w-full" value={value} onChange={(e) => onChange(e.target.value)}>
      {models.map((m) => (
        <NativeSelectOption key={m.id} value={m.id}>
          {m.default ? t("provider.models.defaultFlag", { name: m.label || m.id }) : m.label || m.id}
        </NativeSelectOption>
      ))}
    </NativeSelect>
  )
}
