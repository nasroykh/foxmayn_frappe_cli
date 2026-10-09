package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
)

// markdownResultLimit is how many characters of a tool result the Markdown
// export keeps (a var so a test can shorten it).
var markdownResultLimit = 4000

// renderMarkdown writes a conversation for reading: the messages in order,
// each tool call in a <details> block with its arguments and result. The
// model's reasoning is left out and an image is a note with its type only.
func renderMarkdown(f exportFile) string {
	var b strings.Builder
	title := oneLine(f.Conversation.Title)
	if title == "" {
		title = "Untitled conversation"
	}
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "- Site: %s\n", oneLine(f.Conversation.Site))
	if f.Conversation.ProviderID != "" || f.Conversation.Model != "" {
		fmt.Fprintf(&b, "- Model: %s\n", oneLine(strings.Trim(f.Conversation.ProviderID+" / "+f.Conversation.Model, " /")))
	}
	if f.Conversation.ProfileID != "" {
		fmt.Fprintf(&b, "- Profile: %s\n", oneLine(profileLabel(f)))
	}
	if t, err := time.Parse(time.RFC3339Nano, f.Conversation.Created); err == nil {
		fmt.Fprintf(&b, "- Started: %s\n", t.UTC().Format("2006-01-02 15:04 UTC"))
	}

	// The result of each tool call, by tool use id, from the user messages
	// that carry them.
	results := map[string]llm.ToolResult{}
	parsed := make([][]llm.Part, len(f.Messages))
	for i, m := range f.Messages {
		parts, err := llm.UnmarshalParts(string(m.Parts))
		if err != nil {
			continue
		}
		parsed[i] = parts
		for _, p := range parts {
			if r, ok := p.(llm.ToolResult); ok {
				results[r.ID] = r
			}
		}
	}
	callsByMsg := map[string][]exportToolCall{}
	for _, t := range f.ToolCalls {
		callsByMsg[t.MsgID] = append(callsByMsg[t.MsgID], t)
	}

	for i, m := range f.Messages {
		parts := parsed[i]
		if parts == nil {
			fmt.Fprintf(&b, "\n## %s\n\n*(this message could not be read)*\n", roleHeading(m.Role))
			continue
		}
		var body strings.Builder
		next := 0
		for _, p := range parts {
			switch v := p.(type) {
			case llm.Text:
				if strings.TrimSpace(v.Text) != "" {
					body.WriteString(strings.TrimSpace(v.Text) + "\n\n")
				}
			case llm.Image:
				fmt.Fprintf(&body, "*[image: %s]*\n\n", oneLine(v.MediaType))
			case llm.ToolUse:
				var row *exportToolCall
				if rows := callsByMsg[m.ID]; next < len(rows) {
					row = &rows[next]
				}
				next++
				writeToolCall(&body, v, results[v.ID], row)
			}
		}
		if body.Len() == 0 {
			continue // tool results and reasoning only
		}
		fmt.Fprintf(&b, "\n## %s\n\n%s\n", roleHeading(m.Role), strings.TrimRight(body.String(), "\n"))
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func profileLabel(f exportFile) string {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(f.Profile, &p); err == nil && p.Name != "" {
		return p.Name
	}
	return f.Conversation.ProfileID
}

func roleHeading(role string) string {
	if role == "assistant" {
		return "Assistant"
	}
	return "You"
}

// oneLine puts a value on one line, so it cannot start a new Markdown block.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// fence returns a code fence longer than any run of backticks in s.
func fence(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	if longest < 3 {
		longest = 2
	}
	return strings.Repeat("`", longest+1)
}

func writeToolCall(b *strings.Builder, use llm.ToolUse, res llm.ToolResult, row *exportToolCall) {
	summary := oneLine(use.Name)
	if row != nil {
		status := row.Status
		if row.Approval != "" {
			status += ", " + row.Approval
		}
		summary += " on " + oneLine(row.Site) + " (" + oneLine(status) + ")"
	}
	fmt.Fprintf(b, "<details>\n<summary>%s</summary>\n\n", htmlEscape(summary))
	args := string(use.Args)
	var buf bytes.Buffer
	if json.Indent(&buf, use.Args, "", "  ") == nil {
		args = buf.String()
	}
	f := fence(args)
	fmt.Fprintf(b, "Arguments:\n\n%sjson\n%s\n%s\n\n", f, args, f)
	text, have := res.Text, res.ID != ""
	if !have && row != nil && row.ResultText != "" {
		text, have = row.ResultText, true
	}
	if have {
		if r := []rune(text); len(r) > markdownResultLimit {
			text = string(r[:markdownResultLimit]) + fmt.Sprintf("\n... (%d more characters)", len(r)-markdownResultLimit)
		}
		f := fence(text)
		label := "Result"
		if res.IsError {
			label = "Error"
		}
		fmt.Fprintf(b, "%s:\n\n%stext\n%s\n%s\n\n", label, f, text, f)
	}
	b.WriteString("</details>\n\n")
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
