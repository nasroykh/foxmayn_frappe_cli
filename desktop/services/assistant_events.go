package services

import "encoding/json"

// Assistant event names. The payload types below are not registered with
// Wails yet; the service that binds them does that.
const (
	// EventChatDelta carries a batch of assistant text (about every 40 ms).
	EventChatDelta = "chat:delta"
	// EventChatTool reports a tool call starting and its outcome.
	EventChatTool = "chat:tool"
	// EventChatApproval asks the user to approve one change.
	EventChatApproval = "chat:approval"
	// EventChatApprovalClosed tells the UI an approval card is settled.
	EventChatApprovalClosed = "chat:approval-closed"
	// EventChatUsage reports the token counts of one model turn.
	EventChatUsage = "chat:usage"
	// EventChatDone ends a run: done, paused, cancelled, or error after a
	// chat:error.
	EventChatDone = "chat:done"
	// EventChatError reports why a run failed.
	EventChatError = "chat:error"
)

// Tool call statuses in ChatTool and the store.
const (
	ToolRunning = "running"
	ToolOK      = "ok"
	ToolError   = "error"
	ToolStopped = "stopped"
)

// Run statuses in ChatDone and the store.
const (
	RunRunning   = "running"
	RunDone      = "done"
	RunPaused    = "paused"
	RunCancelled = "cancelled"
	RunError     = "error"
)

// ChatDelta is the payload of EventChatDelta.
type ChatDelta struct {
	RunID string `json:"runID"`
	Text  string `json:"text"`
}

// ChatTool is the payload of EventChatTool.
type ChatTool struct {
	RunID   string `json:"runID"`
	CallID  string `json:"callID"`
	Tool    string `json:"tool"`
	Site    string `json:"site"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

// ChatUsage is the payload of EventChatUsage.
type ChatUsage struct {
	RunID  string `json:"runID"`
	Turn   int    `json:"turn"`
	Input  int    `json:"input"`
	Output int    `json:"output"`
	Cached int    `json:"cached"`
}

// ChatDone is the payload of EventChatDone.
type ChatDone struct {
	RunID  string `json:"runID"`
	Status string `json:"status"`
	// StopReason is set when the model ended the run for a reason the user
	// should hear of: "max_tokens" or "refusal".
	StopReason string `json:"stopReason,omitempty"`
	// Category is the policy area of a refusal, when the provider gave one.
	Category string `json:"category,omitempty"`
}

// ChatError is the payload of EventChatError.
type ChatError struct {
	RunID string `json:"runID"`
	Error *Error `json:"error"`
}

// Approval kinds in ChatApproval: the app's own card or ffc's question.
const (
	ApprovalApp = "app"
	ApprovalFFC = "ffc"
)

// Approval outcomes in ChatApprovalClosed and the store (tool_calls.approval).
const (
	ApprovalApproved    = "approved"
	ApprovalDeclined    = "declined"
	ApprovalCancelled   = "cancelled"
	ApprovalFFCApproved = "ffc-approved"
	ApprovalFFCDeclined = "ffc-declined"
)

// DiffField is one changed field of an update_doc card.
type DiffField struct {
	Field string `json:"field"`
	Old   any    `json:"old"`
	New   any    `json:"new"`
}

// ChatApproval is the payload of EventChatApproval and an item of the list
// the UI reads to show open cards again after a reload.
type ChatApproval struct {
	RunID      string `json:"runID"`
	ApprovalID string `json:"approvalID"`
	// Kind is "app" (the app's card) or "ffc" (ffc's own confirmation).
	Kind     string   `json:"kind"`
	Tool     string   `json:"tool"`
	Site     string   `json:"site"`
	Doctypes []string `json:"doctypes,omitempty"`
	Names    []string `json:"names,omitempty"`
	// Args is the exact JSON the call will run with.
	Args json.RawMessage `json:"args"`
	// Diff lists the changed fields of an update_doc.
	Diff []DiffField `json:"diff,omitempty"`
	// Message is ffc's question, for kind "ffc".
	Message string `json:"message,omitempty"`
}

// ChatApprovalClosed is the payload of EventChatApprovalClosed. Outcome is
// "approved", "declined" or "cancelled".
type ChatApprovalClosed struct {
	RunID      string `json:"runID"`
	ApprovalID string `json:"approvalID"`
	Outcome    string `json:"outcome"`
}
