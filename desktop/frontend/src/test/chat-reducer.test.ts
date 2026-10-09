import { describe, expect, it } from "vitest"

import type { ChatApproval, ChatUsage, ConversationDetail } from "@/lib/backend-types"
import { chatReducer, initialState, ownsRun, type ChatAction, type ChatState } from "@/screens/assistant/chat-reducer"

function run(actions: ChatAction[], from: ChatState = initialState("c1")) {
  return actions.reduce(chatReducer, from)
}

const card = (over: Partial<ChatApproval> = {}): ChatApproval => ({
  convID: "c1",
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
    conversation: { id: "c1", title: "t", site: "acme", mode: "read", providerID: "p", model: "m", profileID: "", created: "", updated: "" },
    messages: [],
    activeRunID: "",
    pausedRunID: "",
    runUsage: [],
    total: { input: 0, output: 0, cached: 0, cacheWrite: 0, costUSD: 0, hasCost: false, unknown: false },
    ...over,
  }
}

const started = (runID = "r1") => run([{ type: "sending", text: "hi" }, { type: "started", runID }])

describe("chat reducer", () => {
  it("appends deltas of the active run", () => {
    const s = run(
      [
        { type: "delta", convID: "c1", runID: "r1", text: "Hel" },
        { type: "delta", convID: "c1", runID: "r1", text: "lo" },
      ],
      started(),
    )
    expect(s.text).toBe("Hello")
    expect(s.phase).toBe("running")
  })

  it("binds the run on sendMessage's answer and replays this conversation's early events", () => {
    const s = run([
      { type: "sending", text: "hi" },
      { type: "delta", convID: "c1", runID: "r9", text: "x" },
      { type: "delta", convID: "c1", runID: "r9", text: "y" },
    ])
    expect(s).toMatchObject({ phase: "starting", runID: "", text: "" })
    const bound = chatReducer(s, { type: "started", runID: "r9" })
    expect(bound).toMatchObject({ runID: "r9", phase: "running", text: "xy", pending: "hi", buffer: [] })
  })

  it("never takes a run id from another conversation's events during starting", () => {
    const s = run([
      { type: "sending", text: "hi" },
      { type: "delta", convID: "c2", runID: "r-other", text: "no" },
      { type: "tool", ev: { convID: "c2", runID: "r-other", callID: "k", tool: "t", site: "", status: "running", summary: "" } },
      { type: "approval", ev: card({ convID: "c2", runID: "r-other" }) },
      { type: "done", ev: { convID: "c2", runID: "r-other", status: "done" } },
      { type: "started", runID: "r1" },
      { type: "delta", convID: "c2", runID: "r-other", text: "no" },
    ])
    expect(s).toMatchObject({ phase: "running", runID: "r1", text: "", tools: [], approvals: [] })
    expect(ownsRun(s, "c2", "r-other")).toBe(false)
    expect(ownsRun(s, "c2", "r1")).toBe(false)
  })

  it("drops buffered events of a run other than the one sendMessage returned", () => {
    const s = run([
      { type: "sending", text: "hi" },
      { type: "delta", convID: "c1", runID: "stale", text: "no" },
      { type: "started", runID: "r1" },
    ])
    expect(s.text).toBe("")
  })

  it("a run that ended before its id came back ends on started", () => {
    const s = run([
      { type: "sending", text: "hi" },
      { type: "delta", convID: "c1", runID: "r1", text: "ok" },
      { type: "done", ev: { convID: "c1", runID: "r1", status: "done" } },
      { type: "started", runID: "r1" },
    ])
    expect(s).toMatchObject({ phase: "idle", runID: "", text: "ok" })
  })

  it("tracks a tool call from running to ok", () => {
    const ev = { convID: "c1", runID: "r1", callID: "k1", tool: "get_doc", site: "acme", status: "running", summary: "ToDo" }
    const s = run([{ type: "tool", ev }, { type: "tool", ev: { ...ev, status: "ok" } }], started())
    expect(s.tools).toEqual([{ callID: "k1", tool: "get_doc", site: "acme", status: "ok", summary: "ToDo" }])
  })

  it("opens and closes approval cards", () => {
    let s = run([{ type: "approval", ev: card() }, { type: "approval", ev: card() }], started())
    expect(s.approvals).toHaveLength(1)
    s = chatReducer(s, { type: "approvalClosed", convID: "c1", runID: "r1", approvalID: "a1" })
    expect(s.approvals).toHaveLength(0)
  })

  it("re-shows open cards after a reload without duplicating", () => {
    let s = run([{ type: "approval", ev: card() }], started())
    s = chatReducer(s, { type: "approvals", list: [card(), card({ approvalID: "a2" })] })
    expect(s.approvals.map((c) => c.approvalID)).toEqual(["a1", "a2"])
  })

  it("ends on done and lets the stored conversation replace the live view", () => {
    let s = run([{ type: "delta", convID: "c1", runID: "r1", text: "ok" }, { type: "done", ev: { convID: "c1", runID: "r1", status: "done" } }], started())
    expect(s.phase).toBe("idle")
    s = chatReducer(s, { type: "loaded", detail: detail() })
    expect(s).toMatchObject({ text: "", pending: "", tools: [], phase: "idle" })
  })

  it("pauses on done(paused) and resumes", () => {
    let s = run([{ type: "done", ev: { convID: "c1", runID: "r1", status: "paused" } }], started())
    expect(s).toMatchObject({ phase: "paused", runID: "r1" })
    s = chatReducer(s, { type: "resumed" })
    expect(s.phase).toBe("running")
  })

  it("keeps the error through the reload that follows", () => {
    let s = run(
      [
        { type: "error", ev: { convID: "c1", runID: "r1", error: { code: "failed", message: "boom" } } },
        { type: "done", ev: { convID: "c1", runID: "r1", status: "error" } },
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
        { type: "delta", convID: "c1", runID: "other", text: "x" },
        { type: "tool", ev: { convID: "c1", runID: "other", callID: "k", tool: "t", site: "", status: "running", summary: "" } },
        { type: "approval", ev: card({ runID: "other" }) },
        { type: "approvalClosed", convID: "c1", runID: "other", approvalID: "a1" },
        { type: "error", ev: { convID: "c1", runID: "other", error: { code: "failed", message: "no" } } },
        { type: "done", ev: { convID: "c1", runID: "other", status: "done" } },
      ],
      s0,
    )
    expect(s).toEqual(s0)
    expect(ownsRun(s, "c1", "other")).toBe(false)
    expect(ownsRun(s, "c1", "r1")).toBe(true)
  })

  it("ignores events when idle, and a load for another conversation", () => {
    const idle = initialState("c1")
    expect(chatReducer(idle, { type: "delta", convID: "c1", runID: "r1", text: "x" })).toBe(idle)
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

  it("adds up the usage of the run, and an unknown turn makes it unknown", () => {
    const ev = (over: Partial<ChatUsage>): ChatAction => ({
      type: "usage",
      ev: { convID: "c1", runID: "r1", turn: 1, input: 100, output: 10, cached: 5, cacheWrite: 0, cost: 0.01, costSource: "table", ...over },
    })
    let s = run([ev({}), ev({ turn: 2, cost: 0.02 })], started())
    expect(s.usage).toMatchObject({ input: 200, output: 20, cached: 10, hasCost: true, unknown: false })
    expect(s.usage.costUSD).toBeCloseTo(0.03)
    s = run([ev({ turn: 3, cost: null, costSource: "" })], s)
    expect(s.usage).toMatchObject({ hasCost: true, unknown: true })
    // A local model has tokens only: no cost, and nothing unknown about it.
    s = run([ev({ cost: null, costSource: "local" })], started())
    expect(s.usage).toMatchObject({ input: 100, hasCost: false, unknown: false })
    // Another run's usage, and a load, leave or clear it.
    expect(run([ev({ runID: "r9" })], started()).usage.input).toBe(0)
    expect(chatReducer(s, { type: "loaded", detail: detail() }).usage.input).toBe(0)
  })

  it("a failed send goes back to idle with the error", () => {
    const s = run([
      { type: "sending", text: "hi" },
      { type: "sendFailed", error: { code: "invalid", message: "busy" } },
    ])
    expect(s).toMatchObject({ phase: "idle", pending: "", error: { message: "busy" } })
  })
})
