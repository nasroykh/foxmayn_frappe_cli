package llm

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPartsRoundTrip(t *testing.T) {
	in := []Part{
		Thinking{Text: "hm <b> & \u2028 \"q\"", Signature: "sig/+==\n", Data: ""},
		Thinking{Redacted: true, Data: "ENC<>&data=="},
		Text{Text: "hello <world> & \"you\"\n\u00e9\U0001F600"},
		Text{},
		ToolUse{ID: "t1", Name: "get_doc", Args: json.RawMessage(`{"doctype":"ToDo","name":"TD-1"}`)},
		ToolResult{ID: "t1", Text: "{\"a\":1}", IsError: true},
		ToolResult{ID: "t2", Text: ""},
		Thinking{Provider: ProviderOpenAI, Signature: "rs_1", Data: "gAAAA=="},
		Thinking{Provider: ProviderGemini, Signature: "c2ln"},
		Image{AttachmentID: "att-1", MediaType: "image/png"},
		Text{Text: "<attachment name=\"a.csv\">\na,b\n</attachment>", AttachmentID: "att-2"},
		Text{AttachmentID: "att-3"},
	}
	s, err := MarshalParts(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := UnmarshalParts(s)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round trip:\n in  %#v\n out %#v", in, out)
	}
	s2, err := MarshalParts(out)
	if err != nil || s2 != s {
		t.Errorf("not byte-stable:\n%s\n%s\n%v", s, s2, err)
	}
}

func TestPartsShape(t *testing.T) {
	s, _ := MarshalParts([]Part{Text{Text: "hi"}, ToolResult{ID: "x", Text: "secretish"}, Thinking{Text: "think", Signature: "s"}})
	var objs []map[string]any
	if err := json.Unmarshal([]byte(s), &objs); err != nil {
		t.Fatal(err)
	}
	if objs[0]["type"] != "text" || objs[0]["text"] != "hi" {
		t.Errorf("text part = %v", objs[0])
	}
	// Only text parts carry a "text" key, so only they are indexed.
	for _, o := range objs[1:] {
		if _, ok := o["text"]; ok {
			t.Errorf("non-text part has text key: %v", o)
		}
	}
	if strings.Contains(s, `\u003c`) {
		t.Error("html escaped")
	}
}

func TestPartsEmptyAndErrors(t *testing.T) {
	s, err := MarshalParts(nil)
	if err != nil || s != "[]" {
		t.Errorf("nil = %q, %v", s, err)
	}
	if _, err := UnmarshalParts(`[{"type":"audio"}]`); err == nil {
		t.Error("unknown type accepted")
	}
	if _, err := UnmarshalParts(`[{"type":"image"}]`); err == nil {
		t.Error("image without attachment id accepted")
	}
	if _, err := MarshalParts([]Part{Image{MediaType: "image/png"}}); err == nil {
		t.Error("image without attachment id encoded")
	}
	if _, err := UnmarshalParts(`nope`); err == nil {
		t.Error("garbage accepted")
	}
	p, _ := UnmarshalParts(`[{"type":"tool_use","id":"a","name":"n"}]`)
	if tu := p[0].(ToolUse); tu.Args != nil {
		t.Errorf("args = %s", tu.Args)
	}
}

// Rows written by 0.2.0 have no provider, attachment_id or media_type keys.
// They must decode to the same parts as before (Provider empty) and encode
// back to the same bytes, so a stored history is never rewritten.
func TestPartsDecode020Rows(t *testing.T) {
	rows := []string{
		`[{"type":"thinking","thinking":"plan","signature":"EqQBCkYI"},{"type":"text","text":"Hi <b>"},{"type":"tool_use","id":"toolu_1","name":"get_doc","args":{"doctype":"ToDo"}}]`,
		`[{"type":"thinking","thinking":"","redacted":true,"data":"ENC=="}]`,
		`[{"type":"tool_result","id":"toolu_1","content":"{\"a\":1}","is_error":true},{"type":"text","text":"next"}]`,
	}
	want := [][]Part{
		{Thinking{Text: "plan", Signature: "EqQBCkYI"}, Text{Text: "Hi <b>"}, ToolUse{ID: "toolu_1", Name: "get_doc", Args: json.RawMessage(`{"doctype":"ToDo"}`)}},
		{Thinking{Redacted: true, Data: "ENC=="}},
		{ToolResult{ID: "toolu_1", Text: `{"a":1}`, IsError: true}, Text{Text: "next"}},
	}
	for i, row := range rows {
		got, err := UnmarshalParts(row)
		if err != nil {
			t.Fatalf("row %d: %v", i, err)
		}
		if !reflect.DeepEqual(got, want[i]) {
			t.Errorf("row %d:\n got  %#v\n want %#v", i, got, want[i])
		}
		if th, ok := got[0].(Thinking); ok && th.Provider != "" {
			t.Errorf("row %d: provider = %q", i, th.Provider)
		}
		again, err := MarshalParts(got)
		if err != nil || again != row {
			t.Errorf("row %d re-encoded:\n%s\n%s\n%v", i, row, again, err)
		}
	}
}

func TestPartsNewKeysStayOutOfText(t *testing.T) {
	s, _ := MarshalParts([]Part{Image{AttachmentID: "a1", MediaType: "image/jpeg"}, Thinking{Provider: ProviderGemini, Signature: "c2ln"},
		Text{Text: "file words", AttachmentID: "a2"}})
	if !strings.Contains(s, `{"type":"attachment","content":"file words","attachment_id":"a2"}`) {
		t.Errorf("attachment text shape: %s", s)
	}
	if _, err := UnmarshalParts(`[{"type":"attachment","content":"x"}]`); err == nil {
		t.Error("an attachment part without an id should not decode")
	}
	if strings.Contains(s, `"text"`) {
		t.Errorf("image or thinking carries a text key: %s", s)
	}
	if !strings.Contains(s, `"type":"image","attachment_id":"a1","media_type":"image/jpeg"`) || !strings.Contains(s, `"provider":"gemini"`) {
		t.Errorf("shape: %s", s)
	}
}

func TestForeignThinking(t *testing.T) {
	cases := []struct {
		provider, own string
		foreign       bool
	}{
		{"", ProviderAnthropic, false},
		{"", ProviderOpenAI, true},
		{"", ProviderGemini, true},
		{ProviderAnthropic, ProviderAnthropic, false},
		{ProviderOpenAI, ProviderOpenAI, false},
		{ProviderOpenAI, ProviderGemini, true},
		{ProviderGemini, ProviderGemini, false},
		{ProviderGemini, ProviderAnthropic, true},
		{"someone", ProviderAnthropic, true},
	}
	for _, c := range cases {
		if got := ForeignThinking(Thinking{Provider: c.provider}, c.own); got != c.foreign {
			t.Errorf("ForeignThinking(%q, %q) = %v", c.provider, c.own, got)
		}
	}
}
