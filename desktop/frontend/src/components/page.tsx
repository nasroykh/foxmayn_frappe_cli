import * as React from "react"
import { IconAlertTriangle, IconDownload, IconRefresh } from "@tabler/icons-react"

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
        <h1 className="font-heading text-xl font-semibold tracking-tight">{title}</h1>
        <p className="text-muted-foreground text-sm">{description}</p>
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  )
}

/** Technical detail behind a disclosure, so the main text stays friendly. */
export function Details({ children, label = "Technical details" }: { children: React.ReactNode; label?: string }) {
  return (
    <Collapsible>
      <CollapsibleTrigger render={<Button variant="link" size="xs" className="text-muted-foreground h-auto px-0" />}>
        {label}
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
  return (
    <Alert variant="destructive" className="has-data-[slot=alert-action]:pr-28">
      <IconAlertTriangle />
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>
        <p>{error.message}</p>
        {error.detail && <Details>{error.detail}</Details>}
      </AlertDescription>
      <AlertAction>
        <Button variant="outline" size="sm" onClick={onRetry}>
          <IconRefresh data-icon="inline-start" />
          Try again
        </Button>
      </AlertAction>
    </Alert>
  )
}

/** Shown while the ffc helper is not installed: assistants cannot run without it. */
export function FFCMissingAlert() {
  const { env, installFFC } = useApp()
  const ffc = env.data?.ffc
  if (!ffc || (ffc.found && !ffc.error)) return null
  return (
    <Alert className="has-data-[slot=alert-action]:pr-28">
      <IconAlertTriangle />
      <AlertTitle>{ffc.found ? "The ffc helper does not answer" : "One more thing: install the ffc helper"}</AlertTitle>
      <AlertDescription>
        {ffc.found
          ? "ffc was found but did not start. Installing it again usually fixes this."
          : "Assistants use ffc, a small program from Foxmayn, to reach your sites. You can add sites without it, but assistants need it."}
      </AlertDescription>
      <AlertAction>
        <Button size="sm" onClick={installFFC}>
          <IconDownload data-icon="inline-start" />
          {ffc.found ? "Reinstall" : "Install ffc"}
        </Button>
      </AlertAction>
    </Alert>
  )
}
