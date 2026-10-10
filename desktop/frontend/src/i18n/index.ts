import i18n, { type PostProcessorModule } from "i18next"
import { initReactI18next } from "react-i18next"

import ar from "./locales/ar.json"
import en from "./locales/en.json"
import fr from "./locales/fr.json"

export const resources = { en: { translation: en }, fr: { translation: fr }, ar: { translation: ar } } as const

export type Language = keyof typeof resources

/** The languages of the switcher, each named in itself. */
export const LANGUAGES: { code: Language; name: string }[] = [
  { code: "en", name: "English" },
  { code: "fr", name: "Français" },
  { code: "ar", name: "العربية" },
]

/**
 * The saved choice. public/theme-init.js reads the same key before the first
 * paint, with the same rules as detectLanguage: keep the two in step.
 */
export const LANGUAGE_KEY = "ffd-language"

/**
 * The pseudo-locale (mock mode only): English with every letter accented and
 * every string bracketed and made longer, so text that skips the catalog and
 * text that would clip both stand out.
 */
export const PSEUDO = "pseudo"
const pseudoOn = import.meta.env.MODE === "mock"

function isLanguage(v: unknown): v is Language {
  return typeof v === "string" && Object.prototype.hasOwnProperty.call(resources, v)
}

/** The base language of a tag ("fr-CA" → "fr") when the app speaks it. */
export function baseLanguage(tag: string): Language | undefined {
  const base = tag.toLowerCase().split(/[-_]/)[0]
  return isLanguage(base) ? base : undefined
}

/** The saved language, else the first system language the app speaks, else English. */
export function detectLanguage(): Language | typeof PSEUDO {
  try {
    const saved = localStorage.getItem(LANGUAGE_KEY)
    if (isLanguage(saved)) return saved
    if (pseudoOn && saved === PSEUDO) return PSEUDO
  } catch {
    // Storage can be unavailable; follow the system.
  }
  const tags = navigator.languages?.length ? navigator.languages : [navigator.language]
  for (const tag of tags) {
    const l = tag && baseLanguage(tag)
    if (l) return l
  }
  return "en"
}

/** Languages whose script runs right to left. */
const RTL = new Set(["ar", "he", "fa", "ur"])

/** Text direction for a language tag such as "ar" or "ar-EG". */
export function dir(lng: string): "rtl" | "ltr" {
  return RTL.has(lng.toLowerCase().split("-")[0]) ? "rtl" : "ltr"
}

/**
 * The locale for Intl and toLocale* formatting: Arabic uses the Algerian
 * conventions with Latin digits, as the app's numbers and site data do.
 */
export function intlLocale(lng: string = i18n.resolvedLanguage ?? i18n.language): string {
  switch (baseLanguage(lng)) {
    case "ar":
      return "ar-DZ-u-nu-latn"
    case "fr":
      return "fr"
    default:
      return "en"
  }
}

/** The language in use, as one of the app's (the pseudo-locale is English). */
export function currentLanguage(): Language {
  return baseLanguage(i18n.language) ?? "en"
}

/** Keep <html lang dir> in step with the active language. */
export function applyDocumentLanguage(lng: string) {
  const l = lng === PSEUDO ? "en" : lng
  document.documentElement.lang = l
  document.documentElement.dir = dir(l)
}

/** Switch the language and remember the choice. */
export function setLanguage(lng: Language | typeof PSEUDO) {
  try {
    localStorage.setItem(LANGUAGE_KEY, lng)
  } catch {
    // Not remembered this time; the choice still applies to this window.
  }
  void i18n.changeLanguage(lng)
}

const ACCENTS: Record<string, string> = {
  a: "á", b: "ƀ", c: "ç", d: "ð", e: "é", f: "ƒ", g: "ĝ", h: "ĥ", i: "í", j: "ĵ", k: "ķ", l: "ĺ", m: "ɱ",
  n: "ñ", o: "ó", p: "þ", q: "ǫ", r: "ŕ", s: "š", t: "ţ", u: "ú", v: "ṽ", w: "ŵ", x: "ẋ", y: "ý", z: "ž",
  A: "Á", B: "Ɓ", C: "Ç", D: "Ð", E: "É", F: "Ƒ", G: "Ĝ", H: "Ĥ", I: "Í", J: "Ĵ", K: "Ķ", L: "Ĺ", M: "Ṁ",
  N: "Ñ", O: "Ó", P: "Þ", Q: "Ǫ", R: "Ŕ", S: "Š", T: "Ţ", U: "Ú", V: "Ṽ", W: "Ŵ", X: "Ẋ", Y: "Ý", Z: "Ž",
}

/** The pseudo-locale form of a translated string. */
export function pseudo(s: string): string {
  if (!s) return s
  const accented = s.replace(/[A-Za-z]/g, (c) => ACCENTS[c] ?? c)
  // About 35% longer, like French and German often are.
  return `⟦${accented}${"~".repeat(Math.ceil(s.length * 0.35))}⟧`
}

const pseudoProcessor: PostProcessorModule = {
  type: "postProcessor",
  name: PSEUDO,
  process: (value: string) => (i18n.language === PSEUDO ? pseudo(value) : value),
}

if (pseudoOn) i18n.use(pseudoProcessor)

void i18n.use(initReactI18next).init({
  resources,
  lng: detectLanguage(),
  fallbackLng: "en",
  supportedLngs: pseudoOn ? ["en", "fr", "ar", PSEUDO] : ["en", "fr", "ar"],
  interpolation: { escapeValue: false }, // React already escapes
  postProcess: pseudoOn ? [PSEUDO] : false,
  returnNull: false,
})

applyDocumentLanguage(i18n.language)
i18n.on("languageChanged", applyDocumentLanguage)

export default i18n
