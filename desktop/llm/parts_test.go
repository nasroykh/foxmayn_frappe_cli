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
	if _, err := UnmarshalParts(`[{"type":"image"}]`); err == nil {
		t.Error("unknown type accepted")
	}
	if _, err := UnmarshalParts(`nope`); err == nil {
		t.Error("garbage accepted")
	}
	p, _ := UnmarshalParts(`[{"type":"tool_use","id":"a","name":"n"}]`)
	if tu := p[0].(ToolUse); tu.Args != nil {
		t.Errorf("args = %s", tu.Args)
	}
}
