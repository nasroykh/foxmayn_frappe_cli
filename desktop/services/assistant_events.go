package services

// Assistant event names. The payload types below are not registered with
// Wails yet; the service that binds them does that.
const (
	// EventChatDelta carries a batch of assistant text (about every 40 ms).
	EventChatDelta = "chat:delta"
	// EventChatTool reports a tool call starting and its outcome.
	EventChatTool = "chat:tool"
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
