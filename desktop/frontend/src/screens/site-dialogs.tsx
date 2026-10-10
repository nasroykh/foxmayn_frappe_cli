import { IconTrash } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { useApp } from "@/app/app-context"
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { toast } from "@/components/ui/toast"
import { backend } from "@/lib/backend"
import type { Site } from "@/lib/backend-types"
import { appError, errorTitle } from "@/lib/errors"

/**
 * A one-field dialog (rename, change address). It keeps the last site while
 * closing so the content does not vanish during the close animation.
 */
function useKept(site: Site | null) {
  const [kept, setKept] = React.useState<Site | null>(site)
  React.useEffect(() => {
    if (site) setKept(site)
  }, [site])
  return kept
}

function OneFieldDialog({
  site,
  onClose,
  title,
  description,
  label,
  hint,
  initial,
  submitLabel,
  submit,
}: {
  site: Site | null
  onClose: () => void
  title: string
  description: React.ReactNode
  label: string
  hint: string
  initial: (s: Site) => string
  submitLabel: string
  submit: (s: Site, value: string) => Promise<string>
}) {
  const { t } = useTranslation()
  const kept = useKept(site)
  const [value, setValue] = React.useState("")
  const [error, setError] = React.useState("")
  const [busy, setBusy] = React.useState(false)

  React.useEffect(() => {
    if (site) {
      setValue(initial(site))
      setError("")
    }
    // initial is a stable formatter per dialog.
  }, [site])

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!kept) return
    setBusy(true)
    setError("")
    try {
      const msg = await submit(kept, value.trim())
      toast.add({ title: msg, type: "success" })
      onClose()
    } catch (err) {
      const ae = appError(err)
      if (ae.code === "invalid" || ae.code === "exists") setError(ae.message)
      else toast.add({ title: errorTitle(ae), description: ae.message, type: "error" })
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={!!site} onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={onSubmit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription>{description}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field data-invalid={!!error || undefined}>
              <FieldLabel htmlFor="one-field">{label}</FieldLabel>
              <Input
                id="one-field"
                value={value}
                onChange={(e) => setValue(e.target.value)}
                aria-invalid={!!error || undefined}
                autoComplete="off"
                spellCheck={false}
                autoFocus
              />
              {error ? <FieldError>{error}</FieldError> : <FieldDescription>{hint}</FieldDescription>}
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose} disabled={busy}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || !value.trim() || (kept ? value.trim() === initial(kept) : true)}>
              {busy && <Spinner data-icon="inline-start" />}
              {submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function RenameSiteDialog({ site, onClose }: { site: Site | null; onClose: () => void }) {
  const { t } = useTranslation()
  const { reloadSites, reloadAssistants } = useApp()
  return (
    <OneFieldDialog
      site={site}
      onClose={onClose}
      title={t("siteDialogs.rename.title")}
      description={t("siteDialogs.rename.description")}
      label={t("siteDialogs.rename.label")}
      hint={t("siteDialogs.rename.hint")}
      initial={(s) => s.name}
      submitLabel={t("siteDialogs.rename.submit")}
      submit={async (s, name) => {
        await backend.rename(s.name, name)
        await Promise.all([reloadSites(), reloadAssistants()])
        return t("siteDialogs.rename.done", { name })
      }}
    />
  )
}

export function SiteURLDialog({ site, onClose }: { site: Site | null; onClose: () => void }) {
  const { t } = useTranslation()
  const { reloadSites } = useApp()
  return (
    <OneFieldDialog
      site={site}
      onClose={onClose}
      title={t("siteDialogs.url.title")}
      description={t("siteDialogs.url.description")}
      label={t("siteDialogs.url.label")}
      hint={t("siteDialogs.url.hint")}
      initial={(s) => s.url}
      submitLabel={t("siteDialogs.url.submit")}
      submit={async (s, url) => {
        const saved = await backend.changeURL(s.name, url)
        await reloadSites()
        return t("siteDialogs.url.done", { name: s.name, url: saved })
      }}
    />
  )
}

export function RemoveSiteDialog({ site, onClose }: { site: Site | null; onClose: () => void }) {
  const { t } = useTranslation()
  const { reloadSites, reloadAssistants } = useApp()
  const kept = useKept(site)
  const [busy, setBusy] = React.useState(false)

  async function remove() {
    if (!kept) return
    setBusy(true)
    try {
      const res = await backend.remove(kept.name)
      await Promise.all([reloadSites(), reloadAssistants()])
      const parts: string[] = []
      if (kept.auth === "oauth") {
        parts.push(
          res.revoked
            ? t("siteDialogs.remove.revoked")
            : t("siteDialogs.remove.notRevoked"),
        )
      }
      if (res.newDefault) parts.push(t("siteDialogs.remove.newDefault", { name: res.newDefault }))
      toast.add({ title: t("siteDialogs.remove.done", { name: kept.name }), description: parts.join(" ") || undefined, type: "success" })
      onClose()
    } catch (err) {
      const e = appError(err)
      toast.add({ title: errorTitle(e), description: e.message, type: "error" })
    } finally {
      setBusy(false)
    }
  }

  return (
    <AlertDialog open={!!site} onOpenChange={(o) => !o && !busy && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogMedia>
            <IconTrash />
          </AlertDialogMedia>
          <AlertDialogTitle>{t("siteDialogs.remove.title", { name: kept?.name })}</AlertDialogTitle>
          <AlertDialogDescription>
            {kept?.auth === "oauth" ? t("siteDialogs.remove.bodyOAuth") : t("siteDialogs.remove.bodyLocal")}

          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>{t("common.cancel")}</AlertDialogCancel>
          <Button variant="destructive" onClick={remove} disabled={busy}>
            {busy && <Spinner data-icon="inline-start" />}
            {t("siteDialogs.remove.submit")}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
