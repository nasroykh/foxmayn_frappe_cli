import { readFileSync } from "node:fs"
import path from "node:path"
import { describe, expect, it } from "vitest"

import { applyDocumentLanguage, dir } from "@/i18n"

const html = readFileSync(path.resolve(import.meta.dirname, "../../index.html"), "utf8")
const csp = /http-equiv="Content-Security-Policy"\s+content="([^"]*)"/.exec(html)?.[1] ?? ""
const directive = (name: string) =>
  csp
    .split(";")
    .map((d) => d.trim())
    .find((d) => d.startsWith(name + " "))

describe("index.html CSP", () => {
  it("is present", () => {
    expect(csp).not.toBe("")
  })
  it("never allows eval", () => {
    expect(csp).not.toContain("unsafe-eval")
  })
  it("script-src has no inline", () => {
    const d = directive("script-src")
    expect(d).toBeDefined()
    expect(d).not.toContain("unsafe-inline")
  })
  it("has no inline script in the page", () => {
    expect(html).not.toMatch(/<script(?![^>]*\bsrc=)[^>]*>/i)
  })
  it("locks object-src and base-uri", () => {
    expect(directive("object-src")).toBe("object-src 'none'")
    expect(directive("base-uri")).toBe("base-uri 'none'")
  })
})

describe("language direction", () => {
  it("is rtl for Arabic only", () => {
    expect(dir("ar")).toBe("rtl")
    expect(dir("ar-EG")).toBe("rtl")
    expect(dir("en")).toBe("ltr")
    expect(dir("fr")).toBe("ltr")
  })
  it("sets <html lang dir>", () => {
    applyDocumentLanguage("ar")
    expect(document.documentElement.dir).toBe("rtl")
    expect(document.documentElement.lang).toBe("ar")
    applyDocumentLanguage("en")
    expect(document.documentElement.dir).toBe("ltr")
  })
})
