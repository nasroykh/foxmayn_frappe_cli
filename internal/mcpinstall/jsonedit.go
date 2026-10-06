package mcpinstall

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/tailscale/hujson"
)

// jsonEntry is a stdio server entry in Claude Desktop, Cursor and VS Code
// configs, in the key order written.
type jsonEntry struct {
	Type    string   `json:"type,omitempty"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

var utf8BOM = []byte("\xEF\xBB\xBF")

// editJSON sets <topKey>.<name> to entry in a JSON or JSONC document and
// returns the new document. Comments, key order and the formatting of
// everything else are kept byte for byte; the entry is written in the
// file's indentation. An existing entry that already equals entry (in
// value, whatever its formatting) is left as it is. old nil or blank is an
// empty document.
func editJSON(old []byte, topKey, name string, entry jsonEntry) (out []byte, replaced bool, err error) {
	if entry.Args == nil {
		entry.Args = []string{}
	}
	bom := bytes.HasPrefix(old, utf8BOM)
	src := bytes.TrimPrefix(old, utf8BOM)
	if len(bytes.TrimSpace(src)) == 0 {
		doc, err := freshJSON(topKey, name, entry)
		return doc, false, err
	}
	v, err := hujson.Parse(src)
	if err != nil {
		return nil, false, fmt.Errorf("not valid JSON: %w", err)
	}
	standard := v.IsStandard()
	root, ok := v.Value.(*hujson.Object)
	if !ok {
		return nil, false, errors.New("the top level is not a JSON object")
	}
	unit := indentUnit(root)

	top, err := uniqueMember(root, topKey)
	if err != nil {
		return nil, false, err
	}
	if top == nil {
		ind := memberIndent(root, "", unit)
		inner := ind + unit
		body, err := marshalIndent(entry, inner, unit)
		if err != nil {
			return nil, false, err
		}
		text := "{\n" + inner + quoteJSON(name) + ": " + string(body) + "\n" + ind + "}"
		if err := appendMember(root, topKey, text, ind, ""); err != nil {
			return nil, false, err
		}
	} else {
		servers, ok := top.Value.Value.(*hujson.Object)
		if !ok {
			return nil, false, fmt.Errorf("%q is not a JSON object", topKey)
		}
		topInd := lineIndent(top.Name.BeforeExtra, unit)
		m, err := uniqueMember(servers, name)
		if err != nil {
			return nil, false, fmt.Errorf("in %q: %w", topKey, err)
		}
		if m == nil {
			ind := memberIndent(servers, topInd, unit)
			body, err := marshalIndent(entry, ind, unit)
			if err != nil {
				return nil, false, err
			}
			if err := appendMember(servers, name, string(body), ind, topInd); err != nil {
				return nil, false, err
			}
		} else {
			replaced = true
			same, err := sameJSON(m.Value, entry)
			if err != nil {
				return nil, false, err
			}
			if same {
				return old, true, nil
			}
			ind := lineIndent(m.Name.BeforeExtra, topInd+unit)
			body, err := marshalIndent(entry, ind, unit)
			if err != nil {
				return nil, false, err
			}
			nv, err := hujson.Parse(body)
			if err != nil {
				return nil, false, fmt.Errorf("internal error: %w", err)
			}
			m.Value.Value = nv.Value
		}
	}

	out = v.Pack()
	// The result must parse, and a strict JSON file must stay strict (Claude
	// Desktop and Cursor read plain JSON).
	check, err := hujson.Parse(out)
	if err != nil || (standard && !check.IsStandard()) {
		return nil, false, fmt.Errorf("internal error: the edited file would not be valid JSON (%v)", err)
	}
	if bom {
		out = append(append([]byte{}, utf8BOM...), out...)
	}
	return out, replaced, nil
}

// freshJSON is a new document holding only the entry.
func freshJSON(topKey, name string, entry jsonEntry) ([]byte, error) {
	const unit = "  "
	body, err := marshalIndent(entry, unit+unit, unit)
	if err != nil {
		return nil, err
	}
	return []byte("{\n" + unit + quoteJSON(topKey) + ": {\n" + unit + unit + quoteJSON(name) + ": " +
		string(body) + "\n" + unit + "}\n}\n"), nil
}

// uniqueMember returns the member named name, nil if there is none, and
// an error if there are several (parsers disagree on which one counts).
func uniqueMember(obj *hujson.Object, name string) (*hujson.ObjectMember, error) {
	var found *hujson.ObjectMember
	for i := range obj.Members {
		lit, ok := obj.Members[i].Name.Value.(hujson.Literal)
		if !ok || lit.String() != name {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("the key %q appears more than once", name)
		}
		found = &obj.Members[i]
	}
	return found, nil
}

// appendMember adds "name": <valueText> as the last member of obj on its own
// line at indent ind; parentInd is the indentation of obj's closing brace.
// A comment after the old last member stays on that member's line, and a
// trailing comma is kept when the old last member had one.
func appendMember(obj *hujson.Object, name, valueText, ind, parentInd string) error {
	val, err := hujson.Parse([]byte(valueText))
	if err != nil {
		return fmt.Errorf("internal error: %w", err)
	}
	after := []byte(obj.AfterExtra)
	var head, tail []byte
	if i := bytes.LastIndexByte(after, '\n'); i >= 0 {
		head, tail = after[:i], after[i:]
	} else {
		head, tail = after, []byte("\n"+parentInd)
	}
	head = bytes.TrimRight(head, " \t\r")
	before := append(append([]byte{}, head...), '\n')
	before = append(before, ind...)

	member := hujson.ObjectMember{
		Name:  hujson.Value{BeforeExtra: hujson.Extra(before), Value: hujson.String(name)},
		Value: hujson.Value{BeforeExtra: hujson.Extra(" "), Value: val.Value},
	}
	if n := len(obj.Members); n > 0 && obj.Members[n-1].Value.AfterExtra != nil {
		member.Value.AfterExtra = hujson.Extra{} // keep the trailing comma style
	}
	obj.Members = append(obj.Members, member)
	obj.AfterExtra = hujson.Extra(tail)
	return nil
}

// indentUnit guesses one level of indentation from the first member of the
// top-level object: a tab, or its run of spaces; two spaces by default.
func indentUnit(root *hujson.Object) string {
	if len(root.Members) > 0 {
		if ind := lineIndent(root.Members[0].Name.BeforeExtra, ""); ind != "" {
			return ind
		}
	}
	return "  "
}

// memberIndent is the indentation of obj's members: that of its last
// member, or parentInd plus one unit when it has none.
func memberIndent(obj *hujson.Object, parentInd, unit string) string {
	if n := len(obj.Members); n > 0 {
		if ind := lineIndent(obj.Members[n-1].Name.BeforeExtra, ""); ind != "" {
			return ind
		}
	}
	return parentInd + unit
}

// lineIndent returns the whitespace that starts the line a value is on,
// taken from the extra before it, or def when the value does not start its
// own line.
func lineIndent(extra hujson.Extra, def string) string {
	i := bytes.LastIndexByte(extra, '\n')
	if i < 0 {
		return def
	}
	ind := string(extra[i+1:])
	if strings.Trim(ind, " \t") != "" {
		return def
	}
	return ind
}

// sameJSON reports whether a parsed value equals entry as JSON data.
func sameJSON(v hujson.Value, entry jsonEntry) (bool, error) {
	c := v.Clone()
	c.Standardize()
	var have interface{}
	if err := json.Unmarshal(c.Pack(), &have); err != nil {
		return false, nil
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return false, err
	}
	var want interface{}
	if err := json.Unmarshal(b, &want); err != nil {
		return false, err
	}
	return reflect.DeepEqual(have, want), nil
}

// marshalIndent is json.MarshalIndent without HTML escaping and without
// the trailing newline.
func marshalIndent(v interface{}, prefix, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent(prefix, indent)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func quoteJSON(s string) string {
	b, _ := marshalIndent(s, "", "")
	return string(b)
}
