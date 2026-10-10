import { expect, type Page } from "@playwright/test"

import { goTo } from "./support"

// The screens and dialogs the e2e specs visit. Selectors avoid visible text,
// which changes with the language.

export interface State {
  name: string
  query?: string
  onboarded?: boolean
  reach?: (page: Page) => Promise<void>
}

const dialog = (page: Page) => page.getByRole("dialog").last()

async function newConversation(page: Page) {
  await goTo(page, "assistant")
  await page.keyboard.press("Control+n")
  await expect(dialog(page)).toBeVisible()
}

export const states: State[] = [
  { name: "sites" },
  {
    name: "sites-checked",
    reach: async (page) => {
      // The first site's check button (the globe-check icon), then its toast.
      await page.locator("main [role=listitem] button:has(svg.tabler-icon-world-check)").first().click()
      await expect(page.locator('[data-slot="toast"]').first()).toBeVisible({ timeout: 8000 })
    },
  },
  {
    name: "add-site",
    reach: async (page) => {
      await page.locator('[data-sidebar="menu-button"]').nth(5).click()
      await expect(dialog(page)).toBeVisible()
    },
  },
  { name: "assistant-empty", reach: (page) => goTo(page, "assistant") },
  { name: "new-conversation", reach: newConversation },
  {
    name: "chat-starters",
    reach: async (page) => {
      await newConversation(page)
      await dialog(page).locator("button[type=submit], [data-slot=button]").last().click()
      await expect(page.locator("#chat-input")).toBeVisible()
    },
  },
  {
    name: "chat-approval",
    reach: async (page) => {
      await newConversation(page)
      await dialog(page).locator("button[type=submit], [data-slot=button]").last().click()
      // Ask before changes: the second option of the mode switch in the chat header.
      await page.locator("section > header [data-slot=toggle-group-item]").nth(1).click()
      await expect(page.locator("section > header [data-slot=toggle-group-item]").nth(1)).toHaveAttribute("aria-pressed", "true")
      await page.locator("#chat-input").fill("update the todo")
      await page.locator("#chat-input").press("Enter")
      await expect(page.locator("[data-slot=approval-card]").first()).toBeVisible({ timeout: 10_000 })
    },
  },
  { name: "connect-apps", reach: (page) => goTo(page, "apps") },
  {
    name: "connect-dialog",
    reach: async (page) => {
      await goTo(page, "apps")
      await page.locator("main [data-slot=card] [data-slot=card-footer] button").first().click()
      await expect(dialog(page)).toBeVisible()
    },
  },
  { name: "settings-general", reach: (page) => goTo(page, "settings") },
  ...[1, 2, 3].map((i) => ({
    name: `settings-tab${i + 1}`,
    reach: async (page: Page) => {
      await goTo(page, "settings")
      await page.getByRole("tab").nth(i).click()
    },
  })),
  {
    name: "palette",
    reach: async (page) => {
      await page.keyboard.press("Control+k")
      await expect(dialog(page)).toBeVisible()
    },
  },
  {
    name: "shortcuts",
    reach: async (page) => {
      await page.keyboard.press("Control+/")
      await expect(dialog(page)).toBeVisible()
    },
  },
  { name: "welcome", query: "sites=none", onboarded: false },
  { name: "assistant-setup", query: "providers=none", reach: (page) => goTo(page, "assistant") },
  { name: "ffc-missing", query: "ffc=missing" },
  {
    name: "update-available",
    query: "update=available",
    reach: async (page) => {
      await goTo(page, "settings")
      await page.getByRole("tab").last().click()
    },
  },
]
