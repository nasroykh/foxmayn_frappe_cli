// The state of one open conversation's live run. Pure: the screen feeds it the
// chat:* events and the stored conversation (the store is the truth; events
// only paint the run in progress).
import type { ChatApproval, ChatDone, ChatError, ChatTool, ConversationDetail, ToolStatus } from "@/lib/backend-types"

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
  approvals: ChatApproval[]
  error: ChatFailure | null
}

export type ChatAction =
  | { type: "select"; convID: string }
  | { type: "loaded"; detail: ConversationDetail }
  | { type: "sending"; text: string }
  | { type: "started"; runID: string }
  | { type: "sendFailed"; error: ChatFailure }
  | { type: "resumed" }
  | { type: "delta"; runID: string; text: string }
  | { type: "tool"; ev: ChatTool }
  | { type: "approval"; ev: ChatApproval }
  | { type: "approvalClosed"; runID: string; approvalID: string }
  | { type: "approvals"; list: ChatApproval[] }
  | { type: "done"; ev: ChatDone }
  | { type: "error"; ev: ChatError }

export function initialState(convID = ""): ChatState {
  return { convID, phase: "idle", runID: "", pending: "", text: "", tools: [], approvals: [], error: null }
}

/**
 * Whether an event belongs to the run on screen. While a send is in flight the
 * run id is not known yet, and the first event names it.
 */
function mine(s: ChatState, runID: string): boolean {
  if (!runID) return false
  if (s.phase === "starting" && s.runID === "") return true
  return (s.phase === "running" || s.phase === "starting") && s.runID === runID
}

/** True when the run belongs to the conversation on screen (events of other runs are ignored). */
export function ownsRun(s: ChatState, runID: string): boolean {
  return mine(s, runID) || (s.phase === "paused" && s.runID === runID)
}

function adopt(s: ChatState, runID: string): ChatState {
  return s.phase === "starting" && s.runID === "" ? { ...s, runID, phase: "running" } : s
}

export function chatReducer(s: ChatState, a: ChatAction): ChatState {
  switch (a.type) {
    case "select":
      return initialState(a.convID)
    case "loaded": {
      if (a.detail.conversation.id !== s.convID) return s
      // A send in flight is not overwritten by a late load.
      if (s.phase === "starting") return s
      const base = { ...s, pending: "", text: "", tools: [], approvals: [] }
      if (a.detail.activeRunID) return { ...base, phase: "running", runID: a.detail.activeRunID }
      if (a.detail.pausedRunID) return { ...base, phase: "paused", runID: a.detail.pausedRunID }
      return { ...base, phase: "idle", runID: "" }
    }
    case "sending":
      return { ...s, phase: "starting", runID: "", pending: a.text, text: "", tools: [], approvals: [], error: null }
    case "started":
      if (s.phase === "starting" && s.runID === "") return { ...s, phase: "running", runID: a.runID }
      return s
    case "sendFailed":
      return s.phase === "starting" ? { ...s, phase: "idle", runID: "", pending: "", error: a.error } : s
    case "resumed":
      return s.phase === "paused" ? { ...s, phase: "running", error: null } : s
    case "delta":
      return mine(s, a.runID) ? { ...adopt(s, a.runID), text: s.text + a.text } : s
    case "tool": {
      if (!mine(s, a.ev.runID)) return s
      const row: LiveTool = {
        callID: a.ev.callID,
        tool: a.ev.tool,
        site: a.ev.site,
        status: a.ev.status as ToolStatus,
        summary: a.ev.summary,
      }
      const i = s.tools.findIndex((t) => t.callID === row.callID)
      const tools = i < 0 ? [...s.tools, row] : s.tools.map((t, j) => (j === i ? row : t))
      return { ...adopt(s, a.ev.runID), tools }
    }
    case "approval": {
      if (!mine(s, a.ev.runID)) return s
      if (s.approvals.some((c) => c.approvalID === a.ev.approvalID)) return adopt(s, a.ev.runID)
      return { ...adopt(s, a.ev.runID), approvals: [...s.approvals, a.ev] }
    }
    case "approvalClosed":
      if (s.runID !== a.runID) return s
      return { ...s, approvals: s.approvals.filter((c) => c.approvalID !== a.approvalID) }
    case "approvals": {
      // Cards from before a reload; ones already shown stay as they are.
      const known = new Set(s.approvals.map((c) => c.approvalID))
      const add = a.list.filter((c) => !known.has(c.approvalID))
      return add.length ? { ...s, approvals: [...s.approvals, ...add] } : s
    }
    case "done": {
      if (!ownsRun(s, a.ev.runID)) return s
      if (a.ev.status === "paused") return { ...s, phase: "paused", runID: a.ev.runID, approvals: [] }
      return { ...s, phase: "idle", runID: "", approvals: [] }
    }
    case "error":
      return mine(s, a.ev.runID) ? { ...adopt(s, a.ev.runID), error: a.ev.error } : s
  }
}
