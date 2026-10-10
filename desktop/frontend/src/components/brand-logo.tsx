import { cn } from "cn"
import { useTranslation } from "react-i18next"

/**
 * The Foxmayn logo (white fox on a black rounded square), never recoloured.
 * Small sizes use the 64 px copy, larger ones the 256 px copy.
 */
export function BrandLogo({ size, className }: { size: number; className?: string }) {
  const { t } = useTranslation()
  const src = size <= 32 ? "/brand/favicon-64.png" : "/brand/logo-256.png"
  return (
    <img
      src={src}
      width={size}
      height={size}
      alt={t("shell.logoAlt")}
      draggable={false}
      className={cn("shrink-0 select-none", className)}
    />
  )
}
