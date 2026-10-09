package services

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
)

const (
	// importedHistoryLimit caps the characters of imported history sent to
	// the model; the newest are kept.
	importedHistoryLimit = 60_000
	// The longest tool arguments and tool result kept per call in it.
	importedArgsLimit   = 2_000
	importedResultLimit = 8_000
)

// importedBlock renders the imported messages of a conversation as the one
// piece of text the model gets in their place. A file is not the person's
// word: its messages are never replayed as turns of their own (no assistant
// turn, tool call, tool result or thinking the model could take for its own),
// only described as text inside a wrapper marked untrusted, with every '<'
// escaped so nothing in it can close the wrapper.
func importedBlock(rows []store.Message) (llm.Text, error) {
	var parts []string
	for _, m := range rows {
		ps, err := llm.UnmarshalParts(m.PartsJSON)
		if err != nil {
			return llm.Text{}, newError(CodeFailed, "A saved message could not be read.", err)
		}
		who := "User"
		if m.Role == string(llm.RoleAssistant) {
			who = "Assistant"
		}
		var lines []string
		for _, p := range ps {
			switch v := p.(type) {
			case llm.Text:
				lines = append(lines, v.Text)
			case llm.ToolUse:
				lines = append(lines, fmt.Sprintf("[tool call: %s %s]", v.Name, clipRunes(string(v.Args), importedArgsLimit)))
			case llm.ToolResult:
				kind := "tool result"
				if v.IsError {
					kind = "tool error"
				}
				lines = append(lines, fmt.Sprintf("[%s: %s]", kind, clipRunes(v.Text, importedResultLimit)))
			case llm.Image:
				lines = append(lines, "[image not included]")
			}
			// Thinking is not part of the record.
		}
		if len(lines) > 0 {
			parts = append(parts, who+":\n"+strings.Join(lines, "\n"))
		}
	}
	text := strings.Join(parts, "\n\n")
	if n := utf8.RuneCountInString(text); n > importedHistoryLimit {
		text = "[earlier imported history not shown]\n" + string([]rune(text)[n-importedHistoryLimit:])
	}
	return llm.Text{Text: `<imported_history untrusted="true">` + "\n" + escapeUntrusted(text) + "\n</imported_history>"}, nil
}

// foldImported puts the model messages of a conversation in order: one block
// for everything imported (see importedBlock), then the messages the model
// and the person made here. rows are in order; the block joins the first
// user message, or stands as one, so the turns keep alternating.
func foldImported(rows []store.Message) ([]llm.Message, error) {
	var imported []store.Message
	out := make([]llm.Message, 0, len(rows))
	for _, m := range rows {
		if store.IsImportedID(m.ID) {
			imported = append(imported, m)
			continue
		}
		parts, err := llm.UnmarshalParts(m.PartsJSON)
		if err != nil {
			return nil, newError(CodeFailed, "A saved message could not be read.", err)
		}
		out = append(out, llm.Message{Role: llm.Role(m.Role), Parts: parts})
	}
	if len(imported) == 0 {
		return out, nil
	}
	block, err := importedBlock(imported)
	if err != nil {
		return nil, err
	}
	if len(out) > 0 && out[0].Role == llm.RoleUser {
		out[0].Parts = append([]llm.Part{block}, out[0].Parts...)
		return out, nil
	}
	return append([]llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{block}}}, out...), nil
}
