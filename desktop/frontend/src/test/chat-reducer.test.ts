import { describe, expect, it } from "vitest"

import type { ChatApproval, ConversationDetail } from "@/lib/backend-types"
import { chatReducer, initialState, ownsRun, type ChatAction, type ChatState } from "@/screens/assistant/chat-reducer"

function run(actions: ChatAction[], from: ChatState = initialState("c1")) {
  return actions.reduce(chatReducer, from)
}

const card = (over: Partial<ChatApproval> = {}): ChatApproval => ({
  runID: "r1",
  approvalID: "a1",
  kind: "app",
  tool: "update_doc",
  site: "acme",
  doctypes: ["ToDo"],
  names: ["TD-1"],
  args: {},
  diff: [],
  noChanges: false,
  message: "",
  ...over,
})

function detail(over: Partial<ConversationDetail> = {}): ConversationDetail {
  return {
    conversation: { id: "c1", title: "t", site: "acme", mode: "read", providerID: "p", model: "m", created: "", updated: "" },
    messages: [],
    activeRunID: "",
    pausedRunID: "",
    ...over,
  }
}

const started = (runID = "r1") => run([{ type: "sending", text: "hi" }, { type: "started", runID }])

describe("chat reducer", () => {
  it("appends deltas of the active run", () => {
    const s = run(
      [
        { type: "delta", runID: "r1", text: "Hel" },
        { type: "delta", runID: "r1", text: "lo" },
      ],
      started(),
    )
    expect(s.text).toBe("Hello")
    expect(s.phase).toBe("running")
  })

  it("adopts the run id from the first event while the send is in flight", () => {
    const s = run([
      { type: "sending", text: "hi" },
      { type: "delta", runID: "r9", text: "x" },
      { type: "started", runID: "r9" },
    ])
    expect(s).toMatchObject({ runID: "r9", phase: "running", text: "x", pending: "hi" })
  })

  it("tracks a tool call from running to ok", () => {
    const ev = { runID: "r1", callID: "k1", tool: "get_doc", site: "acme", status: "running", summary: "ToDo" }
    const s = run([{ type: "tool", ev }, { type: "tool", ev: { ...ev, status: "ok" } }], started())
    expect(s.tools).toEqual([{ callID: "k1", tool: "get_doc", site: "acme", status: "ok", summary: "ToDo" }])
  })

  it("opens and closes approval cards", () => {
    let s = run([{ type: "approval", ev: card() }, { type: "approval", ev: card() }], started())
    expect(s.approvals).toHaveLength(1)
    s = chatReducer(s, { type: "approvalClosed", runID: "r1", approvalID: "a1" })
    expect(s.approvals).toHaveLength(0)
  })

  it("re-shows open cards after a reload without duplicating", () => {
    let s = run([{ type: "approval", ev: card() }], started())
    s = chatReducer(s, { type: "approvals", list: [card(), card({ approvalID: "a2" })] })
    expect(s.approvals.map((c) => c.approvalID)).toEqual(["a1", "a2"])
  })

  it("ends on done and lets the stored conversation replace the live view", () => {
    let s = run([{ type: "delta", runID: "r1", text: "ok" }, { type: "done", ev: { runID: "r1", status: "done" } }], started())
    expect(s.phase).toBe("idle")
    s = chatReducer(s, { type: "loaded", detail: detail() })
    expect(s).toMatchObject({ text: "", pending: "", tools: [], phase: "idle" })
  })

  it("pauses on done(paused) and resumes", () => {
    let s = run([{ type: "done", ev: { runID: "r1", status: "paused" } }], started())
    expect(s).toMatchObject({ phase: "paused", runID: "r1" })
    s = chatReducer(s, { type: "resumed" })
    expect(s.phase).toBe("running")
  })

  it("keeps the error through the reload that follows", () => {
    let s = run(
      [
        { type: "error", ev: { runID: "r1", error: { code: "failed", message: "boom" } } },
        { type: "done", ev: { runID: "r1", status: "error" } },
        { type: "loaded", detail: detail() },
      ],
      started(),
    )
    expect(s.error).toMatchObject({ message: "boom" })
    s = chatReducer(s, { type: "sending", text: "again" })
    expect(s.error).toBeNull()
  })

  it("ignores events of another run", () => {
    const s0 = started("r1")
    const s = run(
      [
        { type: "delta", runID: "other", text: "x" },
        { type: "tool", ev: { runID: "other", callID: "k", tool: "t", site: "", status: "running", summary: "" } },
        { type: "approval", ev: card({ runID: "other" }) },
        { type: "approvalClosed", runID: "other", approvalID: "a1" },
        { type: "error", ev: { runID: "other", error: { code: "failed", message: "no" } } },
        { type: "done", ev: { runID: "other", status: "done" } },
      ],
      s0,
    )
    expect(s).toEqual(s0)
    expect(ownsRun(s, "other")).toBe(false)
    expect(ownsRun(s, "r1")).toBe(true)
  })

  it("ignores events when idle, and a load for another conversation", () => {
    const idle = initialState("c1")
    expect(chatReducer(idle, { type: "delta", runID: "r1", text: "x" })).toBe(idle)
    const other = detail({ conversation: { ...detail().conversation, id: "c2" } })
    expect(chatReducer(idle, { type: "loaded", detail: other })).toBe(idle)
  })

  it("restores an active or paused run from the store", () => {
    expect(chatReducer(initialState("c1"), { type: "loaded", detail: detail({ activeRunID: "r2" }) })).toMatchObject({
      phase: "running",
      runID: "r2",
    })
    expect(chatReducer(initialState("c1"), { type: "loaded", detail: detail({ pausedRunID: "r3" }) })).toMatchObject({
      phase: "paused",
      runID: "r3",
    })
  })

  it("a failed send goes back to idle with the error", () => {
    const s = run([
      { type: "sending", text: "hi" },
      { type: "sendFailed", error: { code: "invalid", message: "busy" } },
    ])
    expect(s).toMatchObject({ phase: "idle", pending: "", error: { message: "busy" } })
  })
})
