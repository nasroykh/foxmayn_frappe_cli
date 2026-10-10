import { useTranslation } from "react-i18next"

import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { LANGUAGES, PSEUDO, setLanguage, type Language } from "@/i18n"

const pseudoOn = import.meta.env.MODE === "mock"

/** The language choice: each language named in itself. */
export function LanguageSwitcher({ className }: { className?: string }) {
  const { t, i18n } = useTranslation()
  return (
    <ToggleGroup
      variant="outline"
      className={className}
      value={[i18n.language]}
      onValueChange={(v: string[]) => v[0] && setLanguage(v[0] as Language)}
      aria-label={t("language.label")}
    >
      {LANGUAGES.map((l) => (
        <ToggleGroupItem key={l.code} value={l.code} lang={l.code}>
          {l.name}
        </ToggleGroupItem>
      ))}
      {pseudoOn && (
        <ToggleGroupItem value={PSEUDO} lang="en">
          Pseudo
        </ToggleGroupItem>
      )}
    </ToggleGroup>
  )
}
