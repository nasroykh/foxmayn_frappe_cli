import { describe, expect, it } from "vitest"

import type { ChatApproval, ChatDone, ChatError, ChatTool } from "@/lib/backend-types"
import { backend } from "@/mock/backend"

// The scripted assistant of the mock backend, which the chat screens are
// built against.

async function newConv(mode: "read" | "ask") {
  return backend.newConversation("acme-prod", mode, "anthropic", "")
}

function collect() {
  const cards: ChatApproval[] = []
  const tools: ChatTool[] = []
  const errors: ChatError[] = []
  const dones: ChatDone[] = []
  let text = ""
  const off = [
    backend.onChatApproval((a) => cards.push(a)),
    backend.onChatTool((t) => tools.push(t)),
    backend.onChatError((e) => errors.push(e)),
    backend.onChatDone((d) => dones.push(d)),
    backend.onChatDelta((d) => (text += d.text)),
  ]
  return {
    cards,
    tools,
    errors,
    dones,
    text: () => text,
    stop: () => off.forEach((f) => f()),
    until: async (ok: () => boolean) => {
      for (let i = 0; i < 400 && !ok(); i++) await new Promise((r) => setTimeout(r, 25))
      expect(ok()).toBe(true)
    },
  }
}

describe("mock assistant", () => {
  it("answers a read question with one tool call", async () => {
    const c = await newConv("read")
    const ev = collect()
    const runID = await backend.sendMessage(c.id, "What is TD-0001?")
    await ev.until(() => ev.dones.length === 1)
    ev.stop()
    expect(ev.dones[0]).toMatchObject({ runID, status: "done" })
    expect(ev.tools.map((t) => t.status)).toEqual(["running", "ok"])
    expect(ev.text()).toContain("TD-0001")
    const d = await backend.getConversation(c.id)
    expect(d.messages.map((m) => m.role)).toEqual(["user", "assistant"])
    expect(d.messages[1].tools[0]).toMatchObject({ tool: "get_doc", status: "ok" })
    expect(d.conversation.title).toBe("What is TD-0001?")
  })

  it("shows an app card with a diff and applies the answer", async () => {
    const c = await newConv("ask")
    const ev = collect()
    await backend.sendMessage(c.id, "update the todo")
    await ev.until(() => ev.cards.length === 1)
    const card = ev.cards[0]
    expect(card.kind).toBe("app")
    expect(card.diff.map((f) => f.field)).toEqual(["description", "status"])
    expect(card.args).toMatchObject({ doctype: "ToDo", name: "TD-0001" })
    expect(await backend.pendingApprovals(c.id)).toHaveLength(1)
    const other = await newConv("ask")
    await expect(backend.answerApproval(other.id, card.approvalID, true)).rejects.toMatchObject({
      cause: { code: "not_found" },
    })
    await expect(backend.setConversationMode(c.id, "read")).rejects.toMatchObject({ cause: { code: "invalid" } })
    await backend.answerApproval(c.id, card.approvalID, true)
    await ev.until(() => ev.dones.length === 1)
    ev.stop()
    expect(ev.dones[0].status).toBe("done")
    const d = await backend.getConversation(c.id)
    expect(d.messages[1].tools[d.messages[1].tools.length - 1]).toMatchObject({ tool: "update_doc", approval: "approved", status: "ok" })
    expect(await backend.pendingApprovals(c.id)).toHaveLength(0)
  })

  it("asks ffc's own question for a delete and takes a decline", async () => {
    const c = await newConv("ask")
    const ev = collect()
    await backend.sendMessage(c.id, "delete the todo")
    await ev.until(() => ev.cards.length === 1)
    expect(ev.cards[0].kind).toBe("ffc")
    expect(ev.cards[0].message).toContain("TD-0001")
    await backend.answerApproval(c.id, ev.cards[0].approvalID, false)
    await ev.until(() => ev.dones.length === 1)
    ev.stop()
    const d = await backend.getConversation(c.id)
    expect(d.messages[1].tools[0]).toMatchObject({ tool: "delete_doc", approval: "ffc-declined", status: "error" })
  })

  it("reports no field changes and stops on cancel", async () => {
    const c = await newConv("ask")
    const ev = collect()
    const runID = await backend.sendMessage(c.id, "update with the same value")
    await ev.until(() => ev.cards.length === 1)
    expect(ev.cards[0].noChanges).toBe(true)
    expect(ev.cards[0].diff).toEqual([])
    await backend.cancelRun(runID)
    await ev.until(() => ev.dones.length === 1)
    ev.stop()
    expect(ev.dones[0].status).toBe("cancelled")
  })

  it("has no write tools in read mode", async () => {
    const c = await newConv("read")
    const ev = collect()
    await backend.sendMessage(c.id, "delete the todo")
    await ev.until(() => ev.dones.length === 1)
    ev.stop()
    expect(ev.cards).toHaveLength(0)
    expect(ev.text()).toContain("only read")
  })

  it("fails with a chat:error and pauses at the step limit", async () => {
    const c = await newConv("read")
    const ev = collect()
    await backend.sendMessage(c.id, "this will fail")
    await ev.until(() => ev.dones.length === 1)
    expect(ev.errors[0].error?.code).toBe("failed")
    expect(ev.dones[0].status).toBe("error")

    const p = await newConv("read")
    const runID = await backend.sendMessage(p.id, "check many todos")
    await ev.until(() => ev.dones.length === 2)
    expect(ev.dones[1]).toMatchObject({ runID, status: "paused" })
    expect((await backend.getConversation(p.id)).pausedRunID).toBe(runID)
    await backend.continueRun(runID)
    await ev.until(() => ev.dones.length === 3)
    ev.stop()
    expect(ev.dones[2].status).toBe("done")
  })

  it("rejects a bad key and never returns one", async () => {
    await expect(backend.setKey("anthropic", "bad-key")).rejects.toMatchObject({ cause: { code: "auth" } })
    await backend.setKey("anthropic", "mock-key-WXYZ-0123")
    const list = await backend.listProviders()
    expect(JSON.stringify(list)).not.toContain("mock-key")
    expect(list[0]).toMatchObject({ keySet: true, keyLast4: "0123" })
    expect((await backend.listModels("anthropic")).find((m) => m.default)?.id).toBe("claude-sonnet-5-5")
  })
})
