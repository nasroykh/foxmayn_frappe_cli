import { IconCheck, IconCopy } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

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
}: {
  value: string
  label: string
  copiedTitle?: string
  mono?: boolean
}) {
  const { t } = useTranslation()
  const [done, setDone] = React.useState(false)
  React.useEffect(() => {
    if (!done) return
    const timer = setTimeout(() => setDone(false), 1500)
    return () => clearTimeout(timer)
  }, [done])

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
