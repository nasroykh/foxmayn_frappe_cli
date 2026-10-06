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
// file's indentation and line ending. An existing entry that already equals
// entry (in value, whatever its formatting) is left as it is. old nil or
// blank is an empty document.
func editJSON(old []byte, topKey, name string, entry jsonEntry) (out []byte, replaced bool, err error) {
	if entry.Args == nil {
		entry.Args = []string{}
	}
	bom := bytes.HasPrefix(old, utf8BOM)
	src := bytes.TrimPrefix(old, utf8BOM)
	eol := "\n"
	if bytes.Contains(src, []byte("\r\n")) {
		eol = "\r\n"
	}
	if len(bytes.TrimSpace(src)) == 0 {
		doc, err := freshJSON(topKey, name, entry, eol)
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
		body, err := marshalIndent(entry, inner, unit, eol)
		if err != nil {
			return nil, false, err
		}
		text := "{" + eol + inner + quoteJSON(name) + ": " + string(body) + eol + ind + "}"
		if err := appendMember(root, topKey, text, ind, "", eol); err != nil {
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
			body, err := marshalIndent(entry, ind, unit, eol)
			if err != nil {
				return nil, false, err
			}
			if err := appendMember(servers, name, string(body), ind, topInd, eol); err != nil {
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
			body, err := marshalIndent(entry, ind, unit, eol)
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
	if err := checkJSONResult(out, topKey, name, entry, standard); err != nil {
		return nil, false, err
	}
	if bom {
		out = append(append([]byte{}, utf8BOM...), out...)
	}
	return out, replaced, nil
}

// checkJSONResult proves the edit before anything is written: the result
// parses, a strict JSON file stays strict (Claude Desktop and Cursor read
// plain JSON), and <topKey>.<name> is there once with the entry's value.
func checkJSONResult(out []byte, topKey, name string, entry jsonEntry, standard bool) error {
	fail := func(why string) error {
		return fmt.Errorf("could not edit the file safely: the result %s; add the entry by hand", why)
	}
	v, err := hujson.Parse(out)
	if err != nil {
		return fail("would not be valid JSON (" + err.Error() + ")")
	}
	if standard && !v.IsStandard() {
		return fail("would no longer be strict JSON")
	}
	root, ok := v.Value.(*hujson.Object)
	if !ok {
		return fail("would have no top-level object")
	}
	top, err := uniqueMember(root, topKey)
	if err != nil || top == nil {
		return fail(fmt.Sprintf("would not hold %q", topKey))
	}
	servers, ok := top.Value.Value.(*hujson.Object)
	if !ok {
		return fail(fmt.Sprintf("would not hold %q as an object", topKey))
	}
	m, err := uniqueMember(servers, name)
	if err != nil || m == nil {
		return fail(fmt.Sprintf("would not hold the %q entry", name))
	}
	if same, err := sameJSON(m.Value, entry); err != nil || !same {
		return fail(fmt.Sprintf("would hold a different %q entry", name))
	}
	return nil
}

// removeJSON deletes <topKey>.<name> from a JSON or JSONC document, the
// reverse of editJSON. Every other byte is kept: comments on lines of their
// own stay, a comment on the member's own line (before its name or after
// its value) goes with it, the object's trailing-comma style is kept, and
// an object left empty becomes {} (topKey itself stays). found is false,
// and out is old, when there is no such entry.
func removeJSON(old []byte, topKey, name string) (out []byte, found bool, err error) {
	bom := bytes.HasPrefix(old, utf8BOM)
	src := bytes.TrimPrefix(old, utf8BOM)
	if len(bytes.TrimSpace(src)) == 0 {
		return old, false, nil
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
	top, err := uniqueMember(root, topKey)
	if err != nil {
		return nil, false, err
	}
	if top == nil {
		return old, false, nil
	}
	servers, ok := top.Value.Value.(*hujson.Object)
	if !ok {
		return nil, false, fmt.Errorf("%q is not a JSON object", topKey)
	}
	m, err := uniqueMember(servers, name)
	if err != nil {
		return nil, false, fmt.Errorf("in %q: %w", topKey, err)
	}
	if m == nil {
		return old, false, nil
	}
	for i := range servers.Members {
		if &servers.Members[i] == m {
			deleteMember(servers, i)
			break
		}
	}

	out = v.Pack()
	if err := checkRemoveResult(src, out, topKey, name, standard); err != nil {
		return nil, false, err
	}
	if bom {
		out = append(append([]byte{}, utf8BOM...), out...)
	}
	return out, true, nil
}

// deleteMember removes member i of obj with the text of its line(s).
//
// The extra before a member holds the end of the previous line, the
// comment lines above the member and the start of its own line; the extra
// after it (before the next member or the closing brace) holds the end of
// its last line and what follows. The member goes with the start of its
// first line and the end of its last; when it shares a line with the next
// member, that member takes its place on the line instead.
func deleteMember(obj *hujson.Object, i int) {
	ms := obj.Members
	n := len(ms)
	before := ms[i].Name.BeforeExtra
	// kept: up to the line break that starts the member's line.
	var kept []byte
	if b := lineBreaks(before); len(b) > 0 {
		kept = before[:b[len(b)-1]]
	} else {
		kept = bytes.TrimRight(before, " \t")
	}

	if i < n-1 {
		next := ms[i+1].Name.BeforeExtra
		if b := lineBreaks(next); len(b) > 0 {
			ms[i+1].Name.BeforeExtra = concatExtra(kept, next[b[0]:])
		} else {
			ms[i+1].Name.BeforeExtra = concatExtra(before)
		}
		obj.Members = append(ms[:i:i], ms[i+1:]...)
		return
	}

	// The last member: what follows its line is the closing brace's line.
	var closing []byte
	if b := lineBreaks(obj.AfterExtra); len(b) > 0 {
		closing = obj.AfterExtra[b[0]:]
	} else if len(bytes.TrimSpace(obj.AfterExtra)) == 0 {
		closing = obj.AfterExtra // "... }" on one line, no comment
	}
	if n == 1 {
		obj.Members = nil
		inner := concatExtra(kept, closing)
		if len(bytes.TrimSpace(inner)) == 0 {
			inner = hujson.Extra{} // {}
		}
		obj.AfterExtra = inner
		return
	}
	prev := &ms[i-1].Value
	if ms[i].Value.AfterExtra != nil {
		// Trailing-comma style: the new last member keeps its comma.
		if prev.AfterExtra == nil {
			prev.AfterExtra = hujson.Extra{}
		}
		obj.AfterExtra = concatExtra(kept, closing)
	} else {
		// No trailing comma: the one after the new last member goes, and
		// what stood before it moves after the member.
		obj.AfterExtra = concatExtra(prev.AfterExtra, kept, closing)
		prev.AfterExtra = nil
	}
	obj.Members = ms[:i:i]
}

// concatExtra joins byte slices into a new Extra, never writing into the
// parsed source.
func concatExtra(parts ...[]byte) hujson.Extra {
	var n int
	for _, p := range parts {
		n += len(p)
	}
	out := make(hujson.Extra, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// checkRemoveResult proves a removal before anything is written: the
// result parses, a strict JSON file stays strict, <topKey>.<name> is gone,
// and every other member, at the top level and in topKey, is still there in
// the same order with byte-identical names and values.
func checkRemoveResult(src, out []byte, topKey, name string, standard bool) error {
	fail := func(why string) error {
		return fmt.Errorf("could not edit the file safely: the result %s; remove the entry by hand", why)
	}
	nv, err := hujson.Parse(out)
	if err != nil {
		return fail("would not be valid JSON (" + err.Error() + ")")
	}
	if standard && !nv.IsStandard() {
		return fail("would no longer be strict JSON")
	}
	ov, err := hujson.Parse(src)
	if err != nil {
		return fail("could not be compared (" + err.Error() + ")")
	}
	oroot, _ := ov.Value.(*hujson.Object)
	nroot, ok := nv.Value.(*hujson.Object)
	if oroot == nil || !ok || len(nroot.Members) != len(oroot.Members) {
		return fail("would change other settings")
	}
	for j, om := range oroot.Members {
		nm := nroot.Members[j]
		if !sameValueBytes(om.Name, nm.Name) {
			return fail("would change other settings")
		}
		if lit, _ := om.Name.Value.(hujson.Literal); lit == nil || lit.String() != topKey {
			if !sameValueBytes(om.Value, nm.Value) {
				return fail("would change other settings")
			}
			continue
		}
		oldServers, _ := om.Value.Value.(*hujson.Object)
		ns, ok := nm.Value.Value.(*hujson.Object)
		if oldServers == nil || !ok {
			return fail(fmt.Sprintf("would not hold %q as an object", topKey))
		}
		if m, err := uniqueMember(ns, name); err != nil || m != nil {
			return fail(fmt.Sprintf("would still hold the %q entry", name))
		}
		var others []hujson.ObjectMember
		for _, m := range oldServers.Members {
			if lit, _ := m.Name.Value.(hujson.Literal); lit == nil || lit.String() != name {
				others = append(others, m)
			}
		}
		if len(others) != len(ns.Members) {
			return fail(fmt.Sprintf("would change other entries in %q", topKey))
		}
		for k, m := range others {
			if !sameValueBytes(m.Name, ns.Members[k].Name) || !sameValueBytes(m.Value, ns.Members[k].Value) {
				return fail(fmt.Sprintf("would change other entries in %q", topKey))
			}
		}
	}
	return nil
}

// sameValueBytes compares two values byte for byte, without the comments
// and whitespace around them (those inside are compared).
func sameValueBytes(a, b hujson.Value) bool {
	a.BeforeExtra, a.AfterExtra = nil, nil
	b.BeforeExtra, b.AfterExtra = nil, nil
	return bytes.Equal(a.Pack(), b.Pack())
}

// freshJSON is a new document holding only the entry.
func freshJSON(topKey, name string, entry jsonEntry, eol string) ([]byte, error) {
	const unit = "  "
	body, err := marshalIndent(entry, unit+unit, unit, eol)
	if err != nil {
		return nil, err
	}
	return []byte("{" + eol + unit + quoteJSON(topKey) + ": {" + eol + unit + unit + quoteJSON(name) + ": " +
		string(body) + eol + unit + "}" + eol + "}" + eol), nil
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
func appendMember(obj *hujson.Object, name, valueText, ind, parentInd, eol string) error {
	val, err := hujson.Parse([]byte(valueText))
	if err != nil {
		return fmt.Errorf("internal error: %w", err)
	}
	after := []byte(obj.AfterExtra)
	var head, tail []byte
	if i := lastLineBreak(after); i >= 0 {
		head, tail = after[:i], after[i:]
	} else {
		head, tail = after, []byte(eol+parentInd)
	}
	head = bytes.TrimRight(head, " \t\r")
	before := append(append([]byte{}, head...), eol...)
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

// lastLineBreak returns the index of the last line break ("\r\n" or "\n")
// in extra that is not inside a block comment, or -1. A newline inside
// /* ... */ belongs to the comment: splitting there would put the new member
// inside it.
func lastLineBreak(extra []byte) int {
	breaks := lineBreaks(extra)
	if len(breaks) == 0 {
		return -1
	}
	return breaks[len(breaks)-1]
}

// lineBreaks returns the index of every line break ("\r\n" or "\n") in
// extra that is not inside a block comment.
func lineBreaks(extra []byte) []int {
	var out []int
	for i := 0; i < len(extra); i++ {
		switch {
		case bytes.HasPrefix(extra[i:], []byte("/*")):
			end := bytes.Index(extra[i+2:], []byte("*/"))
			if end < 0 {
				return out // not valid HuJSON; the result check refuses it
			}
			i += 2 + end + 1 // on the closing '/'
		case bytes.HasPrefix(extra[i:], []byte("//")):
			end := bytes.IndexByte(extra[i:], '\n')
			if end < 0 {
				return out
			}
			i += end - 1 // the newline ending the comment is a line break
		case extra[i] == '\n':
			at := i
			if i > 0 && extra[i-1] == '\r' {
				at = i - 1
			}
			out = append(out, at)
		}
	}
	return out
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
// the trailing newline, with eol as the line ending.
func marshalIndent(v interface{}, prefix, indent, eol string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent(prefix, indent)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	b := bytes.TrimRight(buf.Bytes(), "\n")
	if eol != "\n" {
		// encoding/json escapes newlines in strings: every raw one is a
		// line break.
		b = bytes.ReplaceAll(b, []byte("\n"), []byte(eol))
	}
	return b, nil
}

func quoteJSON(s string) string {
	b, _ := marshalIndent(s, "", "", "\n")
	return string(b)
}
