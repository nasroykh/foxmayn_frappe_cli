import { IconBrandUbuntu } from "@tabler/icons-react"

import { useApp } from "@/app/app-context"
import { Details } from "@/components/page"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"

/** Windows only: an ffc config inside WSL is separate from this one. */
export function WSLAlert() {
  const { env } = useApp()
  const wsl = env.data?.wsl
  if (!wsl?.detected) return null
  const distros = wsl.distros ?? []
  return (
    <Alert>
      <IconBrandUbuntu />
      <AlertTitle>ffc settings found in WSL</AlertTitle>
      <AlertDescription>
        <p>
          {distros.length === 1 ? `Your Linux environment (${distros[0]})` : "One of your Linux environments"} has its
          own ffc settings. This app manages the Windows settings only, and the two are not synced. Add the sites here
          too if you want Windows assistants to use them.
        </p>
        {(wsl.paths?.length ?? 0) > 0 && <Details label="Where they are">{(wsl.paths ?? []).join("\n")}</Details>}
      </AlertDescription>
    </Alert>
  )
}
