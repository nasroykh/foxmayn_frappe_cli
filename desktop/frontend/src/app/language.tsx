import * as React from "react"
import { useTranslation } from "react-i18next"

import { DirectionProvider } from "@/components/ui/direction"
import { currentLanguage, dir } from "@/i18n"
import { backend } from "@/lib/backend"

/**
 * Gives Base UI the text direction of the language in use and tells Go the
 * language, so its native dialogs (file pickers, the drop question) speak it.
 */
export function LanguageProvider({ children }: { children: React.ReactNode }) {
  const { i18n } = useTranslation()
  const lang = currentLanguage()

  React.useEffect(() => {
    backend.setLanguage(lang).catch(() => {
      // Best effort: the dialogs stay in the last language Go was told.
    })
  }, [lang])

  return <DirectionProvider direction={dir(i18n.language)}>{children}</DirectionProvider>
}
