// First use of the Assistant: pick where the model runs, add the key, pick a
// model, start the first conversation.
import { IconAlertTriangle, IconArrowLeft, IconCloud, IconServer, IconSparkles } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { backend } from "@/lib/backend"
import type { Conversation, ProviderInfo, Site } from "@/lib/backend-types"
import { appError, type AppError } from "@/lib/errors"
import { NewConversationForm } from "@/screens/assistant/new-conversation-form"
import { blankProvider, KeyForm, ModelPicker, needsKey, OpenRouterSignIn, usable } from "@/screens/assistant/provider-parts"

type Step = "choose" | "custom" | "key" | "model" | "conversation"

export function AssistantOnboarding({
  sites,
  defaultSite,
  providers,
  onProvidersChanged,
  onDone,
}: {
  sites: Site[]
  defaultSite?: string
  providers: ProviderInfo[]
  /** Called after a provider was added or changed, so the caller reloads its list. */
  onProvidersChanged: () => void
  onDone: (c: Conversation) => void
}) {
  const { t } = useTranslation()
  const [step, setStep] = React.useState<Step>("choose")
  const [provider, setProvider] = React.useState<ProviderInfo | null>(null)
  const [model, setModel] = React.useState("")
  const [local, setLocal] = React.useState<ProviderInfo[] | null>(null)
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<AppError | null>(null)
  const [custom, setCustom] = React.useState({ label: "", baseURL: "" })

  React.useEffect(() => {
    let stale = false
    backend
      .detectLocal()
      .then((l) => !stale && setLocal(l))
      .catch(() => !stale && setLocal([]))
    return () => {
      stale = true
    }
  }, [])

  // Take the saved provider of that kind when there is one (it may only lack its key).
  async function pick(p: ProviderInfo) {
    setBusy(true)
    setError(null)
    try {
      const existing = providers.find((x) => x.kind === p.kind && (p.kind !== "custom" || x.id === p.id))
      const saved = existing ?? (await backend.saveProvider(p))
      if (!existing) onProvidersChanged()
      setProvider(saved)
      setModel(saved.defaultModel)
      setStep(needsKey(saved.kind) && !saved.keySet ? "key" : "model")
    } catch (err) {
      setError(appError(err))
    } finally {
      setBusy(false)
    }
  }

  async function saveCustom(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const saved = await backend.saveProvider({ ...blankProvider("custom"), label: custom.label.trim(), baseURL: custom.baseURL.trim() })
      onProvidersChanged()
      setProvider(saved)
      setModel(saved.defaultModel)
      setStep("key")
    } catch (err) {
      setError(appError(err))
    } finally {
      setBusy(false)
    }
  }

  async function chooseModel() {
    if (!provider) return
    setBusy(true)
    setError(null)
    try {
      const saved = await backend.saveProvider({ ...provider, defaultModel: model })
      onProvidersChanged()
      setProvider(saved)
      setStep("conversation")
    } catch (err) {
      setError(appError(err))
    } finally {
      setBusy(false)
    }
  }

  const back = step !== "choose" && (
    <Button
      variant="ghost"
      size="sm"
      className="self-start"
      onClick={() => {
        setError(null)
        setStep(step === "conversation" ? "model" : "choose")
      }}
    >
      <IconArrowLeft data-icon="inline-start" />
      {t("onboarding.back")}
    </Button>
  )

  return (
    <div className="mx-auto flex w-full max-w-lg flex-col gap-5 p-6">
      <div className="flex flex-col gap-1">
        <h2 className="font-heading flex items-center gap-2 text-xl font-semibold tracking-tight">
          <IconSparkles className="size-5" aria-hidden="true" />
          {t("onboarding.title")}
        </h2>
        <p className="text-muted-foreground text-sm">{t("onboarding.intro")}</p>
      </div>
      {back}

      {error && (
        <Alert variant="destructive" role="alert">
          <IconAlertTriangle />
          <AlertDescription>{error.message}</AlertDescription>
        </Alert>
      )}

      {step === "choose" && (
        <section aria-label={t("onboarding.chooseLabel")} className="flex flex-col gap-2">
          <ChoiceButton
            icon={<IconCloud aria-hidden="true" />}
            title="Anthropic"
            hint={t("onboarding.anthropicHint")}
            disabled={busy}
            onClick={() => void pick(blankProvider("anthropic"))}
          />
          <ChoiceButton
            icon={<IconCloud aria-hidden="true" />}
            title="OpenAI"
            hint={t("onboarding.openaiHint")}
            disabled={busy}
            onClick={() => void pick(blankProvider("openai"))}
          />
          <ChoiceButton
            icon={<IconCloud aria-hidden="true" />}
            title="Google Gemini"
            hint={t("onboarding.geminiHint")}
            disabled={busy}
            onClick={() => void pick(blankProvider("gemini"))}
          />
          <ChoiceButton
            icon={<IconCloud aria-hidden="true" />}
            title="OpenRouter"
            hint={t("onboarding.openrouterHint")}
            disabled={busy}
            onClick={() => void pick(blankProvider("openrouter"))}
          />
          {local === null ? (
            <p className="text-muted-foreground flex items-center gap-2 text-sm" role="status">
              <Spinner /> {t("onboarding.detecting")}
            </p>
          ) : local.length > 0 ? (
            local.map((p) => (
              <ChoiceButton
                key={p.id}
                icon={<IconServer aria-hidden="true" />}
                title={t("onboarding.localFound", { name: p.label })}
                hint={t("onboarding.localHint")}
                disabled={busy}
                onClick={() => void pick(p)}
              />
            ))
          ) : (
            <p className="text-muted-foreground text-sm">{t("onboarding.noLocal")}</p>
          )}
          <ChoiceButton
            icon={<IconServer aria-hidden="true" />}
            title={t("onboarding.custom")}
            hint={t("onboarding.customHint")}
            disabled={busy}
            onClick={() => {
              setError(null)
              setStep("custom")
            }}
          />
        </section>
      )}

      {step === "custom" && (
        <form onSubmit={saveCustom} className="flex flex-col gap-4" noValidate>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="custom-label">{t("onboarding.customName")}</FieldLabel>
              <Input
                id="custom-label"
                value={custom.label}
                onChange={(e) => setCustom({ ...custom, label: e.target.value })}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="custom-url">{t("onboarding.customUrl")}</FieldLabel>
              <Input
                id="custom-url"
                inputMode="url"
                placeholder="https://"
                value={custom.baseURL}
                onChange={(e) => setCustom({ ...custom, baseURL: e.target.value })}
              />
              <FieldDescription>{t("onboarding.customUrlHint")}</FieldDescription>
            </Field>
          </FieldGroup>
          <Button type="submit" disabled={busy || !custom.baseURL.trim()} className="self-end">
            {busy && <Spinner data-icon="inline-start" />}
            {t("onboarding.next")}
          </Button>
        </form>
      )}

      {step === "key" && provider && (
        <div className="flex flex-col gap-4">
          {provider.kind === "openrouter" && (
            <OpenRouterSignIn
              providerID={provider.id}
              onSignedIn={(p) => {
                setProvider({ ...provider, keySet: p.keySet, keyLast4: p.keyLast4 })
                onProvidersChanged()
                setStep("model")
              }}
            />
          )}
          <KeyForm
            providerID={provider.id}
            providerLabel={provider.label || provider.id}
            optional={!needsKey(provider.kind)}
            onSaved={(s) => {
              setProvider({ ...provider, keySet: s.set, keyLast4: s.last4 ?? "" })
              onProvidersChanged()
            }}
          />
          <div className="flex justify-end gap-2">
            {!needsKey(provider.kind) && (
              <Button variant="outline" onClick={() => setStep("model")}>
                {t("onboarding.skipKey")}
              </Button>
            )}
            <Button disabled={!usable(provider)} onClick={() => setStep("model")}>
              {t("onboarding.next")}
            </Button>
          </div>
        </div>
      )}

      {step === "model" && provider && (
        <div className="flex flex-col gap-4">
          <Field>
            <FieldLabel htmlFor="onboarding-model">{t("onboarding.model")}</FieldLabel>
            <ModelPicker id="onboarding-model" providerID={provider.id} value={model} onChange={setModel} />
          </Field>
          <Button disabled={busy || !model} onClick={() => void chooseModel()} className="self-end">
            {busy && <Spinner data-icon="inline-start" />}
            {t("onboarding.next")}
          </Button>
        </div>
      )}

      {step === "conversation" && provider && (
        <div className="flex flex-col gap-3">
          {sites.length === 0 ? (
            <p className="text-muted-foreground text-sm">{t("chat.noSites")}</p>
          ) : (
            <NewConversationForm
              sites={sites}
              defaultSite={defaultSite}
              providers={[provider]}
              providerID={provider.id}
              model={model}
              submitLabel={t("onboarding.startChat")}
              onCreated={onDone}
            />
          )}
        </div>
      )}
    </div>
  )
}

function ChoiceButton({
  icon,
  title,
  hint,
  disabled,
  onClick,
}: {
  icon: React.ReactNode
  title: string
  hint: string
  disabled?: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className="hover:bg-muted focus-visible:ring-ring/50 flex items-start gap-3 rounded-xl border p-3 text-start outline-none focus-visible:ring-3 disabled:opacity-50 [&_svg]:mt-0.5 [&_svg]:size-5 [&_svg]:shrink-0"
    >
      {icon}
      <span className="flex flex-col">
        <span className="font-medium">{title}</span>
        <span className="text-muted-foreground text-sm">{hint}</span>
      </span>
    </button>
  )
}
