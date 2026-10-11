import { IconCheck, IconCopy } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "@/components/ui/input-group"
import { toast } from "@/components/ui/toast"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import i18n from "@/i18n"
import { backend } from "@/lib/backend"

export async function copy(text: string, what?: string) {
  try {
    await backend.copyText(text)
    toast.add({ title: what ?? i18n.t("copy.copied"), description: i18n.t("copy.onClipboard"), type: "success" })
    return true
  } catch {
    toast.add({ title: i18n.t("copy.failed"), description: i18n.t("copy.failedHint"), type: "error" })
    return false
  }
}

/** A read-only value with a copy button, e.g. a command or a redirect URI. */
export function CopyField({
  value,
  label,
  copiedTitle,
  mono = true,
  wrap = false,
}: {
  value: string
  label: string
  copiedTitle?: string
  mono?: boolean
  /** Show the value as wrapped text (a long command) instead of a one-line input. */
  wrap?: boolean
}) {
  const { t } = useTranslation()
  const [done, setDone] = React.useState(false)
  React.useEffect(() => {
    if (!done) return
    const timer = setTimeout(() => setDone(false), 1500)
    return () => clearTimeout(timer)
  }, [done])

  if (wrap) {
    return (
      <div role="group" aria-label={label} className="bg-muted/50 flex items-start gap-2 rounded-lg border p-2">
        <code
          dir="ltr"
          className="min-w-0 flex-1 text-start font-mono text-xs break-all select-all"
        >
          {value}
        </code>
        <Button
          type="button"
          size="sm"
          variant="outline"
          className="shrink-0"
          onClick={async () => setDone(await copy(value, copiedTitle))}
        >
          {done ? <IconCheck data-icon="inline-start" /> : <IconCopy data-icon="inline-start" />}
          {t("common.copy")}
        </Button>
      </div>
    )
  }

  return (
    <InputGroup>
      <InputGroupInput
        readOnly
        value={value}
        aria-label={label}
        className={mono ? "font-mono text-xs" : undefined}
        onFocus={(e) => e.currentTarget.select()}
      />
      <InputGroupAddon align="inline-end">
        <Tooltip>
          <TooltipTrigger
            render={
              <InputGroupButton
                size="icon-xs"
                aria-label={t("copy.copyLabel", { label: label.toLowerCase() })}
                onClick={async () => setDone(await copy(value, copiedTitle))}
              />
            }
          >
            {done ? <IconCheck /> : <IconCopy />}
          </TooltipTrigger>
          <TooltipContent>{t("common.copy")}</TooltipContent>
        </Tooltip>
      </InputGroupAddon>
    </InputGroup>
  )
}
