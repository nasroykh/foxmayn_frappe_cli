import { IconTrash } from "@tabler/icons-react"
import * as React from "react"

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
              Cancel
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
  const { reloadSites, reloadAssistants } = useApp()
  return (
    <OneFieldDialog
      site={site}
      onClose={onClose}
      title="Rename site"
      description="Assistants set to use this site by name stop reaching it until you connect them again."
      label="New name"
      hint="Letters, numbers, dots, dashes and underscores."
      initial={(s) => s.name}
      submitLabel="Rename"
      submit={async (s, name) => {
        await backend.rename(s.name, name)
        await Promise.all([reloadSites(), reloadAssistants()])
        return `Renamed to ${name}`
      }}
    />
  )
}

export function SiteURLDialog({ site, onClose }: { site: Site | null; onClose: () => void }) {
  const { reloadSites } = useApp()
  return (
    <OneFieldDialog
      site={site}
      onClose={onClose}
      title="Change address"
      description="Use this when the site moved to a new address. The saved sign-in stays the same."
      label="Site address"
      hint="For example erp.example.com or https://erp.example.com."
      initial={(s) => s.url}
      submitLabel="Save address"
      submit={async (s, url) => {
        const saved = await backend.changeURL(s.name, url)
        await reloadSites()
        return `${s.name} now uses ${saved}`
      }}
    />
  )
}

export function RemoveSiteDialog({ site, onClose }: { site: Site | null; onClose: () => void }) {
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
            ? "Its sign-in was revoked on the site."
            : "The sign-in could not be revoked on the site; it expires on its own.",
        )
      }
      if (res.newDefault) parts.push(`${res.newDefault} is now the default site.`)
      toast.add({ title: `Removed ${kept.name}`, description: parts.join(" ") || undefined, type: "success" })
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
          <AlertDialogTitle>Remove {kept?.name}?</AlertDialogTitle>
          <AlertDialogDescription>
            {kept?.auth === "oauth"
              ? "The app signs out of the site first, so the saved sign-in stops working everywhere. "
              : "The saved sign-in details are deleted from this computer. "}
            Assistants using this site lose access to it. Nothing on the site itself is deleted.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>Cancel</AlertDialogCancel>
          <Button variant="destructive" onClick={remove} disabled={busy}>
            {busy && <Spinner data-icon="inline-start" />}
            Remove site
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
