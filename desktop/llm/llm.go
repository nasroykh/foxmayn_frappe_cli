// Package llm defines the provider-neutral types the assistant loop speaks:
// a streaming Provider, the conversation parts it sends and the events it
// receives. Adapters live in subpackages; llmtest holds a scripted fake.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Provider streams one model turn and lists the models it offers.
type Provider interface {
	Stream(ctx context.Context, req Request) (Stream, error)
	Models(ctx context.Context) ([]Model, error)
}

// Stream yields the events of one model turn. Next returns io.EOF after the
// last event. Close releases the connection and is safe to call twice.
type Stream interface {
	Next() (Event, error)
	Close() error
}

// Model is one entry of a provider's model list.
type Model struct {
	ID    string
	Label string
}

// Request is one model turn: the full history plus the tools on offer.
type Request struct {
	Model     string
	System    string
	Messages  []Message
	Tools     []Tool
	MaxTokens int
}

// Tool describes one callable tool; InputSchema is a JSON Schema object.
type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// Role is the author of a Message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one history entry made of ordered parts.
type Message struct {
	Role  Role
	Parts []Part
}

// Part is one of Text, ToolUse or ToolResult.
type Part interface{ isPart() }

// Text is plain text.
type Text struct{ Text string }

// ToolUse is a tool call the assistant made; Args is a JSON object.
type ToolUse struct {
	ID   string
	Name string
	Args json.RawMessage
}

// ToolResult answers a ToolUse (user role).
type ToolResult struct {
	ID      string
	Text    string
	IsError bool
}

func (Text) isPart()       {}
func (ToolUse) isPart()    {}
func (ToolResult) isPart() {}

// Event is one of TextDelta, ToolCall, Usage or Stop.
type Event interface{ isEvent() }

// TextDelta is a chunk of assistant text.
type TextDelta struct{ Text string }

// ToolCall is a complete tool call: Args is a valid JSON object, "{}" when empty.
type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}

// Usage reports token counts for the turn. In counts input tokens read fresh
// (cache writes included); Cached counts those served from the prompt cache.
type Usage struct{ In, Out, Cached int }

// Stop ends the turn; Reason is the provider's stop reason (for example
// "end_turn", "tool_use" or "max_tokens").
type Stop struct{ Reason string }

func (TextDelta) isEvent() {}
func (ToolCall) isEvent()  {}
func (Usage) isEvent()     {}
func (Stop) isEvent()      {}

// APIError is a provider failure with an HTTP status and a short message that
// never contains the API key. Status is 0 for transport failures.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Status == 0 {
		return e.Message
	}
	return fmt.Sprintf("provider error (HTTP %d): %s", e.Status, e.Message)
}

// IsAuth reports whether err is a 401 or 403 from the provider.
func IsAuth(err error) bool {
	var e *APIError
	return errors.As(err, &e) && (e.Status == 401 || e.Status == 403)
}

// IsRateLimit reports whether err is a 429 from the provider.
func IsRateLimit(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == 429
}
