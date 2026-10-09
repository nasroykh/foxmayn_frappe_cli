// Small pieces shared by the assistant onboarding, the new-conversation form
// and the provider settings: the key form, the model picker and the helpers
// that say what a provider needs.
import { IconAlertTriangle, IconCircleCheck, IconExternalLink } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { CopyField } from "@/components/copy-field"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import { Spinner } from "@/components/ui/spinner"
import { backend } from "@/lib/backend"
import type { KeyStatus, Model, OpenRouterAuthStatus, ProviderInfo, ProviderKind } from "@/lib/backend-types"
import { appError, type AppError } from "@/lib/errors"

/** Anthropic, OpenAI, Gemini and OpenRouter cannot be used without a key; the others may run without one. */
export function needsKey(kind: string): boolean {
  return kind === "anthropic" || kind === "openrouter" || kind === "openai" || kind === "gemini"
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

/**
 * "Sign in with OpenRouter": the browser sign-in that creates a key for the
 * app. The key never reaches the page; onSignedIn gets the provider with its
 * key status. Leaving the screen cancels a sign-in in progress.
 */
export function OpenRouterSignIn({
  providerID,
  onSignedIn,
}: {
  providerID: string
  onSignedIn?: (p: ProviderInfo) => void
}) {
  const { t } = useTranslation()
  const [busy, setBusy] = React.useState(false)
  const [status, setStatus] = React.useState<OpenRouterAuthStatus | null>(null)
  const [page, setPage] = React.useState({ url: "", browserError: "" })
  const [error, setError] = React.useState<AppError | null>(null)
  const busyRef = React.useRef(false)
  const live = React.useRef(true)

  React.useEffect(
    () =>
      backend.onOpenRouterAuth((ev) => {
        if (ev.providerID !== providerID || !busyRef.current) return
        setStatus(ev.status)
        if (ev.status === "browser") setPage({ url: ev.authURL ?? "", browserError: ev.browserError ?? "" })
      }),
    [providerID],
  )
  React.useEffect(() => {
    live.current = true
    return () => {
      live.current = false
      if (busyRef.current) void backend.cancelOpenRouterSignIn()
    }
  }, [])

  async function start() {
    busyRef.current = true
    setBusy(true)
    setStatus(null)
    setPage({ url: "", browserError: "" })
    setError(null)
    try {
      const p = await backend.signInOpenRouter(providerID)
      if (live.current) onSignedIn?.(p)
    } catch (err) {
      const e = appError(err)
      if (live.current && e.code !== "cancelled") setError(e)
    } finally {
      busyRef.current = false
      if (live.current) {
        setBusy(false)
        setStatus(null)
      }
    }
  }

  const step =
    status === "exchanging"
      ? t("provider.openrouter.exchanging")
      : status === "verifying"
        ? t("provider.openrouter.verifying")
        : t("provider.openrouter.browser")

  return (
    <div className="flex flex-col gap-3">
      {busy ? (
        <div className="flex flex-wrap items-center justify-between gap-2">
          <p className="text-muted-foreground flex items-center gap-2 text-sm" role="status">
            <Spinner /> {step}
          </p>
          <Button variant="outline" size="sm" onClick={() => void backend.cancelOpenRouterSignIn()}>
            {t("provider.openrouter.cancel")}
          </Button>
        </div>
      ) : (
        <div className="flex flex-col gap-1.5">
          <Button className="self-start" onClick={() => void start()}>
            <IconExternalLink data-icon="inline-start" />
            {t("provider.openrouter.signIn")}
          </Button>
          <p className="text-muted-foreground text-xs">{t("provider.openrouter.hint")}</p>
        </div>
      )}
      {busy && page.browserError && page.url && (
        <Alert role="alert">
          <IconAlertTriangle />
          <AlertDescription className="flex flex-col gap-2">
            {t("provider.openrouter.browserFailed")}
            <CopyField value={page.url} label={t("provider.openrouter.pageLabel")} />
          </AlertDescription>
        </Alert>
      )}
      {error && (
        <Alert variant="destructive" role="alert">
          <IconAlertTriangle />
          <AlertTitle>{t("provider.openrouter.failed")}</AlertTitle>
          <AlertDescription>{error.message}</AlertDescription>
        </Alert>
      )}
      <p className="text-muted-foreground text-xs font-medium">{t("provider.openrouter.orPaste")}</p>
    </div>
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
