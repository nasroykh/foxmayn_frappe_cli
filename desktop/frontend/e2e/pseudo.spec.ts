import { expect, test } from "@playwright/test"

import { states } from "./states"
import { openApp, settle } from "./support"

// The pseudo-locale (mock mode) turns every catalog string, values included,
// into ⟦Áççéñţéð~~⟧. What is left of a visible text or an accessible name
// once the ⟦…⟧ parts are removed skipped the catalog, unless each of its
// pieces (split at · • | , : ( ) and line breaks) is exactly one of these:
// the mock's data and names that are never translated.
const DATA = new Set([
  // Products, programs, languages in their own names, keys.
  "Foxmayn", "Frappe Desktop", "Foxmayn Frappe Desktop", "ffc", "Frappe", "ERPNext",
  "Claude Desktop", "Claude Code", "Cursor", "VS Code", "Codex",
  "Anthropic", "OpenAI", "Google Gemini", "OpenRouter", "Ollama", "LM Studio",
  "Windows", "macOS", "English", "Français", "Pseudo", "Ctrl", "Shift", "Enter", "Esc", "⌘", "K", "N", "B", "F",
  // The mock's sites, users, hosts and paths.
  "acme-prod", "acme-staging", "local-bench", "erp.acme.example", "staging.acme.example", "localhost",
  // The add-site form's example address.
  "erp.example.com",
  "https://erp.acme.example", "https://staging.acme.example", "http://localhost:8000",
  "nas@acme.example", "integration@acme.example", "Administrator",
  "AP", "AS", "LB",
  // Models, tools and documents of the scripted assistant; what the tests type.
  "claude-sonnet-5-5", "get_doc", "update_doc", "ToDo", "TD-0001", "update the todo",
])

const PIECES = /\s*[·•|,:()\n]\s*/

function outside(text: string): string[] {
  const rest = text.replace(/⟦[^⟧]*⟧/g, "\n")
  return rest
    .split(PIECES)
    .map((p) => p.trim())
    .filter((p) => /[A-Za-z]{2,}/.test(p) && !DATA.has(p) && !/^v?\d+(\.\d+)+$/.test(p))
}

for (const s of states) {
  test(`pseudo: ${s.name}`, async ({ page }) => {
    await openApp(page, { lang: "pseudo", query: s.query, onboarded: s.onboarded ?? true })
    await s.reach?.(page)
    await settle(page)
    const found = await page.evaluate(() => {
      const out: string[] = []
      const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
      let n: Node | null
      while ((n = walker.nextNode())) {
        const el = n.parentElement
        // Code blocks and diffs hold site data (JSON, field values).
        if (!el || el.closest("script, style, pre")) continue
        const r = el.getBoundingClientRect()
        if (r.width === 0 && r.height === 0 && !el.closest(".sr-only")) continue
        out.push(n.textContent ?? "")
      }
      for (const el of document.querySelectorAll("[aria-label], [placeholder], [title], [alt], [aria-description], [aria-roledescription]")) {
        for (const a of ["aria-label", "placeholder", "title", "alt", "aria-description", "aria-roledescription"]) {
          const v = el.getAttribute(a)
          if (v) out.push(v)
        }
      }
      out.push(document.title)
      return out
    })
    const missed = [...new Set(found.flatMap(outside))]
    expect(missed, "text outside the catalog").toEqual([])
  })
}
