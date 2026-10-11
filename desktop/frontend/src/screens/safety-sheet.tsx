// The Safety rules sheet of a site: its ffc policy (what the Assistant and
// every connected app may do there). Most people pick one of two cards; the
// lists sit under Custom. A save that opens access is confirmed by Go in a
// native dialog, so this screen only warns in advance.
import { IconAlertTriangle, IconEye, IconInfoCircle, IconListCheck, IconShieldCheck } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Field, FieldContent, FieldDescription, FieldGroup, FieldLabel, FieldLegend, FieldSet, FieldTitle } from "@/components/ui/field"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { toast } from "@/components/ui/toast"
import { backend } from "@/lib/backend"
import type { PolicyChange, PolicyConfirm, Site, SitePolicy } from "@/lib/backend-types"
import { appError, errorTitle, type AppError } from "@/lib/errors"

type Choice = "read" | "ask" | "custom"

const KINDS = [
  "readOnlyOff",
  "confirmLooser",
  "allowToolsAdded",
  "allowToolsLifted",
  "allowDoctypesAdded",
  "allowDoctypesLifted",
  "denyDoctypesRemoved",
  "allowMethodsAdded",
  "allowMethodsLifted",
  "denyMethodsRemoved",
  "sensitiveDoctypesUnlocked",
  "deniedMethodsUnlocked",
  "serverMethodsEnabled",
]

type ListKey = "allowDoctypes" | "denyDoctypes" | "allowTools" | "allowMethods" | "denyMethods"
const LIST_KEYS: ListKey[] = ["allowDoctypes", "denyDoctypes", "allowTools", "allowMethods", "denyMethods"]

const splitList = (s: string) =>
  s
    .split(/[\n,]/)
    .map((x) => x.trim())
    .filter(Boolean)

const noLists = (p: SitePolicy) => LIST_KEYS.every((k) => p[k].length === 0)

/** A policy that is exactly one of the two cards opens as it; anything else as Custom. */
export function choiceOf(p: SitePolicy): Choice {
  if (p.confirm === "" && noLists(p)) return p.readOnly ? "read" : "ask"
  return "custom"
}

interface Draft {
  choice: Choice
  readOnly: boolean
  confirm: PolicyConfirm
  lists: Record<ListKey, string>
}

function draftOf(p: SitePolicy): Draft {
  return {
    choice: choiceOf(p),
    readOnly: p.readOnly,
    confirm: p.confirm,
    lists: {
      allowDoctypes: p.allowDoctypes.join(", "),
      denyDoctypes: p.denyDoctypes.join(", "),
      allowTools: p.allowTools.join(", "),
      allowMethods: p.allowMethods.join("\n"),
      denyMethods: p.denyMethods.join("\n"),
    },
  }
}

/**
 * The policy to save. The two cards set only read-only and when to ask; the
 * lists are kept as they are, so tightening a site to Read only never drops
 * a refusal (which would open access).
 */
function policyOf(d: Draft): SitePolicy {
  const readOnly = d.choice === "read" ? true : d.choice === "ask" ? false : d.readOnly
  const confirm = d.choice === "custom" ? d.confirm : ""
  return {
    readOnly,
    confirm,
    allowTools: splitList(d.lists.allowTools),
    allowDoctypes: splitList(d.lists.allowDoctypes),
    denyDoctypes: splitList(d.lists.denyDoctypes),
    allowMethods: splitList(d.lists.allowMethods),
    denyMethods: splitList(d.lists.denyMethods),
  }
}

/** Keeps the last site while the sheet closes, so its content does not vanish mid-animation. */
export function SafetySheet({ site, onClose }: { site: Site | null; onClose: () => void }) {
  const [kept, setKept] = React.useState<Site | null>(site)
  React.useEffect(() => {
    if (site) setKept(site)
  }, [site])
  return (
    <Sheet open={site !== null} onOpenChange={(o) => !o && onClose()}>
      <SheetContent className="w-full gap-0 data-[side=right]:sm:max-w-md">
        {kept && <SafetyBody key={kept.name} site={kept} onClose={onClose} />}
      </SheetContent>
    </Sheet>
  )
}

