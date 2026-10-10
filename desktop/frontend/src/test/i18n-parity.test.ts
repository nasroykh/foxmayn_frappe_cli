import { readdirSync, readFileSync, statSync } from "node:fs"
import path from "node:path"
import { describe, expect, it } from "vitest"

import ar from "@/i18n/locales/ar.json"
import en from "@/i18n/locales/en.json"
import fr from "@/i18n/locales/fr.json"

type Catalog = Record<string, unknown>

/** Flattens a catalog to "a.b.c" keys. */
function flat(o: Catalog, prefix = "", out: Record<string, string> = {}) {
  for (const [k, v] of Object.entries(o)) {
    if (v && typeof v === "object") flat(v as Catalog, `${prefix}${k}.`, out)
    else out[prefix + k] = String(v)
  }
  return out
}

const PLURAL = /_(zero|one|two|few|many|other)$/
const vars = (s: string) => [...s.matchAll(/{{\s*(\w+)\s*}}/g)].map((m) => m[1]).sort()
const tags = (s: string) => [...s.matchAll(/<\/?(\w+)\s*\/?>/g)].map((m) => m[0]).sort()

const EN = flat(en)
const bases = new Set(Object.keys(EN).filter((k) => PLURAL.test(k)).map((k) => k.replace(PLURAL, "")))

describe.each([
  ["fr", fr],
  ["ar", ar],
] as const)("%s catalog", (lng, catalog) => {
  const T = flat(catalog)
  // The forms this language uses (fr: one, many, other; ar: all six).
  const categories = new Intl.PluralRules(lng).resolvedOptions().pluralCategories

  it("has every English key", () => {
    const missing = Object.keys(EN).filter((k) => !PLURAL.test(k) && !(k in T))
    expect(missing).toEqual([])
  })

  it("has every plural form of its language for each plural key", () => {
    // Without a form, i18next falls back to English for that count.
    const missing = [...bases].flatMap((b) => categories.filter((c) => !(`${b}_${c}` in T)).map((c) => `${b}_${c}`))
    expect(missing).toEqual([])
  })

  it("has no key English lacks and no empty string", () => {
    const bad = Object.entries(T)
      .filter(([k, v]) => (!(k in EN) && !bases.has(k.replace(PLURAL, ""))) || !v.trim())
      .map(([k]) => k)
    expect(bad).toEqual([])
  })

  it("keeps the placeholders and tags of the English text", () => {
    const bad: string[] = []
    for (const [k, v] of Object.entries(T)) {
      const base = k.replace(PLURAL, "")
      const source = EN[k] ?? (bases.has(base) ? EN[`${base}_other`] : undefined)
      if (source === undefined) continue
      // A plural form may say the number in words ("one", "two").
      const want = vars(source).filter((x) => !(bases.has(base) && x === "count"))
      const got = vars(v).filter((x) => !(bases.has(base) && x === "count"))
      if (want.join() !== got.join()) bad.push(`${k}: {${vars(source)}} vs {${vars(v)}}`)
      if (tags(source).join() !== tags(v).join()) bad.push(`${k}: tags ${tags(source)} vs ${tags(v)}`)
    }
    expect(bad).toEqual([])
  })

  it("uses Latin digits", () => {
    const eastern = Object.entries(T).filter(([, v]) => /[٠-٩۰-۹]/.test(v)).map(([k]) => k)
    expect(eastern).toEqual([])
  })
})

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = path.join(dir, name)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (/\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(p)
  }
  return out
}

describe("dynamic keys", () => {
  // Keys built at run time are listed in a "// i18n keys:" comment next to
  // the code; each listed key, or subtree for a "prefix.*" entry, must exist.
  const src = path.resolve(import.meta.dirname, "..")
  const listed: [string, string][] = []
  for (const file of walk(src)) {
    const lines = readFileSync(file, "utf8").split("\n")
    for (let i = 0; i < lines.length; i++) {
      if (!lines[i].includes("i18n keys:")) continue
      // The comment may run on to the next comment lines.
      let text = lines[i].split("i18n keys:")[1]
      for (let j = i + 1; j < lines.length && /^\s*\/\/(?!.*i18n keys:)/.test(lines[j]); j++) text += " " + lines[j].replace(/^\s*\/\//, "")
      for (const tok of text.split(/[\s,()]+/)) if (/^[a-zA-Z][\w-]*(\.[\w*-]+)+$/.test(tok)) listed.push([tok, path.relative(src, file)])
    }
  }

  it("finds the listed keys", () => {
    expect(listed.length).toBeGreaterThan(5)
  })

  it("has every listed key in en.json", () => {
    const get = (k: string) => k.split(".").reduce<unknown>((c, p) => (c && typeof c === "object" ? (c as Catalog)[p] : undefined), en)
    const missing = listed
      .filter(([k]) => {
        const v = get(k.replace(/\.\*$/, ""))
        return v === undefined && get(`${k}_other`) === undefined
      })
      .map(([k, f]) => `${k} (${f})`)
    expect(missing).toEqual([])
  })
})
