// Settings > Assistant > Profiles: the presets (duplicate to edit) and the
// user's own profiles. A profile only narrows what a site allows.
import {
  IconAlertTriangle,
  IconChevronDown,
  IconCopy,
  IconEye,
  IconPencil,
  IconPencilCheck,
  IconPlus,
  IconTrash,
} from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
  FieldTitle,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { toast } from "@/components/ui/toast"
import { backend } from "@/lib/backend"
import type { ConversationMode, Profile, Toolset } from "@/lib/backend-types"
import { appError, errorTitle, type AppError } from "@/lib/errors"
import { profileName, useProfiles } from "@/screens/assistant/profile-picker"

export const TOOLSETS: Toolset[] = ["core", "lifecycle", "collab", "admin", "files", "erp"]

/** A new profile: read only, the default tool sets and step limit. */
export function blankProfile(): Profile {
  return {
    id: "",
    name: "",
    preset: false,
    basedOn: "",
    mode: "read",
    toolsets: [],
    allowTools: [],
    allowDoctypes: [],
    denyDoctypes: [],
    allowMethods: [],
    denyMethods: [],
    denyTools: [],
    callMethod: false,
    stepLimit: 25,
    providerID: "",
    model: "",
    instructions: "",
    keepHistory: true,
  }
}

/** A user copy of a preset (or of another profile), ready to edit. */
export function duplicate(p: Profile, name: string): Profile {
  return { ...p, id: "", preset: false, basedOn: p.preset ? p.id : p.basedOn, name }
}

const splitList = (s: string) =>
  s
    .split(/[\n,]/)
    .map((x) => x.trim())
    .filter(Boolean)

type Editing = { p: Profile; title: string }

