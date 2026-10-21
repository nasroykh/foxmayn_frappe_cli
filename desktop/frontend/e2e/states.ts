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

/** Settings > About, then the update card's primary button (Install update) opens the dialog. */
async function openUpdateInstall(page: Page) {
  await goTo(page, "settings")
  await page.getByRole("tab").last().click()
  await page.locator('[data-slot="alert-action"] button').first().click()
  await expect(dialog(page)).toBeVisible()
}

/** The dialog's last footer button: Install and restart. */
async function confirmUpdateInstall(page: Page) {
  await dialog(page).locator('[data-slot="dialog-footer"] button').last().click()
}

/** The first site's (acme-prod) menu, then its Safety rules action; waits for the loaded policy. */
async function openSafety(page: Page) {
  await page.locator("main [role=listitem]").first().locator("button:has(svg.tabler-icon-dots)").click()
  await page.locator("[role=menuitem]:has(svg.tabler-icon-shield-check)").click()
  await expect(dialog(page)).toBeVisible()
  await expect(page.locator("#safety-ask")).toBeVisible({ timeout: 5000 })
}

export const states: State[] = [
  { name: "sites" },
  {
    name: "sites-checked",
    reach: async (page) => {
      // The first site's check button (the refresh icon), then its toast.
      await page.locator("main [role=listitem] button:has(svg.tabler-icon-refresh)").first().click()
      await expect(page.locator('[data-slot="toast"]').first()).toBeVisible({ timeout: 8000 })
    },
  },
  {
    name: "safety-rules",
    reach: async (page) => {
      await openSafety(page)
    },
  },
  {
    name: "safety-custom",
    reach: async (page) => {
      await openSafety(page)
      await page.locator("label[for=safety-custom]").click()
      await expect(page.getByTestId("safety-advanced")).toBeVisible()
    },
  },
  {
    name: "safety-confirm",
    reach: async (page) => {
      await openSafety(page)
      // acme-prod refuses User; lifting that opens access, which the dialog confirms.
      await page.locator("#safety-denyDoctypes").fill("")
      await page.locator("button[form=safety-form]").click()
      await expect(page.getByRole("alertdialog")).toBeVisible({ timeout: 5000 })
    },
  },
  {
    name: "change-address-signin",
    reach: async (page) => {
      // The second site (acme-staging, API key); another host asks for the sign-in again.
      await page.locator("main [role=listitem]").nth(1).locator("button:has(svg.tabler-icon-dots)").click()
      await page.locator("[role=menuitem]:has(svg.tabler-icon-link)").click()
      await expect(dialog(page)).toBeVisible()
      await page.locator("#one-field").fill("https://erp.other-server.example")
      await expect(page.getByTestId("url-sign-in")).toBeVisible({ timeout: 5000 })
    },
  },
  {
    name: "replace-site-confirm",
    reach: async (page) => {
      await page.locator('[data-sidebar="menu-button"]').nth(6).click()
      await expect(dialog(page)).toBeVisible()
      await page.locator("#site-url").fill("https://staging.acme.example")
      await page.locator("#site-name").fill("acme-staging")
      await page.locator("label[for=site-replace]").click()
      await dialog(page).locator("button[form=site-form]").click()
      await expect(page.getByRole("alertdialog")).toBeVisible({ timeout: 5000 })
    },
  },
  {
    name: "local-only-off-confirm",
    reach: async (page) => {
      await goTo(page, "settings")
      await page.getByRole("tab").nth(1).click()
      const section = page.locator("section[aria-labelledby=site-settings-title]")
      await section.scrollIntoViewIfNeeded()
      const sw = section.locator("[data-slot=switch]")
      await expect(sw).toBeVisible({ timeout: 5000 })
      // On and saved first, then off: only turning it off asks.
      await sw.click()
      await expect(sw).toHaveAttribute("aria-checked", "true")
      await section.locator("button[type=submit]").click()
      await expect(page.locator('[data-slot="toast"]').first()).toBeVisible({ timeout: 5000 })
      await sw.click()
      await expect(sw).toHaveAttribute("aria-checked", "false")
      await section.locator("button[type=submit]").click()
      await expect(page.getByRole("alertdialog")).toBeVisible({ timeout: 5000 })
    },
  },
  {
    name: "add-site",
    reach: async (page) => {
      await page.locator('[data-sidebar="menu-button"]').nth(6).click()
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
      // Ask before changes: the mode switch in the chat header.
      await page.locator("section > header [data-slot=switch]").click()
      await expect(page.locator("section > header [data-slot=switch]")).toHaveAttribute("aria-checked", "true")
      await page.locator("#chat-input").fill("update the todo")
      await page.locator("#chat-input").press("Enter")
      await expect(page.locator("[data-slot=approval-card]").first()).toBeVisible({ timeout: 10_000 })
    },
  },
  ...(
    [
      // bulk_update: the field changes of each document.
      ["chat-approval-bulk", "close the todos in bulk"],
      // copy_doc: the fields that differ from the original.
      ["chat-approval-copy", "copy the todo"],
    ] as const
  ).map(([name, prompt]) => ({
    name,
    reach: async (page: Page) => {
      await newConversation(page)
      await dialog(page).locator("button[type=submit], [data-slot=button]").last().click()
      await page.locator("section > header [data-slot=switch]").click()
      await expect(page.locator("section > header [data-slot=switch]")).toHaveAttribute("aria-checked", "true")
      await page.locator("#chat-input").fill(prompt)
      await page.locator("#chat-input").press("Enter")
      await expect(page.locator("[data-slot=approval-card] table").first()).toBeVisible({ timeout: 10_000 })
    },
  })),
  {
    name: "chat-message-actions",
    reach: async (page) => {
      await newConversation(page)
      await dialog(page).locator("button[type=submit], [data-slot=button]").last().click()
      await page.locator("#chat-input").fill("What is TD-0001?")
      await page.locator("#chat-input").press("Enter")
      // The stored conversation (with footers) is back once the run ends.
      await expect(page.locator("[data-slot=message-footer]").last()).toBeAttached({ timeout: 10_000 })
      await page.locator("[data-slot=message-footer]").first().locator("..").hover()
    },
  },
  {
    name: "chat-attachments",
    reach: async (page) => {
      await newConversation(page)
      await dialog(page).locator("button[type=submit], [data-slot=button]").last().click()
      await expect(page.locator("#chat-input")).toBeVisible()
      // The mock's samples, in order: a CSV, an XLSX, a PDF and a DOCX.
      // The paperclip (the chips have buttons too, before it).
      const clip = page.locator("form:has(#chat-input) button:has(svg.tabler-icon-paperclip)")
      for (let i = 1; i <= 4; i++) {
        await clip.click()
        await expect(page.locator("form:has(#chat-input) ul li").nth(i - 1)).toBeVisible({ timeout: 5000 })
      }
    },
  },
  { name: "connect-apps", reach: (page) => goTo(page, "apps") },
  {
    name: "connect-dialog",
    reach: async (page) => {
      await goTo(page, "apps")
      await page.locator("main [data-slot=card] [data-slot=card-footer] button:has(svg.tabler-icon-plug-connected)").first().click()
      await expect(dialog(page)).toBeVisible()
    },
  },
  {
    name: "profile-editor",
    reach: async (page) => {
      await goTo(page, "settings")
      await page.getByRole("tab").nth(1).click()
      await page.locator("#profiles-title").scrollIntoViewIfNeeded()
      await page.locator("section[aria-labelledby=profiles-title] > div button").first().click()
      await expect(dialog(page)).toBeVisible()
    },
  },
  {
    name: "chat-more-menu",
    reach: async (page) => {
      await newConversation(page)
      await dialog(page).locator("button[type=submit], [data-slot=button]").last().click()
      await page.locator("section > header button[aria-haspopup=menu]").click()
      await expect(page.getByRole("menu")).toBeVisible()
    },
  },
  {
    name: "activity",
    reach: async (page) => {
      await goTo(page, "activity")
      await expect(page.locator("main ul[aria-label] li").first()).toBeVisible({ timeout: 5000 })
    },
  },
  {
    name: "activity-details",
    reach: async (page) => {
      await goTo(page, "activity")
      // The third row: a refused call, with arguments and an error text.
      await page.locator("main ul[aria-label] li button").nth(2).click()
      await expect(dialog(page)).toBeVisible()
      await expect(dialog(page).locator("pre")).toBeVisible()
    },
  },
  {
    name: "activity-empty",
    query: "activity=none",
    reach: async (page) => {
      await goTo(page, "activity")
      await expect(page.locator("main [data-slot=empty]")).toBeVisible({ timeout: 5000 })
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
  {
    name: "ffc-check-brew",
    query: "ffc=brew",
    reach: async (page) => {
      await goTo(page, "settings")
      await page.getByRole("tab").nth(2).click()
      await page.getByTestId("ffc-check").click()
      await expect(page.locator("main [data-slot=alert]").first()).toBeVisible({ timeout: 5000 })
    },
  },
  {
    name: "conversation-search",
    reach: async (page) => {
      await newConversation(page)
      await dialog(page).locator("button[type=submit], [data-slot=button]").last().click()
      await expect(page.locator("#chat-input")).toBeVisible()
      await page.getByRole("searchbox").fill("acme")
      // The count line says the search is done (results or none).
      await expect(page.locator("nav [aria-live=polite]").filter({ hasText: /…|\.\.\./ })).toHaveCount(0, { timeout: 5000 })
      await page.waitForTimeout(300)
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
  {
    // The confirmation with both warnings: a run is going (?busy=1) and the
    // dialog is opened from the About tab.
    name: "update-install-confirm",
    query: "update=available&busy=1",
    reach: async (page) => {
      await openUpdateInstall(page)
      await expect(dialog(page).locator('[data-slot="alert"]')).toBeVisible()
    },
  },
  {
    // ?install=hold stays in the download (16 of 48 MB) until cancelled.
    name: "update-install-progress",
    query: "update=available&install=hold",
    reach: async (page) => {
      await openUpdateInstall(page)
      await confirmUpdateInstall(page)
      await expect(dialog(page).getByRole("progressbar")).toBeVisible({ timeout: 5000 })
      await page.waitForTimeout(1800)
    },
  },
  {
    name: "update-install-failed",
    query: "update=available&install=fail",
    reach: async (page) => {
      await openUpdateInstall(page)
      await confirmUpdateInstall(page)
      await expect(dialog(page).locator('[data-slot="alert"]')).toBeVisible({ timeout: 8000 })
    },
  },
  {
    // The copy cannot update itself: no Install button, the reason and the command.
    name: "update-install-blocked",
    query: "update=available&install=blocked",
    reach: async (page) => {
      await goTo(page, "settings")
      await page.getByRole("tab").last().click()
      await expect(page.locator('[data-slot="alert"]').first()).toBeVisible()
    },
  },
]