function ChoiceCard({ id, value, icon, title, description }: { id: string; value: Choice; icon: React.ReactNode; title: string; description: string }) {
  return (
    <FieldLabel htmlFor={id}>
      <Field orientation="horizontal">
        <span className="text-muted-foreground [&_svg:not([class*='size-'])]:size-5">{icon}</span>
        <FieldContent>
          <FieldTitle>{title}</FieldTitle>
          <FieldDescription>{description}</FieldDescription>
        </FieldContent>
        <RadioGroupItem value={value} id={id} />
      </Field>
    </FieldLabel>
  )
}

// i18n keys: safety.opens.readOnlyOff safety.opens.confirmLooser safety.opens.allowToolsAdded safety.opens.allowToolsLifted safety.opens.allowDoctypesAdded safety.opens.allowDoctypesLifted safety.opens.denyDoctypesRemoved safety.opens.allowMethodsAdded safety.opens.allowMethodsLifted safety.opens.denyMethodsRemoved safety.opens.sensitiveDoctypesUnlocked safety.opens.deniedMethodsUnlocked safety.opens.serverMethodsEnabled safety.opens.generic
function OpenLine({ change }: { change: PolicyChange }) {
  const { t } = useTranslation()
  const key = KINDS.includes(change.kind) ? change.kind : "generic"
  // Confirm changes carry the old and new mode, not entries to list.
  const values = change.kind === "confirmLooser" ? [] : change.values
  return (
    <li>
      {t(`safety.opens.${key}`)}
      {values.length > 0 && (
        <>
          {" "}
          <span data-site-data dir="auto">
            {values.join(", ")}
          </span>
        </>
      )}
    </li>
  )
}

