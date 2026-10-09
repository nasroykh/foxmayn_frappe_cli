import { readdirSync, readFileSync, statSync } from "node:fs"
import path from "node:path"
import { describe, expect, it } from "vitest"

import en from "@/i18n/locales/en.json"

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = path.join(dir, name)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (/\.tsx?$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(p)
  }
  return out
}

function has(obj: unknown, key: string): boolean {
  let cur: any = obj
  for (const part of key.split(".")) {
    if (cur === null || typeof cur !== "object" || !(part in cur)) return false
    cur = cur[part]
  }
  return typeof cur === "string"
}

describe("i18n catalog", () => {
  const src = path.resolve(import.meta.dirname, "..")
  const used = new Map<string, string>()
  for (const file of walk(src)) {
    const text = readFileSync(file, "utf8")
    for (const m of text.matchAll(/(?<![\w.])(?:i18n\.)?t\(\s*["'`]([^"'`$]+)["'`]/g)) used.set(m[1], file)
  }

  it("finds the keys in use", () => {
    expect(used.size).toBeGreaterThan(0)
  })

  it("has every t() key in en.json", () => {
    const missing = [...used].filter(([key]) => !has(en, key)).map(([key, file]) => `${key} (${path.relative(src, file)})`)
    expect(missing).toEqual([])
  })
})
