import { afterEach, describe, expect, it, vi } from "vitest"

import i18n, { detectLanguage, dir, intlLocale, LANGUAGE_KEY, pseudo, setLanguage } from "@/i18n"
import { appError, errorTitle } from "@/lib/errors"

function languages(tags: string[]) {
  vi.spyOn(navigator, "languages", "get").mockReturnValue(tags)
}

afterEach(async () => {
  vi.restoreAllMocks()
  localStorage.clear()
  await i18n.changeLanguage("en")
})

describe("language detection", () => {
  it("prefers the saved choice", () => {
    languages(["fr-FR"])
    localStorage.setItem(LANGUAGE_KEY, "ar")
    expect(detectLanguage()).toBe("ar")
  })

  it("follows the first system language the app speaks", () => {
    languages(["de-DE", "ar-DZ", "fr"])
    expect(detectLanguage()).toBe("ar")
    languages(["en-GB", "fr"])
    expect(detectLanguage()).toBe("en")
  })

  it("falls back to English", () => {
    languages(["de", "es"])
    localStorage.setItem(LANGUAGE_KEY, "klingon")
    expect(detectLanguage()).toBe("en")
  })

  it("ignores the pseudo-locale outside mock mode", () => {
    languages(["fr"])
    localStorage.setItem(LANGUAGE_KEY, "pseudo")
    expect(detectLanguage()).toBe("fr")
  })
})

describe("setLanguage", () => {
  it("switches, saves and sets <html lang dir>", async () => {
    setLanguage("ar")
    await vi.waitFor(() => expect(i18n.language).toBe("ar"))
    expect(localStorage.getItem(LANGUAGE_KEY)).toBe("ar")
    expect(document.documentElement.lang).toBe("ar")
    expect(document.documentElement.dir).toBe("rtl")
    setLanguage("fr")
    await vi.waitFor(() => expect(document.documentElement.dir).toBe("ltr"))
    expect(document.documentElement.lang).toBe("fr")
  })
})

describe("formatting", () => {
  it("uses Latin digits and Algerian conventions in Arabic", () => {
    expect(intlLocale("ar")).toBe("ar-DZ-u-nu-latn")
    expect(new Intl.NumberFormat(intlLocale("ar")).format(1234.5)).toMatch(/1.?234[.,]5/)
    expect(intlLocale("fr-CA")).toBe("fr")
    expect(intlLocale("de")).toBe("en")
  })

  it("knows the direction", () => {
    expect(dir("ar-EG")).toBe("rtl")
    expect(dir("fr")).toBe("ltr")
  })

  it("pseudo-localizes letters and brackets the text", () => {
    expect(pseudo("Save")).toMatch(/^⟦Šáṽé~+⟧$/)
    expect(pseudo("")).toBe("")
  })
})

describe("errors", () => {
  const goError = (key: string, message: string, args?: Record<string, string>) =>
    Object.assign(new Error(message), { cause: { code: "failed", message, key, args } })

  it("translates a keyed error and fills its arguments", async () => {
    await i18n.changeLanguage("fr")
    const e = appError(goError("site.status", "The site answered with an error (502).", { status: "502" }))
    expect(e.message).toBe("Le site a répondu par une erreur (502).")
    expect(errorTitle(e)).toBe("Une erreur s’est produite")
  })

  it("keeps Go's English message for an unknown key", async () => {
    await i18n.changeLanguage("ar")
    expect(appError(goError("nope.missing", "As Go said it.")).message).toBe("As Go said it.")
  })

  it("leaves errors without a key as they are", () => {
    expect(appError(Object.assign(new Error("x"), { cause: { code: "auth", message: "Plain" } })).message).toBe("Plain")
  })
})
