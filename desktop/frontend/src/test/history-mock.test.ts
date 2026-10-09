import { beforeEach, describe, expect, it } from "vitest"

import { backend } from "@/mock/backend"

// The mock must behave like the Go service for the calls the history UI makes.

const filter = { site: "", profileID: "", from: "", to: "", archived: false }

beforeEach(async () => {
  for (const c of await backend.listConversations()) await backend.deleteConversation(c.id)
})

describe("mock history", () => {
  it("pins and archives, and lists the marks", async () => {
    const a = await backend.newConversation("acme-prod", "read", "anthropic", "")
    const b = await backend.newConversation("acme-prod", "read", "anthropic", "")
    await backend.pinConversation(a.id, true)
    await backend.archiveConversation(b.id, true)
    const list = await backend.listConversations()
    expect(list.find((c) => c.id === a.id)?.pinned).toBe(true)
    expect(list.find((c) => c.id === b.id)?.archived).toBe(true)
    await backend.pinConversation(a.id, false)
    expect((await backend.listConversations()).find((c) => c.id === a.id)?.pinned).toBe(false)
    await expect(backend.pinConversation("nope", true)).rejects.toThrow()
  })

  it("finds nothing in an empty history and ignores an empty query", async () => {
    await backend.newConversation("acme-prod", "read", "anthropic", "")
    expect(await backend.search("anything", filter, 10)).toEqual([])
    expect(await backend.search("   ", filter, 10)).toEqual([])
  })

  it("keeps the retention period and refuses other values", async () => {
    expect(await backend.getRetention()).toBe(0)
    await backend.setRetention(90)
    expect(await backend.getRetention()).toBe(90)
    await expect(backend.setRetention(7 as 30)).rejects.toThrow()
    await backend.setRetention(0)
  })

  it("exports to a path named after the title and imports a new conversation", async () => {
    const a = await backend.newConversation("acme-prod", "read", "anthropic", "")
    expect(await backend.exportConversation(a.id, "md")).toMatch(/conversation\.md$/)
    const res = await backend.importConversation()
    expect(res.cancelled).toBe(false)
    expect(res.conversation.mode).toBe("read")
    expect((await backend.listConversations()).some((c) => c.id === res.conversation.id)).toBe(true)
  })
})
