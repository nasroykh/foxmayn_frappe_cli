package services

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm/llmtest"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// A turn that ended without a usable answer is shown with the reason.
func TestServiceErrorExplainsNoAnswer(t *testing.T) {
	err := fmt.Errorf("turn: %w", &llm.APIError{Status: 502, Category: "malformed_function_call", Message: "the model produced a tool call that could not be read"})
	se := toServiceError(err)
	if se.Code != CodeFailed || !strings.Contains(se.Message, "could not be read") {
		t.Fatalf("got %#v", se)
	}
	plain := toServiceError(&llm.APIError{Status: 500, Message: "boom"})
	if plain.Message != "The AI provider returned an error." {
		t.Fatalf("plain %#v", plain)
	}
}

func TestRedactSecretsGoogleKey(t *testing.T) {
	key := "AIza" + strings.Repeat("Xy9_-", 7)
	got := redactSecrets("key " + key + " refused")
	if strings.Contains(got, key) || !strings.Contains(got, "[hidden]") {
		t.Fatalf("got %q", got)
	}
	if redactSecrets("AIzaShort") != "AIzaShort" {
		t.Fatal("short AIza text redacted")
	}
}

// A pre-read failure longer than the result limit is cut before it is
// wrapped, so the closing tag is never cut off, even when every '<' of it
// grows into "&lt;".
func TestUpdateDocPreReadFailureLongKeepsWrapper(t *testing.T) {
	g := newLoopRig(t, toolTurn(call("c1", "update_doc", `{"doctype":"ToDo","name":"TD-1","data":{"description":"x"}}`)), textTurn("ok"))
	g.fake.Handle("GET /api/resource/ToDo/TD-1", frappetest.ErrorHandler(frappetest.Validation(strings.Repeat("<x", 30000))))
	cid := g.conv(t, ModeAsk)
	if _, err := g.r.start(cid, "go"); err != nil {
		t.Fatal(err)
	}
	g.waitDone(t, 1)
	res := g.toolResults(t, cid)
	if len(res) != 1 {
		t.Fatalf("results = %+v", res)
	}
	txt := res[0].Text
	if !strings.HasPrefix(txt, "Could not read the document before the change:\n") ||
		!strings.Contains(txt, `<tool_result tool="get_doc" untrusted="true">`) ||
		!strings.HasSuffix(txt, "</tool_result>") || strings.Count(txt, "</tool_result>") != 1 {
		t.Fatalf("wrapper lost: %q ... %q", txt[:120], txt[len(txt)-120:])
	}
	if !strings.Contains(txt, "[cut: ") {
		t.Fatal("the text was not long enough to be cut")
	}
	if len(g.writes(t)) != 0 {
		t.Error("the update was sent")
	}
}

// A turn the model ended with no usable answer is not sent again: the request
// was complete and paid for.
func TestNoAnswerTurnNotRetried(t *testing.T) {
	noAnswer := &llm.APIError{Status: 502, Category: "malformed_function_call", Message: "the model produced a tool call that could not be read"}
	g := newLoopRig(t, llmtest.Turn{Err: noAnswer}, textTurn("must not be asked"))
	cid := g.conv(t, ModeRead)
	if _, err := g.r.start(cid, "hi"); err != nil {
		t.Fatal(err)
	}
	if d := g.waitDone(t, 1); d.Status != RunError {
		t.Fatalf("done = %+v", d)
	}
	if n := len(g.prov.Requests()); n != 1 {
		t.Errorf("%d model requests, want 1", n)
	}
}
