// The conversation header's profile picker and the prompt preview.
import { IconFileDescription } from "@tabler/icons-react"
import * as React from "react"
import type { TFunction } from "i18next"
import { useTranslation } from "react-i18next"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { NativeSelect, NativeSelectOptGroup, NativeSelectOption } from "@/components/ui/native-select"
import { Skeleton } from "@/components/ui/skeleton"
import { toast } from "@/components/ui/toast"
import { backend } from "@/lib/backend"
import type { Conversation, Profile, PromptPreview } from "@/lib/backend-types"
import { appError, errorTitle, type AppError } from "@/lib/errors"

/**
 * A profile's name: a built-in preset by its id in the page's language
 * (Go sends the English name), a user's profile as they named it.
 */
// i18n keys: profile.preset.explore.name profile.preset.accounts.name profile.preset.site-admin.name profile.preset.data-entry.name profile.preset.local-model.name
export function profileName(t: TFunction, p: Pick<Profile, "id" | "name" | "preset">): string {
  return p.preset ? t(`profile.preset.${p.id}.name`, { defaultValue: p.name }) : p.name
}

/** Presets and the user's own profiles, loaded once per mount. */
export function useProfiles() {
  const [presets, setPresets] = React.useState<Profile[] | null>(null)
  const [own, setOwn] = React.useState<Profile[] | null>(null)
  const [error, setError] = React.useState<AppError | null>(null)
  const reload = React.useCallback(async () => {
    try {
      const [p, o] = await Promise.all([backend.listPresets(), backend.listProfiles()])
      setPresets(p)
      setOwn(o)
      setError(null)
    } catch (err) {
      setError(appError(err))
    }
  }, [])
  React.useEffect(() => {
    void reload()
  }, [reload])
  return { presets, own, error, reload }
}

/** Picks the conversation's profile. A profile can only narrow what the site allows. */
export function ProfilePicker({
  conv,
  disabled,
  onChanged,
}: {
  conv: Conversation
  /** A run is active: the profile cannot change. */
  disabled: boolean
  onChanged: (c: Conversation) => void
}) {
  const { t } = useTranslation()
  const { presets, own } = useProfiles()
  const [busy, setBusy] = React.useState(false)
  const [previewOpen, setPreviewOpen] = React.useState(false)

  const all = [...(presets ?? []), ...(own ?? [])]
  const current = all.find((p) => p.id === conv.profileID)
  // The switch says "ask" but the profile is read only: the stricter wins.
  const narrowed = conv.mode === "ask" && current?.mode === "read"

  async function change(id: string) {
    if (id === conv.profileID) return
    setBusy(true)
    try {
      onChanged(await backend.setConversationProfile(conv.id, id))
    } catch (err) {
      const e = appError(err)
      toast.add({ title: errorTitle(e), description: e.message, type: "error" })
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex items-center gap-2">
      {presets ? (
        <NativeSelect
          size="sm"
          aria-label={t("profile.picker.label")}
          value={conv.profileID}
          disabled={disabled || busy}
          onChange={(e) => void change(e.target.value)}
        >
          <NativeSelectOption value="">{t("profile.picker.none")}</NativeSelectOption>
          <NativeSelectOptGroup label={t("profile.picker.presets")}>
            {presets.map((p) => (
              <NativeSelectOption key={p.id} value={p.id}>
                {profileName(t, p)}
              </NativeSelectOption>
            ))}
          </NativeSelectOptGroup>
          {(own?.length ?? 0) > 0 && (
            <NativeSelectOptGroup label={t("profile.picker.own")}>
              {own!.map((p) => (
                <NativeSelectOption key={p.id} value={p.id}>
                  {p.name}
                </NativeSelectOption>
              ))}
            </NativeSelectOptGroup>
          )}
          {conv.profileID !== "" && !current && own && (
            <NativeSelectOption value={conv.profileID}>{t("profile.picker.missing")}</NativeSelectOption>
          )}
        </NativeSelect>
      ) : (
        <Skeleton className="h-7 w-32" />
      )}
      {narrowed && <Badge variant="secondary">{t("profile.picker.readOnly")}</Badge>}
      <Button variant="ghost" size="icon-sm" onClick={() => setPreviewOpen(true)} aria-label={t("prompt.open")}>
        <IconFileDescription />
      </Button>
      <PromptPreviewDialog convID={conv.id} open={previewOpen} onOpenChange={setPreviewOpen} />
    </div>
  )
}

/** What the model gets at the conversation's next run. */
export function PromptPreviewDialog({
  convID,
  open,
  onOpenChange,
}: {
  convID: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const [preview, setPreview] = React.useState<PromptPreview | null>(null)
  const [error, setError] = React.useState<AppError | null>(null)

  React.useEffect(() => {
    if (!open) return
    let live = true
    setPreview(null)
    setError(null)
    backend.promptPreview(convID).then(
      (p) => live && setPreview(p),
      (err) => live && setError(appError(err)),
    )
    return () => {
      live = false
    }
  }, [open, convID])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("prompt.title")}</DialogTitle>
          <DialogDescription>{t("prompt.body")}</DialogDescription>
        </DialogHeader>
        {error && (
          <p role="alert" className="text-destructive text-sm">
            {error.message}
          </p>
        )}
        {!preview && !error && <Skeleton className="h-40 w-full" />}
        {preview && (
          <div className="flex min-h-0 flex-col gap-3">
            <div className="flex flex-wrap gap-2 text-xs">
              <Badge variant="outline">{preview.mode === "ask" ? t("chat.mode.ask") : t("chat.mode.read")}</Badge>
              <Badge variant="outline">{t("prompt.steps", { count: preview.stepLimit })}</Badge>
              <Badge variant="outline">{preview.profileName || t("profile.picker.none")}</Badge>
              {preview.localOnly && <Badge variant="secondary">{t("siteSettings.localOnly")}</Badge>}
            </div>
            {preview.siteContextPending && <p className="text-muted-foreground text-xs">{t("prompt.contextPending")}</p>}
            <div className="flex flex-col gap-1">
              <h3 className="text-xs font-medium">{t("prompt.tools", { count: preview.tools.length })}</h3>
              <p className="text-muted-foreground font-mono text-xs break-words">{preview.tools.join(", ")}</p>
            </div>
            <div className="flex min-h-0 flex-col gap-1">
              <h3 className="text-xs font-medium">{t("prompt.system")}</h3>
              <pre className="bg-muted max-h-80 overflow-auto rounded-lg p-3 text-xs whitespace-pre-wrap">{preview.system}</pre>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
