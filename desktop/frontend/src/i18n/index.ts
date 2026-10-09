import i18n from "i18next"
import { initReactI18next } from "react-i18next"

import en from "./locales/en.json"

export const resources = { en: { translation: en } } as const

/** Languages whose script runs right to left. */
const RTL = new Set(["ar", "he", "fa", "ur"])

/** Text direction for a language tag such as "ar" or "ar-EG". */
export function dir(lng: string): "rtl" | "ltr" {
  return RTL.has(lng.toLowerCase().split("-")[0]) ? "rtl" : "ltr"
}

/** Keep <html lang dir> in step with the active language. */
export function applyDocumentLanguage(lng: string) {
  document.documentElement.lang = lng
  document.documentElement.dir = dir(lng)
}

void i18n.use(initReactI18next).init({
  resources,
  lng: "en",
  fallbackLng: "en",
  interpolation: { escapeValue: false }, // React already escapes
})

applyDocumentLanguage(i18n.language)
i18n.on("languageChanged", applyDocumentLanguage)

export default i18n
