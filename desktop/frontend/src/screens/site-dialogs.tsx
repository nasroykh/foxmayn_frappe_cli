import { IconTrash } from "@tabler/icons-react"
import * as React from "react"
import { Trans, useTranslation } from "react-i18next"

import { useApp } from "@/app/app-context"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { SecretInput } from "@/components/secret-input"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { toast } from "@/components/ui/toast"
import { backend } from "@/lib/backend"
import type { NewSignIn, Site } from "@/lib/backend-types"
import { appError, errorTitle } from "@/lib/errors"
import { hasRules } from "@/screens/safety-sheet"

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

const noSign: NewSignIn = { apiKey: "", apiSecret: "", username: "", password: "" }

/**
 * Change address. On the same server the saved sign-in is kept and nothing
 * else is asked. On another server it is never sent: the person types the
 * sign-in again (the fields appear as the address is edited, or when Go
 * answers site.newServer).
 */
export function SiteURLDialog({ site, onClose }: { site: Site | null; onClose: () => void }) {
  const { t } = useTranslation()
  const { reloadSites } = useApp()
  const kept = useKept(site)
  const [value, setValue] = React.useState("")
  const [sign, setSign] = React.useState<NewSignIn>(noSign)
  const [needs, setNeeds] = React.useState(false)
  const [error, setError] = React.useState("")
  const [busy, setBusy] = React.useState(false)

  React.useEffect(() => {
    // A closed dialog keeps no secret.
    setSign(noSign)
    setNeeds(false)
    setError("")
    if (site) setValue(site.url)
  }, [site])

  const url = value.trim()
  const changed = !!kept && !!url && url !== kept.url
  const oauth = kept?.auth === "oauth"
  const session = kept?.auth === "password"

  // Is the new address on another server? Asked once the typing pauses.
  React.useEffect(() => {
    if (!site || oauth || !changed) {
      setNeeds(false)
      return
    }
    let stale = false
    const timer = setTimeout(() => {
      backend.needsSignIn(site.name, url).then(
        (n) => !stale && setNeeds(n),
        () => !stale && setNeeds(false), // an invalid address is reported by the save
      )
    }, 350)
    return () => {
      stale = true
      clearTimeout(timer)
    }
  }, [site, url, changed, oauth])

  const signComplete = session ? !!sign.username.trim() && !!sign.password : !!sign.apiKey.trim() && !!sign.apiSecret

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!kept || !changed) return
    setBusy(true)
    setError("")
    try {
      const saved = await backend.changeURL(kept.name, url, needs ? sign : noSign)
      await reloadSites()
      toast.add({ title: t("siteDialogs.url.done", { name: kept.name, url: saved }), type: "success" })
      onClose()
    } catch (err) {
      const ae = appError(err)
      if (ae.key === "site.newServer") setNeeds(true)
      else if (ae.code === "invalid" || ae.code === "exists") setError(ae.message)
      else toast.add({ title: errorTitle(ae), description: ae.message, type: "error" })
    } finally {
      setBusy(false)
    }
  }

  const set = (patch: Partial<NewSignIn>) => setSign((x) => ({ ...x, ...patch }))

  return (
    <Dialog open={!!site} onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={onSubmit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("siteDialogs.url.title")}</DialogTitle>
            <DialogDescription>{t("siteDialogs.url.description")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field data-invalid={!!error || undefined}>
              <FieldLabel htmlFor="one-field">{t("siteDialogs.url.label")}</FieldLabel>
              <Input
                id="one-field"
                value={value}
                onChange={(e) => setValue(e.target.value)}
                aria-invalid={!!error || undefined}
                autoComplete="off"
                spellCheck={false}
                autoFocus
              />
              {error ? <FieldError>{error}</FieldError> : <FieldDescription>{t("siteDialogs.url.hint")}</FieldDescription>}
            </Field>
            {needs && (
              <FieldSet data-testid="url-sign-in">
                <FieldLegend variant="label">{t("siteDialogs.url.newServer.title")}</FieldLegend>
                <FieldDescription>{t("siteDialogs.url.newServer.body")}</FieldDescription>
                {session ? (
                  <FieldGroup>
                    <Field>
                      <FieldLabel htmlFor="url-username">{t("addSite.password.username")}</FieldLabel>
                      <Input
                        id="url-username"
                        value={sign.username}
                        onChange={(e) => set({ username: e.target.value })}
                        autoComplete="off"
                        spellCheck={false}
                      />
                    </Field>
                    <Field>
                      <FieldLabel htmlFor="url-password">{t("addSite.password.password")}</FieldLabel>
                      <SecretInput id="url-password" value={sign.password} onChange={(v) => set({ password: v })} />
                    </Field>
                  </FieldGroup>
                ) : (
                  <FieldGroup>
                    <Field>
                      <FieldLabel htmlFor="url-api-key">{t("addSite.apikey.key")}</FieldLabel>
                      <Input
                        id="url-api-key"
                        value={sign.apiKey}
                        onChange={(e) => set({ apiKey: e.target.value })}
                        autoComplete="off"
                        spellCheck={false}
                        className="font-mono"
                      />
                    </Field>
                    <Field>
                      <FieldLabel htmlFor="url-api-secret">{t("addSite.apikey.secret")}</FieldLabel>
                      <SecretInput id="url-api-secret" value={sign.apiSecret} onChange={(v) => set({ apiSecret: v })} />
                    </Field>
                  </FieldGroup>
                )}
              </FieldSet>
            )}
          </FieldGroup>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose} disabled={busy}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || !changed || (needs && !signComplete)}>
              {busy && <Spinner data-icon="inline-start" />}
              {t("siteDialogs.url.submit")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function RemoveSiteDialog({ site, onClose }: { site: Site | null; onClose: () => void }) {
  const { t } = useTranslation()
  const { reloadSites, reloadAssistants } = useApp()
  const kept = useKept(site)
  const [busy, setBusy] = React.useState(false)
  // Whether the site has safety rules, which go with it ("unknown" when they could not be read).
  const [rules, setRules] = React.useState<"yes" | "no" | "unknown">("no")

  React.useEffect(() => {
    if (!site) return
    let live = true
    setRules("no")
    backend.getPolicy(site.name).then(
      (p) => live && setRules(hasRules(p) ? "yes" : "no"),
      () => live && setRules("unknown"),
    )
    return () => {
      live = false
    }
  }, [site])

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

  const body = kept?.auth === "oauth" ? t("siteDialogs.remove.bodyOAuth") : t("siteDialogs.remove.bodyLocal")
  return (
    <ConfirmDialog
      open={!!site}
      onOpenChange={(o) => !o && onClose()}
      icon={<IconTrash />}
      title={<Trans i18nKey="siteDialogs.remove.title" values={{ name: kept?.name }} components={{ site: <bdi /> }} />}
      description={
        <>
          {body}
          {rules !== "no" && (
            <>
              {" "}
              {rules === "yes" ? t("siteDialogs.remove.rules") : t("siteDialogs.remove.rulesMaybe")}
            </>
          )}
        </>
      }
      confirmLabel={t("siteDialogs.remove.submit")}
      onConfirm={() => void remove()}
      busy={busy}
    />
  )
}