function SafetyBody({ site, onClose }: { site: Site; onClose: () => void }) {
  const { t } = useTranslation()
  const [base, setBase] = React.useState<SitePolicy | null>(null)
  const [draft, setDraft] = React.useState<Draft | null>(null)
  const [loadError, setLoadError] = React.useState<AppError | null>(null)
  const [saving, setSaving] = React.useState(false)
  const [opens, setOpens] = React.useState<PolicyChange[]>([])
  const [error, setError] = React.useState<AppError | null>(null)
  const [note, setNote] = React.useState<"declined" | "reloaded" | null>(null)

  const load = React.useCallback(async () => {
    setLoadError(null)
    try {
      const p = await backend.getPolicy(site.name)
      setBase(p)
      setDraft(draftOf(p))
    } catch (err) {
      setLoadError(appError(err))
    }
  }, [site.name])
  React.useEffect(() => {
    void load()
  }, [load])

  const set = (patch: Partial<Draft>) => setDraft((d) => (d ? { ...d, ...patch } : d))

  async function save(e: React.FormEvent) {
    e.preventDefault()
    if (!base || !draft) return
    setSaving(true)
    setError(null)
    setNote(null)
    setOpens([])
    try {
      const next = policyOf(draft)
      const pv = await backend.previewPolicy(site.name, next)
      // The native confirmation comes from Go on save; say so beforehand.
      setOpens(pv.opens)
      await backend.savePolicy(site.name, base, next)
      toast.add({ title: t("safety.saved"), type: "success" })
      onClose()
    } catch (err) {
      const ae = appError(err)
      if (ae.key === "policy.declined") {
        setNote("declined")
      } else if (ae.key === "policy.changed") {
        setOpens([])
        setError(ae)
        await load()
        setNote("reloaded")
      } else {
        setError(ae)
      }
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <SheetHeader className="border-b">
        <SheetTitle>{t("safety.title", { site: site.name })}</SheetTitle>
        <SheetDescription>{t("safety.description")}</SheetDescription>
      </SheetHeader>

      <form id="safety-form" onSubmit={(e) => void save(e)} className="flex-1 overflow-y-auto p-4">
        {loadError ? (
          <Alert variant="destructive" role="alert">
            <IconAlertTriangle />
            <AlertTitle>{t("safety.loadFailed")}</AlertTitle>
            <AlertDescription>{loadError.message}</AlertDescription>
          </Alert>
        ) : !draft ? (
          <div className="flex flex-col gap-3" aria-busy="true" aria-label={t("common.loading")}>
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
          </div>
        ) : (
          <FieldGroup>
            <FieldSet>
              <FieldLegend variant="label">{t("safety.choice")}</FieldLegend>
              <RadioGroup value={draft.choice} onValueChange={(v) => set({ choice: v as Choice })}>
                <ChoiceCard
                  id="safety-read"
                  value="read"
                  icon={<IconEye />}
                  title={t("safety.read.title")}
                  description={t("safety.read.description")}
                />
                <ChoiceCard
                  id="safety-ask"
                  value="ask"
                  icon={<IconShieldCheck />}
                  title={t("safety.ask.title")}
                  description={t("safety.ask.description")}
                />
                <ChoiceCard
                  id="safety-custom"
                  value="custom"
                  icon={<IconListCheck />}
                  title={t("safety.custom.title")}
                  description={t("safety.custom.description")}
                />
              </RadioGroup>
            </FieldSet>

            {draft.choice === "custom" && (
              <FieldSet data-testid="safety-advanced">
                <FieldLegend variant="label">{t("safety.advanced")}</FieldLegend>
                <FieldGroup>
                  <Field orientation="horizontal">
                    <FieldContent>
                      <FieldTitle>{t("safety.readOnly.title")}</FieldTitle>
                      <FieldDescription>{t("safety.readOnly.hint")}</FieldDescription>
                    </FieldContent>
                    <Switch
                      checked={draft.readOnly}
                      onCheckedChange={(v) => set({ readOnly: v })}
                      aria-label={t("safety.readOnly.title")}
                    />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="safety-confirm">{t("safety.confirm.label")}</FieldLabel>
                    <NativeSelect
                      id="safety-confirm"
                      className="w-full"
                      value={draft.confirm}
                      onChange={(e) => set({ confirm: e.target.value as PolicyConfirm })}
                    >
                      <NativeSelectOption value="always">{t("safety.confirm.always")}</NativeSelectOption>
                      <NativeSelectOption value="">{t("safety.confirm.default")}</NativeSelectOption>
                      <NativeSelectOption value="never">{t("safety.confirm.never")}</NativeSelectOption>
                    </NativeSelect>
                  </Field>
                  {LIST_KEYS.map((k) => (
                    <Field key={k}>
                      <FieldLabel htmlFor={`safety-${k}`}>{t(`safety.list.${k}`)}</FieldLabel>
                      <Textarea
                        id={`safety-${k}`}
                        rows={2}
                        spellCheck={false}
                        data-site-data
                        value={draft.lists[k]}
                        onChange={(e) => set({ lists: { ...draft.lists, [k]: e.target.value } })}
                      />
                      <FieldDescription>{t("safety.list.hint")}</FieldDescription>
                    </Field>
                  ))}
                </FieldGroup>
              </FieldSet>
            )}

            {opens.length > 0 && (
              <Alert data-testid="safety-opens">
                <IconInfoCircle />
                <AlertTitle>{t("safety.opensTitle")}</AlertTitle>
                <AlertDescription>
                  <ul className="list-disc ps-4">
                    {opens.map((c, i) => (
                      <OpenLine key={i} change={c} />
                    ))}
                  </ul>
                  <p className="mt-1">{t("safety.willConfirm")}</p>
                </AlertDescription>
              </Alert>
            )}
            {note === "declined" && (
              <Alert role="status">
                <IconInfoCircle />
                <AlertDescription>{t("safety.declined")}</AlertDescription>
              </Alert>
            )}
            {note === "reloaded" && (
              <Alert role="status">
                <IconInfoCircle />
                <AlertDescription>{t("safety.reloaded")}</AlertDescription>
              </Alert>
            )}
            {error && (
              <Alert variant="destructive" role="alert">
                <IconAlertTriangle />
                <AlertTitle>{errorTitle(error)}</AlertTitle>
                <AlertDescription>{error.message}</AlertDescription>
              </Alert>
            )}
          </FieldGroup>
        )}
      </form>

      <SheetFooter className="border-t sm:flex-row sm:justify-between">
        <Button variant="outline" onClick={onClose}>
          {t("common.cancel")}
        </Button>
        <Button type="submit" form="safety-form" disabled={saving || !draft}>
          {saving && <Spinner data-icon="inline-start" />}
          {t("common.save")}
        </Button>
      </SheetFooter>
    </>
  )
}
