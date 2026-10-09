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
//
// History must be replayed byte-identical and append-only: the loop stores the
// parts of every assistant turn exactly as the stream produced them (Thinking
// parts included, in their original order) and never edits, reorders or drops
// one. With thinking on, the Anthropic API answers 400 when an earlier
// assistant turn's thinking blocks are changed or missing (the blocks are
// signed and bound to the conversation that produced them).
type Message struct {
	Role  Role
	Parts []Part
}

// Part is one of Text, ToolUse, ToolResult or Thinking.
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

// Thinking is one model reasoning block, kept opaque so it can be replayed
// unchanged. Text and Signature are a normal block (Text is empty when the
// provider omits it); Redacted marks a redacted block whose payload is Data.
// It is both a Part and an Event: the stream emits it when the block ends and
// the loop stores it in the assistant message at that position. It is never
// shown as assistant text.
type Thinking struct {
	Text      string
	Signature string
	Redacted  bool
	Data      string
}

func (Text) isPart()       {}
func (ToolUse) isPart()    {}
func (ToolResult) isPart() {}
func (Thinking) isPart()   {}

// Event is one of TextDelta, Thinking, ToolCall, Usage or Stop.
type Event interface{ isEvent() }

// TextDelta is a chunk of assistant text.
type TextDelta struct{ Text string }

// ToolCall is a complete tool call. Args is a valid JSON object, "{}" when
// empty. When the model's arguments did not parse as an object, ArgsError says
// why and Args holds the raw text if it is valid JSON, else "{}"; the loop must
// answer such a call with an error tool result and not run the tool.
type ToolCall struct {
	ID        string
	Name      string
	Args      json.RawMessage
	ArgsError string
}

// Usage reports token counts for the turn. In counts input tokens read fresh
// (cache writes included); Cached counts those served from the prompt cache.
type Usage struct{ In, Out, Cached int }

// Stop reasons. Other providers map their own to these where they can.
const (
	StopEndTurn   = "end_turn"
	StopToolUse   = "tool_use"
	StopMaxTokens = "max_tokens"
	// StopPauseTurn means the provider paused a long turn: the loop continues
	// it by sending the history, with this turn's assistant message appended
	// unchanged, again.
	StopPauseTurn = "pause_turn"
	StopRefusal   = "refusal"
)

// Stop ends the turn. Reason is one of the Stop* constants, or the provider's
// own value. Category names the policy area of a StopRefusal when the provider
// gives one ("cyber", "bio", ...), else it is empty.
//
// Message is the authoritative history entry for the turn: the assistant
// message with its parts in the exact order the model produced them (Thinking,
// Text, ToolUse), built by the provider from the finished blocks. Blocks cut
// off by max_tokens are not in it. The loop appends Message to the history
// as-is and uses TextDelta only for display. ToolCall events arrive just before
// Usage and Stop, in the same order as the ToolUse parts. Message has no parts
// when the turn produced nothing.
type Stop struct {
	Reason   string
	Category string
	Message  Message
}

func (TextDelta) isEvent() {}
func (Thinking) isEvent()  {}
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

// IsRetryable reports whether err is worth retrying later: 429, any 5xx
// (529 overloaded included).
func IsRetryable(err error) bool {
	var e *APIError
	return errors.As(err, &e) && (e.Status == 429 || e.Status >= 500)
}

// IsRateLimit reports whether err is a 429 from the provider.
func IsRateLimit(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == 429
}
