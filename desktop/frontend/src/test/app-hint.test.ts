import { afterEach, describe, expect, it } from "vitest"

import i18n from "@/i18n"
import { appHint } from "@/lib/labels"

afterEach(async () => {
  await i18n.changeLanguage("en")
})

describe("appHint", () => {
  const goConnect = "Restart Cursor to load it."

  it("shows Go's own text in English, even when the catalog differs", async () => {
    await i18n.changeLanguage("en")
    expect(appHint("cursor", "connect", "Restart Cursor now, it changed.")).toBe("Restart Cursor now, it changed.")
  })

  it("translates by client and kind", async () => {
    await i18n.changeLanguage("fr")
    expect(appHint("cursor", "connect", goConnect)).toBe("Redémarrez Cursor pour le charger.")
    expect(appHint("cursor", "disconnect", "Restart Cursor to drop the server.")).toBe("Redémarrez Cursor pour retirer le serveur.")
  })

  it("falls back to Go's text for a client the catalog does not know, and to nothing without a hint", async () => {
    await i18n.changeLanguage("ar")
    expect(appHint("zed", "connect", "Restart Zed.")).toBe("Restart Zed.")
    expect(appHint("cursor", "connect", "")).toBe("")
  })
})
