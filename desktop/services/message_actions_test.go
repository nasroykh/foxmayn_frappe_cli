package services

import (
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
)

// shown returns the conversation as the chat shows it: role and text.
func shown(t *testing.T, g *assistantRig, convID string) []string {
	t.Helper()
	d, err := g.a.GetConversation(convID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range d.Messages {
		s := m.Role + ":" + m.Text
		for _, tc := range m.Tools {
			s += "[" + tc.Tool + "]"
		}
		out = append(out, s)
	}
	return out
}

func promptID(t *testing.T, g *assistantRig, convID, text string) string {
	t.Helper()
	d, err := g.a.GetConversation(convID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range d.Messages {
		if m.Role == "user" && m.Text == text {
			return m.ID
		}
	}
	t.Fatalf("no prompt %q", text)
	return ""
}

func TestMessageActions(t *testing.T) {
	g := newAssistantRig(t,
		textTurn("A1"),
		toolTurn(call("c1", "get_doc", `{"doctype":"ToDo","name":"TD-1"}`)), textTurn("A2"),
		textTurn("A3"),
		textTurn("A3 again"),
	)
	c := g.conv(t, ModeRead)
	g.send(t, c.ID, "Q1", 1)
	g.send(t, c.ID, "Q2", 2)
	g.send(t, c.ID, "Q3", 3)
	want := func(w ...string) {
		t.Helper()
		if got := shown(t, g, c.ID); strings.Join(got, "|") != strings.Join(w, "|") {
			t.Fatalf("shown %q, want %q", got, w)
		}
	}
	want("user:Q1", "assistant:A1", "user:Q2", "assistant:[get_doc]", "assistant:A2", "user:Q3", "assistant:A3")

	// Only prompts can be the target; an unknown id is not found.
	d, _ := g.a.GetConversation(c.ID)
	if err := g.a.DeleteExchange(c.ID, d.Messages[1].ID); errorCode(t, err) != CodeInvalid {
		t.Errorf("delete an answer: %v", err)
	}
	if err := g.a.DeleteExchange(c.ID, "nope"); errorCode(t, err) != CodeNotFound {
		t.Errorf("delete unknown: %v", err)
	}
	if _, err := g.a.Rewind("other", d.Messages[0].ID); errorCode(t, err) != CodeNotFound {
		t.Errorf("rewind in another conversation: %v", err)
	}

	// Deleting the middle exchange takes its tool call, results and answer.
	if err := g.a.DeleteExchange(c.ID, promptID(t, g, c.ID, "Q2")); err != nil {
		t.Fatal(err)
	}
	want("user:Q1", "assistant:A1", "user:Q3", "assistant:A3")

	// Retry replaces the last answer; the model gets the history up to Q3.
	if _, err := g.a.Retry(c.ID); err != nil {
		t.Fatal(err)
	}
	g.done(t, 4)
	g.a.run.wg.Wait()
	want("user:Q1", "assistant:A1", "user:Q3", "assistant:A3 again")
	reqs := g.prov.Requests()
	last := reqs[len(reqs)-1].Messages
	if n := len(last); n != 3 || last[n-1].Role != llm.RoleUser {
		t.Fatalf("retry request: %+v", last)
	}

	// Rewind gives the prompt back and removes it and what follows.
	got, err := g.a.Rewind(c.ID, promptID(t, g, c.ID, "Q3"))
	if err != nil || got.Text != "Q3" || len(got.Attachments) != 0 {
		t.Fatalf("rewind: %+v %v", got, err)
	}
	want("user:Q1", "assistant:A1")
}

// A prompt from an imported file is never edited or run again; deleting it
// is fine.
func TestMessageActionsRefuseImportedPrompts(t *testing.T) {
	g := newAssistantRig(t)
	g.provider(t)
	_, st, done, err := g.a.enter()
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.ImportConversation(store.ExportData{
		Conversation: store.Conversation{Title: "imported", Site: "prod", Mode: ModeRead, ProviderID: "p1", Model: "m1"},
		Messages:     []store.Message{{ID: "m1", Seq: 1, Role: "user", PartsJSON: `[{"type":"text","text":"do it"}]`}},
	})
	done()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.a.Retry(c.ID); errorCode(t, err) != CodeInvalid {
		t.Errorf("retry imported: %v", err)
	}
	id := promptID(t, g, c.ID, "do it")
	if _, err := g.a.Rewind(c.ID, id); errorCode(t, err) != CodeInvalid {
		t.Errorf("rewind imported: %v", err)
	}
	if err := g.a.DeleteExchange(c.ID, id); err != nil {
		t.Errorf("delete imported: %v", err)
	}
	if _, err := g.a.Retry(c.ID); errorCode(t, err) != CodeInvalid {
		t.Errorf("retry empty: %v", err)
	}
}

// Edit refuses, before removing anything, when the prompt's files and those
// already staged would be more than a message may carry; otherwise the
// prompt's files are staged again.
func TestRewindStagedLimit(t *testing.T) {
	g := newAssistantRig(t)
	c := g.conv(t, ModeRead)
	_, st, done, err := g.a.enter()
	if err != nil {
		t.Fatal(err)
	}
	// The store stays open; the service's lock is not held across its calls.
	done()
	stage := func(name string) store.Attachment {
		a, err := st.AddAttachment(store.Attachment{ConvID: c.ID, Name: name, Mime: "text/plain", SHA256: name, Text: name}, nil, 10)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a1, a2 := stage("1.txt"), stage("2.txt")
	m, err := st.AppendUserMessage(c.ID, "user", `[{"type":"text","text":"with files"}]`, []string{a1.ID, a2.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"3.txt", "4.txt", "5.txt", "6.txt"} {
		stage(n)
	}
	if _, err := g.a.Rewind(c.ID, m.ID); errorCode(t, err) != CodeInvalid {
		t.Fatalf("over the limit: %v", err)
	}
	if msgs, _ := st.ListMessages(c.ID); len(msgs) != 1 {
		t.Fatalf("a refused edit removed messages: %d left", len(msgs))
	}
	staged, _ := st.StagedAttachments(c.ID)
	if err := st.DeleteStagedAttachment(c.ID, staged[0].ID); err != nil {
		t.Fatal(err)
	}
	got, err := g.a.Rewind(c.ID, m.ID)
	if err != nil || got.Text != "with files" || len(got.Attachments) != 5 {
		t.Fatalf("rewind: %+v, %v", got, err)
	}
}
