import { expect, test, type Page } from "@playwright/test"

import { openApp } from "./support"

// The whole chat flow with the keyboard only: no mouse event is sent.
// Every focused control must show a focus indicator.

async function focusVisible(page: Page) {
  const shown = await page.evaluate(() => {
    const el = document.activeElement as HTMLElement | null
    if (!el || el === document.body) return "nothing focused"
    const s = getComputedStyle(el)
    const ring = s.boxShadow !== "none" || (s.outlineStyle !== "none" && parseFloat(s.outlineWidth) > 0)
    return ring ? "" : `no focus indicator on <${el.tagName.toLowerCase()}> ${el.getAttribute("aria-label") ?? el.textContent?.trim().slice(0, 40)}`
  })
  expect(shown).toBe("")
}

/** Presses Tab until the focused element matches the CSS selector. */
async function tabTo(page: Page, selector: string, max = 40) {
  for (let i = 0; i < max; i++) {
    await page.keyboard.press("Tab")
    if (await page.evaluate((sel) => !!document.activeElement?.matches(sel), selector)) return
  }
  throw new Error(`tabbed ${max} times without reaching ${selector}`)
}

for (const lang of ["en", "ar"] as const) {
  test(`chat flow with the keyboard only (${lang})`, async ({ page }) => {
    await openApp(page, { lang })

    // The palette opens a new conversation.
    await page.keyboard.press("Control+k")
    await expect(page.getByRole("dialog")).toBeVisible()
    await page.keyboard.type(lang === "ar" ? "محادثة جديدة" : "new conversation")
    await page.keyboard.press("Enter")
    const form = page.getByRole("dialog")
    await expect(form).toBeVisible()

    // Site, provider and model keep their defaults; Tab to Start.
    await tabTo(page, "[type=submit]")
    await focusVisible(page)
    await page.keyboard.press("Enter")
    await expect(page.locator("#chat-input")).toBeVisible()

    // The mode switch: the conversation must ask before changes.
    await page.locator("#chat-input").focus()
    await page.keyboard.press("Shift+Tab")
    await focusVisible(page)

    // Ctrl+Shift+F focuses the search box.
    await page.keyboard.press("Control+Shift+F")
    await expect(page.getByRole("searchbox")).toBeFocused()

    // Back to the composer by keyboard and send a message.
    await tabTo(page, "#chat-input", 80)
    await page.keyboard.type("What is TD-0001?")
    await page.keyboard.press("Enter")
    // The run starts (the composer locks), reads the document, then answers.
    await expect(page.locator("#chat-input")).toBeDisabled()
    await expect(page.getByText("get_doc").first()).toBeVisible({ timeout: 15_000 })
    await expect(page.locator("#chat-input")).toBeEnabled({ timeout: 15_000 })

    // Ctrl+N: another conversation, from anywhere.
    await page.keyboard.press("Control+n")
    await expect(page.getByRole("dialog")).toBeVisible()
    await page.keyboard.press("Escape")
    await expect(page.getByRole("dialog")).toBeHidden()
  })
}

test("a screen opened from the palette gets the focus on its heading", async ({ page }) => {
  await openApp(page)
  await page.keyboard.press("Control+k")
  await page.keyboard.type("settings")
  await page.keyboard.press("Enter")
  const h1 = page.locator('[data-slot="sidebar-inset"] h1')
  await expect(h1).toBeFocused({ timeout: 2000 })
  // And it stays there once the palette's exit animation is over.
  await page.waitForTimeout(600)
  await expect(h1).toBeFocused()
})

test("approval card by keyboard: Decline has the focus, Tab reaches Approve, Esc stops a run", async ({ page }) => {
  await openApp(page)
  await page.keyboard.press("Control+n")
  await expect(page.getByRole("dialog")).toBeVisible()
  await tabTo(page, "[type=submit]")
  await page.keyboard.press("Enter")
  const input = page.locator("#chat-input")
  await expect(input).toBeVisible()

  // Ask before changes, chosen with the keyboard: Tab reaches the mode
  // switch (one tab stop, the pressed option), an arrow key moves to the
  // other option, Space presses it.
  const ask = page.locator("section > header [data-slot=toggle-group-item]").nth(1)
  await tabTo(page, "section > header [data-slot=toggle-group-item]", 80)
  await focusVisible(page)
  await page.keyboard.press("ArrowRight")
  await expect(ask).toBeFocused()
  await page.keyboard.press("Space")
  await expect(ask).toHaveAttribute("aria-pressed", "true")

  await input.focus()
  await page.keyboard.type("update the todo")
  await page.keyboard.press("Enter")
  const card = page.locator("[data-slot=approval-card]")
  await expect(card).toBeVisible({ timeout: 10_000 })
  // The last two buttons: Decline, then Approve.
  const decline = card.getByRole("button").nth(-2)
  await expect(decline).toBeFocused()
  await focusVisible(page)
  await page.keyboard.press("Tab")
  await expect(card.getByRole("button").last()).toBeFocused()
  await focusVisible(page)
  await page.keyboard.press("Shift+Tab")
  await page.keyboard.press("Enter")
  await expect(card).toBeHidden({ timeout: 10_000 })

  // A long run stops on Esc.
  await expect(input).toBeEnabled({ timeout: 15_000 })
  await input.focus()
  await page.keyboard.type("many")
  await page.keyboard.press("Enter")
  await expect(input).toBeDisabled()
  await page.keyboard.press("Escape")
  await expect(input).toBeEnabled({ timeout: 10_000 })
})
