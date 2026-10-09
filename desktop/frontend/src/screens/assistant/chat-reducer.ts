// The state of one open conversation's live run. Pure: the screen feeds it the
// chat:* events and the stored conversation (the store is the truth; events
// only paint the run in progress).
import type { ChatApproval, ChatDone, ChatError, ChatTool, ChatUsage, ConversationDetail, ToolStatus, UsageTotals } from "@/lib/backend-types"
import { addEvent, noUsage } from "@/lib/cost"

/** What the alert shows: chat:error's error, or a failed send. */
export interface ChatFailure {
  code: string
  message: string
  detail?: string
}

/** idle: nothing running. starting: sent, run id not known yet. paused: waits for Continue. */
export type Phase = "idle" | "starting" | "running" | "paused"

export interface LiveTool {
  callID: string
  tool: string
  site: string
  status: ToolStatus
  summary: string
}

export interface ChatState {
  convID: string
  phase: Phase
  /** The active or paused run. "" when none, or while starting. */
  runID: string
  /** The user's message, shown until the store returns it. */
  pending: string
  /** The assistant text streamed so far in this run. */
  text: string
  tools: LiveTool[]
  /** What the model turns of this run used so far (chat:usage). */
  usage: UsageTotals
  approvals: ChatApproval[]
  error: ChatFailure | null
  /** This conversation's events that came before sendMessage answered with the run id. */
  buffer: LiveAction[]
}

/** The chat:* events. Each names its conversation and its run. */
export type LiveAction =
  | { type: "delta"; convID: string; runID: string; text: string }
  | { type: "tool"; ev: ChatTool }
  | { type: "usage"; ev: ChatUsage }
  | { type: "approval"; ev: ChatApproval }
  | { type: "approvalClosed"; convID: string; runID: string; approvalID: string }
  | { type: "done"; ev: ChatDone }
  | { type: "error"; ev: ChatError }

export type ChatAction =
  | { type: "select"; convID: string }
  | { type: "loaded"; detail: ConversationDetail }
  | { type: "sending"; text: string }
  | { type: "started"; runID: string }
  | { type: "sendFailed"; error: ChatFailure }
  | { type: "resumed" }
  | { type: "approvals"; list: ChatApproval[] }
  | LiveAction

export function initialState(convID = ""): ChatState {
  return { convID, phase: "idle", runID: "", pending: "", text: "", tools: [], usage: noUsage, approvals: [], error: null, buffer: [] }
}

function idsOf(a: LiveAction): { convID: string; runID: string } {
  switch (a.type) {
    case "delta":
    case "approvalClosed":
      return { convID: a.convID, runID: a.runID }
    default:
      return { convID: a.ev.convID, runID: a.ev.runID }
  }
}

/**
 * True when the run is the one on screen: this conversation's, and bound by
 * sendMessage's answer or by the store. A run id is never taken from an event.
 */
export function ownsRun(s: ChatState, convID: string, runID: string): boolean {
  if (!runID || convID !== s.convID || runID !== s.runID) return false
  return s.phase === "running" || s.phase === "paused"
}

function live(s: ChatState, a: LiveAction): ChatState {
  const { runID } = idsOf(a)
  if (!ownsRun(s, a.type === "delta" || a.type === "approvalClosed" ? a.convID : a.ev.convID, runID)) return s
  const running = s.phase === "running"
  switch (a.type) {
    case "delta":
      return running ? { ...s, text: s.text + a.text } : s
    case "tool": {
      if (!running) return s
      const row: LiveTool = {
        callID: a.ev.callID,
        tool: a.ev.tool,
        site: a.ev.site,
        status: a.ev.status as ToolStatus,
        summary: a.ev.summary,
      }
      const i = s.tools.findIndex((t) => t.callID === row.callID)
      const tools = i < 0 ? [...s.tools, row] : s.tools.map((t, j) => (j === i ? row : t))
      return { ...s, tools }
    }
    case "usage":
      return running ? { ...s, usage: addEvent(s.usage, a.ev) } : s
    case "approval":
      if (!running || s.approvals.some((c) => c.approvalID === a.ev.approvalID)) return s
      return { ...s, approvals: [...s.approvals, a.ev] }
    case "approvalClosed":
      return { ...s, approvals: s.approvals.filter((c) => c.approvalID !== a.approvalID) }
    case "done":
      if (a.ev.status === "paused") return { ...s, phase: "paused", approvals: [] }
      return { ...s, phase: "idle", runID: "", approvals: [] }
    case "error":
      return running ? { ...s, error: a.ev.error } : s
  }
}

export function chatReducer(s: ChatState, a: ChatAction): ChatState {
  switch (a.type) {
    case "select":
      return initialState(a.convID)
    case "loaded": {
      if (a.detail.conversation.id !== s.convID) return s
      // A send in flight is not overwritten by a late load.
      if (s.phase === "starting") return s
      const base = { ...s, pending: "", text: "", tools: [], usage: noUsage, approvals: [], buffer: [] }
      if (a.detail.activeRunID) return { ...base, phase: "running", runID: a.detail.activeRunID }
      if (a.detail.pausedRunID) return { ...base, phase: "paused", runID: a.detail.pausedRunID }
      return { ...base, phase: "idle", runID: "" }
    }
    case "sending":
      return { ...s, phase: "starting", runID: "", pending: a.text, text: "", tools: [], usage: noUsage, approvals: [], error: null, buffer: [] }
    case "started": {
      if (s.phase !== "starting" || s.runID !== "") return s
      // Bound by sendMessage's answer; replay what this conversation's run said meanwhile.
      const buffered = s.buffer.filter((e) => idsOf(e).runID === a.runID)
      return buffered.reduce(live, { ...s, phase: "running", runID: a.runID, buffer: [] })
    }
    case "sendFailed":
      return s.phase === "starting" ? { ...s, phase: "idle", runID: "", pending: "", buffer: [], error: a.error } : s
    case "resumed":
      return s.phase === "paused" ? { ...s, phase: "running", error: null } : s
    case "approvals": {
      // Cards from before a reload; ones already shown stay as they are.
      const known = new Set(s.approvals.map((c) => c.approvalID))
      const add = a.list.filter((c) => c.convID === s.convID && !known.has(c.approvalID))
      return add.length ? { ...s, approvals: [...s.approvals, ...add] } : s
    }
    default: {
      // A chat event: only this conversation's; before the run id is known, keep it for later.
      if (idsOf(a).convID !== s.convID) return s
      if (s.phase === "starting" && s.runID === "") return { ...s, buffer: [...s.buffer, a] }
      return live(s, a)
    }
  }
}
