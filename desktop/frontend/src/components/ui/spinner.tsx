import { cn } from "cn"
import { IconLoader } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

function Spinner({ className, ...props }: React.ComponentProps<"svg">) {
  const { t } = useTranslation()
  return (
    <IconLoader data-slot="spinner" role="status" aria-label={t("common.loading")} className={cn("size-4 animate-spin", className)} {...props} />
  )
}

export { Spinner }
