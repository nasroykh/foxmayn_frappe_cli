import { IconBrandUbuntu } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import { useApp } from "@/app/app-context"
import { Details } from "@/components/page"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"

/** Windows only: an ffc config inside WSL is separate from this one. */
export function WSLAlert() {
  const { t } = useTranslation()
  const { env } = useApp()
  const wsl = env.data?.wsl
  if (!wsl?.detected) return null
  const distros = wsl.distros ?? []
  return (
    <Alert>
      <IconBrandUbuntu />
      <AlertTitle>{t("wsl.title")}</AlertTitle>
      <AlertDescription>
        <p>
          {distros.length === 1 ? t("wsl.bodyOne", { distro: distros[0] }) : t("wsl.bodyMany")}
        </p>
        {(wsl.paths?.length ?? 0) > 0 && <Details label={t("wsl.where")}>{(wsl.paths ?? []).join("\n")}</Details>}
      </AlertDescription>
    </Alert>
  )
}
