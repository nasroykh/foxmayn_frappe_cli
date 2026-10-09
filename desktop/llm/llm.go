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
	// Images reads the bytes of the Image parts in Messages. It may be nil
	// when the history has none; an adapter refuses a request with an Image
	// part and no resolver.
	Images ImageResolver
}

// ImageResolver returns the bytes of an attachment named by an Image part.
// The loop provides it; the store keeps the bytes, the history only the id.
type ImageResolver interface {
	ImageData(ctx context.Context, attachmentID string) ([]byte, error)
}

// ImageBytes reads the bytes of img through r, for an adapter building a
// request. It fails when r is nil, the media type is not one the providers
// take, or the attachment is empty or cannot be read.
func ImageBytes(ctx context.Context, r ImageResolver, img Image) ([]byte, error) {
	switch img.MediaType {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
	default:
		return nil, fmt.Errorf("image %s: unsupported type %q", img.AttachmentID, img.MediaType)
	}
	if r == nil {
		return nil, fmt.Errorf("image %s: no attachment store for this request", img.AttachmentID)
	}
	b, err := r.ImageData(ctx, img.AttachmentID)
	if err != nil {
		return nil, fmt.Errorf("image %s: %w", img.AttachmentID, err)
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("image %s: empty attachment", img.AttachmentID)
	}
	return b, nil
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

// Part is one of Text, ToolUse, ToolResult, Thinking or Image.
type Part interface{ isPart() }

// Image is a picture the user attached (user role). The bytes stay in the
// store; Request.Images resolves AttachmentID when the request is built.
// MediaType is image/png, image/jpeg, image/webp or image/gif.
type Image struct {
	AttachmentID string
	MediaType    string
}

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
//
// Provider names the adapter that produced the block (ProviderAnthropic,
// ProviderOpenAI, ProviderGemini). The payload means something only to that
// provider, so every adapter skips the Thinking of another one when it
// replays the history; a conversation can then switch providers between
// runs. An empty Provider is a block stored by 0.2.0, where only Anthropic
// produced Thinking: the Anthropic adapter replays it, the others skip it.
// What the fields hold per provider:
//   - Anthropic: Text and Signature, or Redacted with Data.
//   - OpenAI: Signature is the reasoning item id, Data its encrypted_content.
//   - Gemini: Signature is a thought signature (standard base64 of the bytes)
//     that belongs to the part right after it.
type Thinking struct {
	Provider  string
	Text      string
	Signature string
	Redacted  bool
	Data      string
}

// Thinking.Provider values.
const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
	ProviderGemini    = "gemini"
)

func (Text) isPart()       {}
func (ToolUse) isPart()    {}
func (ToolResult) isPart() {}
func (Thinking) isPart()   {}
func (Image) isPart()      {}

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
// (cache writes included); Cached counts those served from the prompt cache,
// so In + Cached is the whole prompt. CacheWrite is the part of In written to
// the cache (priced apart by some providers), 0 when the provider does not
// say. Out includes reasoning tokens. Cost is the price the provider reports
// for the turn in USD (OpenRouter's usage.cost), nil when the provider does
// not say; Anthropic leaves it nil.
type Usage struct {
	In, Out, Cached int
	CacheWrite      int
	Cost            *float64
}

// ForeignThinking reports whether an adapter for provider must skip t on
// replay: t was produced by another provider. An empty t.Provider counts as
// Anthropic (see Thinking).
func ForeignThinking(t Thinking, provider string) bool {
	p := t.Provider
	if p == "" {
		p = ProviderAnthropic
	}
	return p != provider
}

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
