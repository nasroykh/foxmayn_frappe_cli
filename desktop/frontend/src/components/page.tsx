import * as React from "react"
import { useTranslation } from "react-i18next"
import { IconAlertTriangle, IconChevronDown, IconDownload, IconRefresh } from "@tabler/icons-react"

import { useApp } from "@/app/app-context"
import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import type { AppError } from "@/lib/errors"

/** The title row of a screen. */
export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string
  description: React.ReactNode
  actions?: React.ReactNode
}) {
  return (
    <div className="flex items-start justify-between gap-4">
      <div className="flex max-w-prose min-w-0 flex-col gap-1">
        <h1 className="font-heading text-xl font-semibold tracking-tight outline-none">{title}</h1>
        <p className="text-muted-foreground text-sm">{description}</p>
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  )
}

/** Technical detail behind a disclosure, so the main text stays friendly. */
export function Details({
  children,
  label,
  defaultOpen,
}: {
  children: React.ReactNode
  label?: string
  defaultOpen?: boolean
}) {
  const { t } = useTranslation()
  return (
    <Collapsible defaultOpen={defaultOpen}>
      <CollapsibleTrigger render={<Button variant="link" size="xs" className="text-muted-foreground h-auto px-0" />}>
        <IconChevronDown data-icon="inline-start" className="transition-transform in-aria-expanded:rotate-180" />
        {label ?? t("common.technicalDetails")}
      </CollapsibleTrigger>
      <CollapsibleContent>
        <pre className="bg-muted mt-1 max-h-40 overflow-auto rounded-md p-2 font-mono text-xs whitespace-pre-wrap break-all">
          {children}
        </pre>
      </CollapsibleContent>
    </Collapsible>
  )
}

/** A failed load, with a retry button. */
export function LoadError({ title, error, onRetry }: { title: string; error: AppError; onRetry: () => void }) {
  const { t } = useTranslation()
  return (
    <Alert variant="destructive" className="has-data-[slot=alert-action]:pe-28">
      <IconAlertTriangle />
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>
        <p>{error.message}</p>
        {error.detail && <Details>{error.detail}</Details>}
      </AlertDescription>
      <AlertAction>
        <Button variant="outline" size="sm" onClick={onRetry}>
          <IconRefresh data-icon="inline-start" />
          {t("common.retry")}
        </Button>
      </AlertAction>
    </Alert>
  )
}

/** Shown while the ffc helper is not installed: assistants cannot run without it. */
export function FFCMissingAlert() {
  const { t } = useTranslation()
  const { env, installFFC } = useApp()
  const ffc = env.data?.ffc
  if (!ffc || (ffc.found && !ffc.error)) return null
  return (
    <Alert className="has-data-[slot=alert-action]:pe-28">
      <IconAlertTriangle />
      <AlertTitle>{ffc.found ? t("page.ffcNoAnswer") : t("page.ffcMissing")}</AlertTitle>
      <AlertDescription>
        {ffc.found
          ? ffc.manager
            ? t("page.reinstallWith", { manager: ffc.manager })
            : t("page.reinstall")
          : t("page.needed")}
      </AlertDescription>
      {!ffc.manager && (
        <AlertAction>
          <Button size="sm" onClick={installFFC}>
            <IconDownload data-icon="inline-start" />
            {ffc.found ? t("page.reinstallButton") : t("page.installButton")}
          </Button>
        </AlertAction>
      )}
    </Alert>
  )
}
