import { IconCheck, IconCopy } from "@tabler/icons-react"
import * as React from "react"

import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "@/components/ui/input-group"
import { toast } from "@/components/ui/toast"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { backend } from "@/lib/backend"

export async function copy(text: string, what = "Copied") {
  try {
    await backend.copyText(text)
    toast.add({ title: what, description: "It is on your clipboard.", type: "success" })
    return true
  } catch {
    toast.add({ title: "Could not copy", description: "Select the text and copy it yourself.", type: "error" })
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
  const [done, setDone] = React.useState(false)
  React.useEffect(() => {
    if (!done) return
    const t = setTimeout(() => setDone(false), 1500)
    return () => clearTimeout(t)
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
                aria-label={`Copy ${label.toLowerCase()}`}
                onClick={async () => setDone(await copy(value, copiedTitle))}
              />
            }
          >
            {done ? <IconCheck /> : <IconCopy />}
          </TooltipTrigger>
          <TooltipContent>Copy</TooltipContent>
        </Tooltip>
      </InputGroupAddon>
    </InputGroup>
  )
}
