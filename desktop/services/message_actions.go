package services

import (
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
)

// Message actions: delete an exchange, retry the last answer, edit a prompt.
// An exchange is a prompt (a user message with text or files, as the chat
// shows it) and every message after it up to the next prompt: the answer,
// its tool calls and their results. Tool calls and usage stay recorded (the
// tokens were spent); repairHistory keeps what the model gets valid.

// EditResult is what Rewind gives the composer back.
type EditResult struct {
	// Text is the prompt's text.
	Text string `json:"text"`
	// Attachments are the prompt's files, staged again for the next message.
	Attachments []StagedAttachment `json:"attachments"`
}

// isPrompt says whether a stored message is a prompt: a user message with
// something besides tool results.
func isPrompt(m store.Message) (bool, error) {
	if m.Role != string(llm.RoleUser) {
		return false, nil
	}
	parts, err := llm.UnmarshalParts(m.PartsJSON)
	if err != nil {
		return false, newError(CodeFailed, "A saved message could not be read.", err)
	}
	for _, p := range parts {
		if _, ok := p.(llm.ToolResult); !ok {
			return true, nil
		}
	}
	return false, nil
}

// exchange finds the prompt msgID among rows and returns its index and the
// index of the next prompt (len(rows) when it is the last).
func exchange(rows []store.Message, msgID string) (int, int, error) {
	start := -1
	for i, m := range rows {
		if m.ID == msgID {
			start = i
			break
		}
	}
	if start < 0 {
		return 0, 0, keyed(CodeNotFound, "chat.messageGone", nil)
	}
	ok, err := isPrompt(rows[start])
	if err != nil {
		return 0, 0, err
	}
	if !ok {
		return 0, 0, keyed(CodeInvalid, "chat.notAPrompt", nil)
	}
	end := len(rows)
	for i := start + 1; i < len(rows); i++ {
		p, err := isPrompt(rows[i])
		if err != nil {
			return 0, 0, err
		}
		if p {
			end = i
			break
		}
	}
	return start, end, nil
}

func messageIDs(rows []store.Message) []string {
	ids := make([]string, len(rows))
	for i, m := range rows {
		ids[i] = m.ID
	}
	return ids
}

// DeleteExchange removes a prompt and its answer from a conversation. It is
// refused while a run is active.
func (a *AssistantService) DeleteExchange(convID, msgID string) error {
	r, st, done, err := a.enter()
	if err != nil {
		return err
	}
	defer done()
	return r.whileIdle(convID, func() error {
		rows, err := st.ListMessages(convID)
		if err != nil {
			return wrapStoreErr(err)
		}
		start, end, err := exchange(rows, msgID)
		if err != nil {
			return err
		}
		// A paused run belongs to the answer being removed.
		if err := st.AbandonPausedRuns(convID); err != nil {
			return wrapStoreErr(err)
		}
		if err := st.DeleteMessages(convID, messageIDs(rows[start:end]), ""); err != nil {
			return wrapStoreErr(err)
		}
		return nil
	})
}

// Rewind removes a prompt and everything after it, and gives its text and
// files back for the composer: editing a prompt is sending it again from
// there. A prompt from an imported file cannot be edited (its text is not the
// user's own). It is refused while a run is active.
func (a *AssistantService) Rewind(convID, msgID string) (EditResult, error) {
	r, st, done, err := a.enter()
	if err != nil {
		return EditResult{}, err
	}
	defer done()
	var out EditResult
	err = r.whileIdle(convID, func() error {
		rows, err := st.ListMessages(convID)
		if err != nil {
			return wrapStoreErr(err)
		}
		start, _, err := exchange(rows, msgID)
		if err != nil {
			return err
		}
		if store.IsImportedID(msgID) {
			return keyed(CodeInvalid, "chat.importedPrompt", nil)
		}
		parts, err := llm.UnmarshalParts(rows[start].PartsJSON)
		if err != nil {
			return newError(CodeFailed, "A saved message could not be read.", err)
		}
		for _, p := range parts {
			if t, ok := p.(llm.Text); ok && t.AttachmentID == "" {
				out.Text += t.Text
			}
		}
		if err := st.AbandonPausedRuns(convID); err != nil {
			return wrapStoreErr(err)
		}
		if err := st.DeleteMessages(convID, messageIDs(rows[start:]), msgID); err != nil {
			return wrapStoreErr(err)
		}
		staged, err := st.StagedAttachments(convID)
		if err != nil {
			return wrapStoreErr(err)
		}
		out.Attachments = make([]StagedAttachment, 0, len(staged))
		for _, s := range staged {
			out.Attachments = append(out.Attachments, stagedOf(s))
		}
		return nil
	})
	return out, err
}

// Retry removes the last answer and runs the last prompt again. A prompt
// from an imported file is never run. It returns the new run's id.
func (a *AssistantService) Retry(convID string) (string, error) {
	r, _, done, err := a.enter()
	if err != nil {
		return "", err
	}
	defer done()
	return r.retry(convID)
}

// retry deletes what follows the last prompt and starts a run on it, under
// r.mu so no other run can start in between.
func (r *runner) retry(convID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	conv, err := r.store.GetConversation(convID)
	if err != nil {
		return "", wrapStoreErr(err)
	}
	if err := r.admit(convID); err != nil {
		return "", err
	}
	rows, err := r.store.ListMessages(convID)
	if err != nil {
		return "", wrapStoreErr(err)
	}
	last := -1
	for i := len(rows) - 1; i >= 0; i-- {
		ok, err := isPrompt(rows[i])
		if err != nil {
			return "", err
		}
		if ok {
			last = i
			break
		}
	}
	if last < 0 {
		return "", keyed(CodeInvalid, "chat.nothingToRetry", nil)
	}
	if store.IsImportedID(rows[last].ID) {
		return "", keyed(CodeInvalid, "chat.importedPrompt", nil)
	}
	if err := r.store.AbandonPausedRuns(convID); err != nil {
		return "", wrapStoreErr(err)
	}
	if after := rows[last+1:]; len(after) > 0 {
		if err := r.store.DeleteMessages(convID, messageIDs(after), ""); err != nil {
			return "", wrapStoreErr(err)
		}
	}
	run, err := r.store.CreateRun(convID)
	if err != nil {
		return "", wrapStoreErr(err)
	}
	r.launch(run.ID, conv, 0)
	return run.ID, nil
}
