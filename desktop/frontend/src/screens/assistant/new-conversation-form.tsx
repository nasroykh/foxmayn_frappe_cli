import { IconAlertTriangle } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import { Spinner } from "@/components/ui/spinner"
import { backend } from "@/lib/backend"
import type { Conversation, ProviderInfo, Site } from "@/lib/backend-types"
import { appError, type AppError } from "@/lib/errors"
import { ModelPicker } from "@/screens/assistant/provider-parts"

/** Site, provider and model for a new conversation. It starts in "Read only". */
export function NewConversationForm({
  sites,
  defaultSite,
  providers,
  providerID: initialProvider,
  model: initialModel,
  submitLabel,
  onCreated,
}: {
  sites: Site[]
  defaultSite?: string
  providers: ProviderInfo[]
  providerID?: string
  model?: string
  submitLabel?: string
  onCreated: (c: Conversation) => void
}) {
  const { t } = useTranslation()
  const first = providers.find((p) => p.id === initialProvider) ?? providers[0]
  const [site, setSite] = React.useState(
    sites.find((s) => s.name === defaultSite)?.name ?? sites[0]?.name ?? "",
  )
  const [providerID, setProviderID] = React.useState(first?.id ?? "")
  const provider = providers.find((p) => p.id === providerID)
  const [model, setModel] = React.useState(
    initialModel || (provider && provider.id === first?.id ? provider.defaultModel : "") || "",
  )
  const [busy, setBusy] = React.useState(false)
  const [error, setError] = React.useState<AppError | null>(null)

  async function create(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      onCreated(await backend.newConversation(site, "read", providerID, model))
    } catch (err) {
      setError(appError(err))
      setBusy(false)
    }
  }

  return (
    <form onSubmit={create} className="flex flex-col gap-4">
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="newconv-site">{t("chat.new.site")}</FieldLabel>
          <NativeSelect id="newconv-site" className="w-full" value={site} onChange={(e) => setSite(e.target.value)}>
            {sites.map((s) => (
              <NativeSelectOption key={s.name} value={s.name}>
                {s.name}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </Field>
        {providers.length > 1 && (
          <Field>
            <FieldLabel htmlFor="newconv-provider">{t("chat.new.provider")}</FieldLabel>
            <NativeSelect
              id="newconv-provider"
              className="w-full"
              value={providerID}
              onChange={(e) => {
                setProviderID(e.target.value)
                setModel(providers.find((p) => p.id === e.target.value)?.defaultModel ?? "")
              }}
            >
              {providers.map((p) => (
                <NativeSelectOption key={p.id} value={p.id}>
                  {p.label || p.id}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </Field>
        )}
        {provider && (
          <Field>
            <FieldLabel htmlFor="newconv-model">{t("chat.new.model")}</FieldLabel>
            <ModelPicker id="newconv-model" providerID={provider.id} value={model} onChange={setModel} />
          </Field>
        )}
      </FieldGroup>
      {error && (
        <Alert variant="destructive" role="alert">
          <IconAlertTriangle />
          <AlertDescription>{error.message}</AlertDescription>
        </Alert>
      )}
      <Button type="submit" disabled={busy || !site || !providerID} className="self-end">
        {busy && <Spinner data-icon="inline-start" />}
        {submitLabel ?? t("chat.new.start")}
      </Button>
    </form>
  )
}
