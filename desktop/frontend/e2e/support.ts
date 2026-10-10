import AxeBuilder from "@axe-core/playwright"
import { expect, type Page, type TestInfo } from "@playwright/test"

export type Lang = "en" | "fr" | "ar"
export type Theme = "light" | "dark"
export const LANGS: Lang[] = ["en", "fr", "ar"]
export const THEMES: Theme[] = ["light", "dark"]

/** Opens the app (query picks a mock scenario) in a language and theme, past the welcome tour. */
export async function openApp(page: Page, opts: { lang?: Lang | "pseudo"; theme?: Theme; query?: string; onboarded?: boolean } = {}) {
  const { lang = "en", theme = "light", query = "", onboarded = true } = opts
  await page.addInitScript(
    ([l, t, o]) => {
      localStorage.setItem("ffd-language", l)
      localStorage.setItem("ffd-theme", t)
      if (o) localStorage.setItem("ffd-onboarded", "1")
    },
    [lang, theme, onboarded ? "1" : ""] as const,
  )
  await page.goto(query ? `/?${query}` : "/")
  await expect(page.locator("html")).toHaveAttribute("lang", lang === "pseudo" ? "en" : lang)
  // The fonts are bundled; wait so screenshots and widths use them.
  await page.evaluate(() => document.fonts.ready)
}

/** The sidebar's screen buttons, in order (the brand button is first). */
export async function goTo(page: Page, screen: "sites" | "assistant" | "apps" | "settings") {
  const i = { sites: 1, assistant: 2, apps: 3, settings: 4 }[screen]
  const button = page.locator('[data-sidebar="menu-button"]').nth(i)
  await button.click()
  // The screen's heading names what the sidebar button says.
  await expect(page.locator('[data-slot="sidebar-inset"] h1')).toHaveText((await button.innerText()).trim())
}

/** Lets animations and async loads settle before a check or a screenshot. */
export async function settle(page: Page) {
  await page.waitForLoadState("networkidle")
  await page.waitForTimeout(400)
}

/**
 * Text that does not fit: a text node whose box sticks out of an ancestor
 * that clips horizontally (overflow other than visible), or out of the window.
 * Measured on each text node's own rectangle, so inline text counts too.
 * Exempt: text that truncates on purpose (text-overflow: ellipsis or
 * line-clamp on its element), code blocks (pre, which scroll), anything under
 * data-clip-ok, and text entirely outside its clipping box (an off-screen
 * carousel slide, a scrolled-away row): only text that is partly cut fails.
 */
export async function clippedText(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const out: string[] = []
    const vw = document.documentElement.clientWidth
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
    const range = document.createRange()
    let n: Node | null
    while ((n = walker.nextNode())) {
      const text = n.textContent!.trim()
      if (!text) continue
      const el = n.parentElement
      if (!el || el.closest("[data-clip-ok], .sr-only, pre, svg, script, style, [aria-hidden=true]")) continue
      const own = getComputedStyle(el)
      if (own.visibility === "hidden" || own.textOverflow === "ellipsis" || own.webkitLineClamp !== "none") continue
      // Visually hidden text for screen readers (Base UI clips it to 1px on purpose).
      if (own.clipPath !== "none") continue
      range.selectNodeContents(n)
      const r = range.getBoundingClientRect()
      if (r.width === 0 || r.height === 0) continue
      const cut = (left: number, right: number) => r.right > left + 1 && r.left < right - 1 && (r.left < left - 1 || r.right > right + 1)
      let why = ""
      let shown = true
      for (let a: Element | null = el; a && !why; a = a.parentElement) {
        const s = getComputedStyle(a)
        if (s.overflowX === "visible") continue
        // Truncation on an ancestor (a flex child with .truncate around a span).
        if (s.textOverflow === "ellipsis") {
          shown = false
          break
        }
        const b = a.getBoundingClientRect()
        if (r.right <= b.left + 1 || r.left >= b.right - 1) {
          shown = false // entirely outside: not shown at all
          break
        }
        if (cut(b.left, b.right)) why = `<${a.tagName.toLowerCase()} class="${(a.getAttribute("class") ?? "").slice(0, 40)}">`
      }
      if (shown && !why && cut(0, vw)) why = "window"
      if (why) out.push(`"${text.replace(/\s+/g, " ").slice(0, 60)}" cut by ${why}`)
    }
    return out
  })
}

/** axe-core over WCAG 2.0/2.1 A and AA; serious and critical issues only. */
export async function seriousAxe(page: Page): Promise<string[]> {
  const res = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze()
  return res.violations
    .filter((v) => v.impact === "serious" || v.impact === "critical")
    .map((v) => `${v.id} (${v.impact}): ${v.help} — ${v.nodes.map((n) => n.target.join(" ")).slice(0, 4).join(", ")}`)
}

/** Saves a full-window screenshot into the test's output (uploaded by CI). */
export async function shot(page: Page, info: TestInfo, name: string) {
  await page.screenshot({ path: info.outputPath(`${name}.png`) })
}
