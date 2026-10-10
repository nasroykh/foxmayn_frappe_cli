import { expect, test } from "@playwright/test"

import { states } from "./states"
import { clippedText, LANGS, openApp, seriousAxe, settle, shot, THEMES, type Lang } from "./support"

// Every screen and the main dialogs, in English, French and Arabic, light and
// dark: no text that does not fit, no serious or critical axe finding, and a
// screenshot for a person to look at (CI uploads test-results).

for (const s of states) {
  for (const lang of LANGS) {
    for (const theme of THEMES) {
      test(`${s.name} ${lang} ${theme}`, async ({ page }, info) => {
        await openApp(page, { lang: lang as Lang, theme, query: s.query, onboarded: s.onboarded ?? true })
        await s.reach?.(page)
        await settle(page)
        await shot(page, info, `${s.name}-${lang}-${theme}`)
        expect(await clippedText(page), "text that does not fit").toEqual([])
        expect(await seriousAxe(page), "serious or critical axe findings").toEqual([])
      })
    }
  }
}
