package output

import (
	"bytes"
	"encoding/json"
	"testing"
)

// rows as the client returns them: numbers stay json.Number literals.
func renderRows() []map[string]interface{} {
	return []map[string]interface{}{
		{"name": "TD-1", "amount": json.Number("1500.0"), "qty": json.Number("12345678901234567890"), "note": "a,b \"q\"", "tags": []interface{}{"x"}, "done": true},
		{"name": "TD-2", "amount": json.Number("0.5"), "qty": json.Number("1"), "note": "tab\there\nline", "tags": nil, "done": false},
	}
}

func TestWriteFormats(t *testing.T) {
	golden := map[Format]string{
		FormatJSON: `[
  {
    "amount": 1500.0,
    "done": true,
    "name": "TD-1",
    "note": "a,b \"q\"",
    "qty": 12345678901234567890,
    "tags": [
      "x"
    ]
  },
  {
    "amount": 0.5,
    "done": false,
    "name": "TD-2",
    "note": "tab\there\nline",
    "qty": 1,
    "tags": null
  }
]
`,
		FormatNDJSON: `{"amount":1500.0,"done":true,"name":"TD-1","note":"a,b \"q\"","qty":12345678901234567890,"tags":["x"]}
{"amount":0.5,"done":false,"name":"TD-2","note":"tab\there\nline","qty":1,"tags":null}
`,
		FormatCSV: "amount,done,name,note,qty,tags\n" +
			"1500.0,true,TD-1,\"a,b \"\"q\"\"\",12345678901234567890,\"[\"\"x\"\"]\"\n" +
			"0.5,false,TD-2,\"tab\there\nline\",1,\n",
		FormatTSV: "amount\tdone\tname\tnote\tqty\ttags\n" +
			"1500.0\ttrue\tTD-1\ta,b \"q\"\t12345678901234567890\t[\"x\"]\n" +
			"0.5\tfalse\tTD-2\ttab\\there\\nline\t1\t\n",
		FormatYAML: `- amount: 1500.0
  done: true
  name: TD-1
  note: a,b "q"
  qty: 12345678901234567890
  tags:
    - x
- amount: 0.5
  done: false
  name: TD-2
  note: |-
    tab	here
    line
  qty: 1
  tags: null
`,
	}
	for f, want := range golden {
		var b bytes.Buffer
		if err := Write(&b, f, renderRows(), nil, false); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if b.String() != want {
			t.Errorf("%s:\n got %q\nwant %q", f, b.String(), want)
		}
	}
}

func TestWriteFieldsAndShapes(t *testing.T) {
	var b bytes.Buffer
	// Requested fields set the columns and their order ("x as y" is y).
	if err := Write(&b, FormatCSV, renderRows(), []string{"name", "sum(amount) as total", "amount"}, false); err != nil {
		t.Fatal(err)
	}
	if want := "name,total,amount\nTD-1,,1500.0\nTD-2,,0.5\n"; b.String() != want {
		t.Errorf("fields: %q", b.String())
	}
	cases := []struct {
		f    Format
		v    interface{}
		want string
	}{
		{FormatCSV, map[string]interface{}{"b": 1, "a": "x"}, "a,b\nx,1\n"},
		{FormatCSV, 42, "value\n42\n"},
		{FormatNDJSON, map[string]interface{}{"a": 1}, "{\"a\":1}\n"},
		{FormatJSON, []map[string]interface{}{}, "[]\n"},
		{FormatCSV, []interface{}{}, ""},
	}
	for _, c := range cases {
		b.Reset()
		if err := Write(&b, c.f, c.v, nil, false); err != nil {
			t.Fatal(err)
		}
		if b.String() != c.want {
			t.Errorf("%s %v: %q, want %q", c.f, c.v, b.String(), c.want)
		}
	}
	// A terminal gets cells without control characters.
	b.Reset()
	_ = Write(&b, FormatCSV, map[string]interface{}{"a": "x\x1b]0;t\x07"}, nil, true)
	if b.String() != "a\nx]0;t\n" {
		t.Errorf("clean: %q", b.String())
	}
}

func TestListStreamMatchesWrite(t *testing.T) {
	for _, f := range []Format{FormatJSON, FormatNDJSON, FormatCSV, FormatTSV} {
		var whole, streamed bytes.Buffer
		if err := Write(&whole, f, renderRows(), nil, false); err != nil {
			t.Fatal(err)
		}
		s := NewListStream(&streamed, f, nil, false)
		rows := renderRows()
		for _, batch := range [][]map[string]interface{}{rows[:1], nil, rows[1:]} {
			if err := s.Rows(batch); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		// CSV/TSV columns come from the first batch; these rows share keys.
		if whole.String() != streamed.String() {
			t.Errorf("%s: streamed %q\nwhole %q", f, streamed.String(), whole.String())
		}
	}
}

func TestYAMLQuotesBoolLikeKeys(t *testing.T) {
	var b bytes.Buffer
	if err := Write(&b, FormatYAML, map[string]interface{}{"yes": "no", "on": "0x1F", "plain": "x"}, nil, false); err != nil {
		t.Fatal(err)
	}
	if want := "\"on\": \"0x1F\"\nplain: x\n\"yes\": \"no\"\n"; b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}

func TestParseFormat(t *testing.T) {
	if f, err := ParseFormat(" CSV "); err != nil || f != FormatCSV {
		t.Errorf("CSV: %v %v", f, err)
	}
	if _, err := ParseFormat("xml"); err == nil {
		t.Error("xml accepted")
	}
}
