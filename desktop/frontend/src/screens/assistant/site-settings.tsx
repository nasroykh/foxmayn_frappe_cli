// Settings > Assistant > Sites: per-site instructions and the local-only switch.
import { IconAlertTriangle, IconWorld } from "@tabler/icons-react"
import * as React from "react"
import { Trans, useTranslation } from "react-i18next"

import { ConfirmDialog } from "@/components/confirm-dialog"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Field, FieldContent, FieldDescription, FieldGroup, FieldLabel, FieldTitle } from "@/components/ui/field"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { toast } from "@/components/ui/toast"
import { useApp } from "@/app/app-context"
import { backend } from "@/lib/backend"
import type { SiteSettings } from "@/lib/backend-types"
import { appError, type AppError } from "@/lib/errors"

export function SiteAssistantSettings() {
  const { t } = useTranslation()
  const { sites } = useApp()
  const list = sites.data?.sites ?? []
  const [site, setSite] = React.useState("")
  const [ss, setSS] = React.useState<SiteSettings | null>(null)
  const [error, setError] = React.useState<AppError | null>(null)
  const [saving, setSaving] = React.useState(false)
  // What is saved now, to tell when the save turns Local models only off.
  const [savedLocalOnly, setSavedLocalOnly] = React.useState(false)
  const [confirmOnline, setConfirmOnline] = React.useState(false)

  const chosen = site || sites.data?.defaultSite || list[0]?.name || ""

  React.useEffect(() => {
    if (!chosen) return
    let live = true
    setSS(null)
    setError(null)
    backend.getSiteSettings(chosen).then(
      (s) => {
        if (!live) return
        setSS(s)
        setSavedLocalOnly(s.localOnly)
      },
      (err) => live && setError(appError(err)),
    )
    return () => {
      live = false
    }
  }, [chosen])

  function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!ss) return
    // Letting online models read the site is asked first.
    if (savedLocalOnly && !ss.localOnly) setConfirmOnline(true)
    else void save()
  }

  async function save() {
    if (!ss) return
    setSaving(true)
    setError(null)
    try {
      const saved = await backend.saveSiteSettings(ss)
      setSS(saved)
      setSavedLocalOnly(saved.localOnly)
      toast.add({ title: t("siteSettings.saved"), type: "success" })
    } catch (err) {
      setError(appError(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <section aria-labelledby="site-settings-title" className="flex max-w-2xl flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 id="site-settings-title" className="font-heading text-base font-semibold">
          {t("siteSettings.title")}
        </h2>
        <p className="text-muted-foreground text-sm">{t("siteSettings.intro")}</p>
      </div>
      {list.length === 0 ? (
        <p className="text-muted-foreground text-sm">{t("siteSettings.noSites")}</p>
      ) : (
        <form onSubmit={onSubmit} className="flex flex-col gap-4 rounded-xl border p-4">
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="site-settings-site">{t("siteSettings.site")}</FieldLabel>
              <NativeSelect id="site-settings-site" value={chosen} onChange={(e) => setSite(e.target.value)}>
                {list.map((s) => (
                  <NativeSelectOption key={s.name} value={s.name}>
                    {s.name}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
            {!ss && !error && <Skeleton className="h-24 w-full" />}
            {ss && (
              <>
                <Field>
                  <FieldLabel htmlFor="site-settings-instructions">{t("siteSettings.instructions")}</FieldLabel>
                  <Textarea
                    id="site-settings-instructions"
                    rows={4}
                    maxLength={4000}
                    value={ss.instructions}
                    onChange={(e) => setSS({ ...ss, instructions: e.target.value })}
                  />
                  <FieldDescription>{t("siteSettings.instructionsHelp")}</FieldDescription>
                </Field>
                <Field orientation="horizontal">
                  <FieldContent>
                    <FieldTitle>{t("siteSettings.localOnly")}</FieldTitle>
                    <FieldDescription>{t("siteSettings.localOnlyHelp")}</FieldDescription>
                    {ss.localOnlyFrom && (
                      <FieldDescription>{t("siteSettings.localOnlyFrom", { site: ss.localOnlyFrom })}</FieldDescription>
                    )}
                  </FieldContent>
                  <Switch
                    checked={ss.localOnly}
                    onCheckedChange={(v) => setSS({ ...ss, localOnly: v })}
                    aria-label={t("siteSettings.localOnly")}
                  />
                </Field>
              </>
            )}
          </FieldGroup>
          {error && (
            <Alert variant="destructive" role="alert">
              <IconAlertTriangle />
              <AlertDescription>{error.message}</AlertDescription>
            </Alert>
          )}
          <div className="flex justify-end">
            <Button type="submit" disabled={!ss || saving}>
              {t("common.save")}
            </Button>
          </div>
        </form>
      )}
      <ConfirmDialog
        open={confirmOnline}
        onOpenChange={setConfirmOnline}
        icon={<IconWorld />}
        title={t("siteSettings.confirmOnline.title")}
        description={
          <Trans i18nKey="siteSettings.confirmOnline.body" values={{ site: ss?.site }} components={{ site: <bdi /> }} />
        }
        confirmLabel={t("siteSettings.confirmOnline.action")}
        onConfirm={() => {
          setConfirmOnline(false)
          void save()
        }}
      />
    </section>
  )
}
