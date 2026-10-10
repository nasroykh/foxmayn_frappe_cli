import { IconEye, IconEyeOff } from "@tabler/icons-react"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "@/components/ui/input-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

/** A masked input with a show/hide button. The value stays in this form only. */
export function SecretInput({
  id,
  value,
  onChange,
  invalid,
  autoComplete = "off",
  placeholder,
}: {
  id: string
  value: string
  onChange: (v: string) => void
  invalid?: boolean
  autoComplete?: string
  placeholder?: string
}) {
  const { t } = useTranslation()
  const [shown, setShown] = React.useState(false)
  const label = shown ? t("common.hide") : t("common.show")
  return (
    <InputGroup>
      <InputGroupInput
        id={id}
        type={shown ? "text" : "password"}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        aria-invalid={invalid || undefined}
        autoComplete={autoComplete}
        spellCheck={false}
        placeholder={placeholder}
      />
      <InputGroupAddon align="inline-end">
        <Tooltip>
          <TooltipTrigger
            render={
              <InputGroupButton
                size="icon-xs"
                aria-label={label}
                aria-pressed={shown}
                onClick={() => setShown((s) => !s)}
              />
            }
          >
            {shown ? <IconEyeOff /> : <IconEye />}
          </TooltipTrigger>
          <TooltipContent>{label}</TooltipContent>
        </Tooltip>
      </InputGroupAddon>
    </InputGroup>
  )
}
