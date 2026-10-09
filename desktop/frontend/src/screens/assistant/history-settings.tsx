// Settings > Assistant history: how long conversations are kept.
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
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import { Skeleton } from "@/components/ui/skeleton"
import { toast } from "@/components/ui/toast"
import { backend } from "@/lib/backend"
import type { RetentionDays } from "@/lib/backend-types"
import { appError, errorTitle } from "@/lib/errors"

const CHOICES: RetentionDays[] = [0, 90, 30]

function parse(v: string): RetentionDays {
  return v === "30" ? 30 : v === "90" ? 90 : 0
}

export function HistorySettings() {
  const { t } = useTranslation()
  const [days, setDays] = React.useState<RetentionDays | null>(null)
  const [asking, setAsking] = React.useState<RetentionDays | null>(null)
  const [busy, setBusy] = React.useState(false)

  React.useEffect(() => {
    let live = true
    backend.getRetention().then(
      (d) => live && setDays(d),
      (err) => {
        const e = appError(err)
        toast.add({ title: errorTitle(e), description: e.message, type: "error" })
        if (live) setDays(0)
      },
    )
    return () => {
      live = false
    }
  }, [])

  async function save(next: RetentionDays) {
    setBusy(true)
    try {
      await backend.setRetention(next)
      setDays(next)
    } catch (err) {
      const e = appError(err)
      toast.add({ title: errorTitle(e), description: e.message, type: "error" })
    } finally {
      setBusy(false)
      setAsking(null)
    }
  }

  function choose(next: RetentionDays) {
    if (next === days) return
    // Keeping less is the one choice that can delete something: ask first.
    if (next !== 0) setAsking(next)
    else void save(next)
  }

  return (
    <section className="flex flex-col gap-3" aria-labelledby="history-title">
      <div>
        <h3 id="history-title" className="text-sm font-semibold">
          {t("history.title")}
        </h3>
        <p className="text-muted-foreground text-sm">{t("history.intro")}</p>
      </div>
      {days === null ? (
        <Skeleton className="h-8 w-48" />
      ) : (
        <Field className="max-w-xs">
          <FieldLabel htmlFor="retention">{t("history.keep")}</FieldLabel>
          <NativeSelect
            id="retention"
            value={String(days)}
            disabled={busy}
            onChange={(e) => choose(parse(e.target.value))}
          >
            {CHOICES.map((d) => (
              <NativeSelectOption key={d} value={String(d)}>
                {t(`history.choice.${d}`)}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          <FieldDescription>{t("history.help")}</FieldDescription>
        </Field>
      )}

      <AlertDialog open={asking !== null} onOpenChange={(o) => !o && !busy && setAsking(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("history.confirmTitle", { days: asking ?? 0 })}</AlertDialogTitle>
            <AlertDialogDescription>{t("history.confirmBody", { days: asking ?? 0 })}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={busy}
              onClick={() => asking !== null && void save(asking)}
            >
              {t("history.confirm", { days: asking ?? 0 })}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}