export function ProfileSettings() {
  const { t } = useTranslation()
  const { presets, own, error, reload } = useProfiles()
  const [editing, setEditing] = React.useState<Editing | null>(null)
  const [doomed, setDoomed] = React.useState<Profile | null>(null)

  async function remove() {
    if (!doomed) return
    try {
      await backend.deleteProfile(doomed.id)
      await reload()
    } catch (err) {
      const e = appError(err)
      toast.add({ title: errorTitle(e), description: e.message, type: "error" })
    } finally {
      setDoomed(null)
    }
  }

  const row = (p: Profile) => (
    <li key={p.id} className="flex items-start justify-between gap-2 rounded-xl border p-3">
      <div className="flex min-w-0 flex-col gap-0.5">
        <p className="flex items-center gap-2 text-sm font-medium">
          <span className="truncate">{profileName(t, p)}</span>
          {p.preset && <Badge variant="secondary">{t("profile.builtIn")}</Badge>}
          <Badge variant="outline">{p.mode === "ask" ? t("chat.mode.ask") : t("chat.mode.read")}</Badge>
        </p>
        <p className="text-muted-foreground text-xs">
          {t("profile.summary", {
            toolsets: shownToolsets(p)
              .map((ts) => t(`profile.toolset.${ts}.title`))
              .join(", "),
            count: p.stepLimit,
          })}
        </p>
      </div>
      <div className="flex shrink-0 gap-1">
        <Button
          variant="ghost"
          size="sm"
          onClick={() => setEditing({ p: duplicate(p, t("profile.copyName", { name: profileName(t, p) })), title: t("profile.duplicateTitle") })}
          aria-label={t("profile.duplicateLabel", { name: profileName(t, p) })}
        >
          <IconCopy data-icon="inline-start" />
          {t("profile.duplicate")}
        </Button>
        {!p.preset && (
          <>
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => setEditing({ p, title: t("profile.editTitle") })}
              aria-label={t("profile.editLabel", { name: p.name })}
            >
              <IconPencil />
            </Button>
            <Button variant="ghost" size="icon-sm" onClick={() => setDoomed(p)} aria-label={t("profile.deleteLabel", { name: p.name })}>
              <IconTrash />
            </Button>
          </>
        )}
      </div>
    </li>
  )

  return (
    <section aria-labelledby="profiles-title" className="flex max-w-2xl flex-col gap-4">
      <div className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-1">
          <h2 id="profiles-title" className="font-heading text-base font-semibold">
            {t("profile.title")}
          </h2>
          <p className="text-muted-foreground text-sm">{t("profile.intro")}</p>
        </div>
        <Button size="sm" onClick={() => setEditing({ p: blankProfile(), title: t("profile.newTitle") })}>
          <IconPlus data-icon="inline-start" />
          {t("profile.new")}
        </Button>
      </div>
      {error && (
        <Alert variant="destructive" role="alert">
          <IconAlertTriangle />
          <AlertDescription>{error.message}</AlertDescription>
        </Alert>
      )}
      {!presets && !error && <Skeleton className="h-16 w-full rounded-xl" />}
      <ul className="flex flex-col gap-2" aria-label={t("profile.picker.presets")}>
        {presets?.map(row)}
      </ul>
      {own && own.length > 0 && (
        <ul className="flex flex-col gap-2" aria-label={t("profile.picker.own")}>
          {own.map(row)}
        </ul>
      )}

      <Dialog open={!!editing} onOpenChange={(o) => !o && setEditing(null)}>
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{editing?.title}</DialogTitle>
            <DialogDescription>{t("profile.editorBody")}</DialogDescription>
          </DialogHeader>
          {editing && (
            <ProfileEditor
              initial={editing.p}
              onSaved={() => {
                setEditing(null)
                void reload()
              }}
            />
          )}
        </DialogContent>
      </Dialog>

      <AlertDialog open={!!doomed} onOpenChange={(o) => !o && setDoomed(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("profile.deleteTitle", { name: doomed?.name ?? "" })}</AlertDialogTitle>
            <AlertDialogDescription>{t("profile.deleteBody")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={() => void remove()}>
              {t("profile.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}

/** Tool sets in the order shown; core is always served. */
// i18n keys: profile.toolset.core.title profile.toolset.core.description profile.toolset.lifecycle.title profile.toolset.lifecycle.description profile.toolset.collab.title profile.toolset.collab.description profile.toolset.admin.title profile.toolset.admin.description profile.toolset.files.title profile.toolset.files.description profile.toolset.erp.title profile.toolset.erp.description
const DEFAULT_TOOLSETS: Toolset[] = ["core", "lifecycle"]

/** The tool sets a profile serves: its own list, or ffc's default; core always. */
export function shownToolsets(p: Profile): Toolset[] {
  const on = p.toolsets.length ? p.toolsets : DEFAULT_TOOLSETS
  return TOOLSETS.filter((ts) => ts === "core" || on.includes(ts))
}

/** Whether a profile sets anything the Advanced section holds. */
function hasAdvanced(p: Profile): boolean {
  return (
    p.allowTools.length + p.denyTools.length + p.allowDoctypes.length + p.denyDoctypes.length + p.allowMethods.length + p.denyMethods.length > 0 ||
    p.callMethod ||
    !p.keepHistory ||
    p.stepLimit !== 25
  )
}

function ChoiceCard({ id, value, icon, title, description }: { id: string; value: string; icon: React.ReactNode; title: string; description: string }) {
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

/**
 * Edits one of the user's profiles; presets are copied first. The form shows
 * what most people set (name, whether it may change things, what it works
 * with, instructions); lists, server methods, history and the step limit sit
 * under Advanced, opened when the profile already sets one of them.
 */
export function ProfileEditor({ initial, onSaved }: { initial: Profile; onSaved: (p: Profile) => void }) {
  const { t } = useTranslation()
  const [p, setP] = React.useState<Profile>(() => ({ ...initial, toolsets: shownToolsets(initial) }))
  const [lists, setLists] = React.useState(() => ({
    allowTools: initial.allowTools.join(", "),
    denyTools: initial.denyTools.join(", "),
    allowDoctypes: initial.allowDoctypes.join(", "),
    denyDoctypes: initial.denyDoctypes.join(", "),
    allowMethods: initial.allowMethods.join("\n"),
    denyMethods: initial.denyMethods.join("\n"),
  }))
  const [steps, setSteps] = React.useState(String(initial.stepLimit))
  const [advanced, setAdvanced] = React.useState(() => hasAdvanced(initial))
  const [saving, setSaving] = React.useState(false)
  const [error, setError] = React.useState<AppError | null>(null)
  const set = <K extends keyof Profile>(k: K, v: Profile[K]) => setP((x) => ({ ...x, [k]: v }))

  async function save(e: React.FormEvent) {
    e.preventDefault()
    setSaving(true)
    setError(null)
    try {
      const n = Number.parseInt(steps, 10)
      const saved = await backend.saveProfile({
        ...p,
        stepLimit: Number.isFinite(n) ? n : 0,
        allowTools: splitList(lists.allowTools),
        denyTools: splitList(lists.denyTools),
        allowDoctypes: splitList(lists.allowDoctypes),
        denyDoctypes: splitList(lists.denyDoctypes),
        allowMethods: splitList(lists.allowMethods),
        denyMethods: splitList(lists.denyMethods),
      })
      onSaved(saved)
    } catch (err) {
      setError(appError(err))
    } finally {
      setSaving(false)
    }
  }

  const list = (key: keyof typeof lists, multiline = false) => (
    <Field>
      <FieldLabel htmlFor={`profile-${key}`}>{t(`profile.field.${key}`)}</FieldLabel>
      {multiline ? (
        <Textarea
          id={`profile-${key}`}
          rows={2}
          value={lists[key]}
          onChange={(e) => setLists((l) => ({ ...l, [key]: e.target.value }))}
        />
      ) : (
        <Input id={`profile-${key}`} value={lists[key]} onChange={(e) => setLists((l) => ({ ...l, [key]: e.target.value }))} />
      )}
    </Field>
  )

  return (
    <form onSubmit={(e) => void save(e)} className="flex flex-col gap-4">
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="profile-name">{t("profile.field.name")}</FieldLabel>
          <Input id="profile-name" value={p.name} onChange={(e) => set("name", e.target.value)} autoFocus />
        </Field>
        <FieldSet>
          <FieldLegend variant="label">{t("chat.mode.label")}</FieldLegend>
          <RadioGroup value={p.mode} onValueChange={(v) => set("mode", v as ConversationMode)}>
            <ChoiceCard
              id="profile-mode-read"
              value="read"
              icon={<IconEye />}
              title={t("chat.mode.read")}
              description={t("profile.modeRead")}
            />
            <ChoiceCard
              id="profile-mode-ask"
              value="ask"
              icon={<IconPencilCheck />}
              title={t("chat.mode.ask")}
              description={t("profile.modeAsk")}
            />
          </RadioGroup>
        </FieldSet>
        <FieldSet>
          <FieldLegend variant="label">{t("profile.field.toolsets")}</FieldLegend>
          <FieldDescription>{t("profile.toolsetsHelp")}</FieldDescription>
          <FieldGroup data-slot="checkbox-group" className="gap-3">
            {TOOLSETS.map((ts) => (
              <Field key={ts} orientation="horizontal">
                <Checkbox
                  id={`profile-toolset-${ts}`}
                  checked={p.toolsets.includes(ts)}
                  disabled={ts === "core"}
                  onCheckedChange={(on) =>
                    set(
                      "toolsets",
                      TOOLSETS.filter((x) => (x === ts ? on === true : p.toolsets.includes(x))),
                    )
                  }
                />
                <FieldContent>
                  <FieldLabel htmlFor={`profile-toolset-${ts}`} className="font-normal">
                    {t(`profile.toolset.${ts}.title`)}
                  </FieldLabel>
                  <FieldDescription>{t(`profile.toolset.${ts}.description`)}</FieldDescription>
                </FieldContent>
              </Field>
            ))}
          </FieldGroup>
        </FieldSet>
        <Field>
          <FieldLabel htmlFor="profile-instructions">{t("profile.field.instructions")}</FieldLabel>
          <Textarea
            id="profile-instructions"
            rows={4}
            maxLength={4000}
            placeholder={t("profile.instructionsPlaceholder")}
            value={p.instructions}
            onChange={(e) => set("instructions", e.target.value)}
          />
        </Field>
      </FieldGroup>

      <Collapsible open={advanced} onOpenChange={setAdvanced}>
        <CollapsibleTrigger render={<Button variant="ghost" size="sm" className="text-muted-foreground -ms-2" />}>
          <IconChevronDown data-icon="inline-start" className="transition-transform in-aria-expanded:rotate-180" />
          {t("profile.advanced")}
        </CollapsibleTrigger>
        <CollapsibleContent>
          <FieldGroup className="pt-3">
            <FieldDescription>{t("profile.advancedHelp")}</FieldDescription>
            {list("denyTools")}
            {list("allowTools")}
            {list("allowDoctypes")}
            {list("denyDoctypes")}
            {list("allowMethods", true)}
            {list("denyMethods", true)}
            <Field orientation="horizontal">
              <FieldContent>
                <FieldTitle>{t("profile.field.callMethod")}</FieldTitle>
                <FieldDescription>{t("profile.callMethodHelp")}</FieldDescription>
              </FieldContent>
              <Switch
                checked={p.callMethod}
                onCheckedChange={(v) => set("callMethod", v)}
                aria-label={t("profile.field.callMethod")}
              />
            </Field>
            <Field orientation="horizontal">
              <FieldContent>
                <FieldTitle>{t("profile.field.keepHistory")}</FieldTitle>
                <FieldDescription>{t("profile.keepHistoryHelp")}</FieldDescription>
              </FieldContent>
              <Switch
                checked={p.keepHistory}
                onCheckedChange={(v) => set("keepHistory", v)}
                aria-label={t("profile.field.keepHistory")}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="profile-steps">{t("profile.field.stepLimit")}</FieldLabel>
              <Input id="profile-steps" type="number" min={1} max={100} value={steps} onChange={(e) => setSteps(e.target.value)} />
              <FieldDescription>{t("profile.stepLimitHelp")}</FieldDescription>
            </Field>
          </FieldGroup>
        </CollapsibleContent>
      </Collapsible>
      {error && (
        <Alert variant="destructive" role="alert">
          <IconAlertTriangle />
          <AlertDescription>{error.message}</AlertDescription>
        </Alert>
      )}
      <div className="flex justify-end">
        <Button type="submit" disabled={saving}>
          {t("common.save")}
        </Button>
      </div>
    </form>
  )
}
