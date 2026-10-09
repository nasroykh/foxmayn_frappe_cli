package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// partJSON is the stored shape of one Part. Text parts carry their words in
// "text", which the store's search indexes; the other kinds use other keys
// (tool results in "content", reasoning in "thinking") so they stay out of
// the index.
type partJSON struct {
	Type      string          `json:"type"`
	Text      *string         `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Args      json.RawMessage `json:"args,omitempty"`
	Content   *string         `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Thinking  *string         `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Redacted  bool            `json:"redacted,omitempty"`
	Data      string          `json:"data,omitempty"`
}

// MarshalParts encodes parts as a JSON array of objects with a "type" key
// (text, tool_use, tool_result or thinking). The strings of a Thinking part
// round-trip exactly, as providers require.
func MarshalParts(parts []Part) (string, error) {
	out := make([]partJSON, 0, len(parts))
	for _, p := range parts {
		switch v := p.(type) {
		case Text:
			out = append(out, partJSON{Type: "text", Text: &v.Text})
		case ToolUse:
			args := v.Args
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			out = append(out, partJSON{Type: "tool_use", ID: v.ID, Name: v.Name, Args: args})
		case ToolResult:
			out = append(out, partJSON{Type: "tool_result", ID: v.ID, Content: &v.Text, IsError: v.IsError})
		case Thinking:
			out = append(out, partJSON{Type: "thinking", Thinking: &v.Text, Signature: v.Signature, Redacted: v.Redacted, Data: v.Data})
		default:
			return "", fmt.Errorf("llm: cannot encode part %T", p)
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return "", fmt.Errorf("llm: encode parts: %w", err)
	}
	return string(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

// UnmarshalParts decodes what MarshalParts wrote.
func UnmarshalParts(s string) ([]Part, error) {
	var in []partJSON
	if err := json.Unmarshal([]byte(s), &in); err != nil {
		return nil, fmt.Errorf("llm: decode parts: %w", err)
	}
	out := make([]Part, 0, len(in))
	for _, p := range in {
		switch p.Type {
		case "text":
			out = append(out, Text{Text: deref(p.Text)})
		case "tool_use":
			out = append(out, ToolUse{ID: p.ID, Name: p.Name, Args: p.Args})
		case "tool_result":
			out = append(out, ToolResult{ID: p.ID, Text: deref(p.Content), IsError: p.IsError})
		case "thinking":
			out = append(out, Thinking{Text: deref(p.Thinking), Signature: p.Signature, Redacted: p.Redacted, Data: p.Data})
		default:
			return nil, fmt.Errorf("llm: unknown part type %q", p.Type)
		}
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
