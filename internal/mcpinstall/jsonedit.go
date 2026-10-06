package mcpinstall

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
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
// reverse of editJSON, by cutting bytes out of it (memberCuts): comments on
// other lines stay, a comment on the member's own lines goes with it, the
// object's comma style is kept, and an object left empty becomes {}
// (topKey itself stays). found is false, and out is old, when there is no
// such entry (or topKey is null).
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
		if lit, isLit := top.Value.Value.(hujson.Literal); isLit && lit.Kind() == 'n' {
			return old, false, nil // "mcpServers": null holds nothing
		}
		return nil, false, fmt.Errorf("%q is not a JSON object", topKey)
	}
	m, err := uniqueMember(servers, name)
	if err != nil {
		return nil, false, fmt.Errorf("in %q: %w", topKey, err)
	}
	if m == nil {
		return old, false, nil
	}
	var cuts []byteRange
	for i := range servers.Members {
		if &servers.Members[i] == m {
			if cuts, err = memberCuts(src, top.Value, i); err != nil {
				return nil, false, err
			}
			break
		}
	}

	if out, err = deleteRanges(src, cuts); err != nil {
		return nil, false, err
	}
	if err := checkRemoveResult(src, out, topKey, name, standard); err != nil {
		return nil, false, err
	}
	if bom {
		out = append(append([]byte{}, utf8BOM...), out...)
	}
	return out, true, nil
}

// byteRange is a span [start, end) of the source to delete.
type byteRange struct{ start, end int }

// memberCuts returns the byte ranges of src to delete to remove member i of
// the object value obj (parsed from src, so offsets are valid), with one of
// the commas around it.
//
// When the member has lines of its own, those lines go: from the line break
// before its first line to the line break after its last, with the
// comments on them. A comma that starts its line (comma-first style) goes
// with the member. Comments on other lines are never touched, nor is a
// block comment that spans lines, even when it starts or ends on the
// member's line. When the member shares a line with another member, only
// the member, its comma and the blanks after the comma go, so a comment
// before the next member stays with it. An object left with nothing but
// whitespace becomes {}.
func memberCuts(src []byte, obj hujson.Value, i int) ([]byteRange, error) {
	o, _ := obj.Value.(*hujson.Object)
	open, closeAt := obj.StartOffset, obj.EndOffset-1
	if o == nil || i < 0 || i >= len(o.Members) || closeAt <= open || src[open] != '{' || src[closeAt] != '}' {
		return nil, errors.New("internal error: unexpected object layout")
	}
	ms := o.Members
	n := len(ms)
	ns, ve := ms[i].Name.StartOffset, ms[i].Value.EndOffset
	ca, cb, prevEnd := -1, -1, -1 // comma after, comma before, end of the previous value
	if i < n-1 || ms[i].Value.AfterExtra != nil {
		ca = ve + len(ms[i].Value.AfterExtra)
	}
	if i > 0 {
		prevEnd = ms[i-1].Value.EndOffset
		cb = prevEnd + len(ms[i-1].Value.AfterExtra)
	}
	if (ca >= 0 && (ca >= closeAt || src[ca] != ',')) || (cb >= 0 && src[cb] != ',') {
		return nil, errors.New("internal error: a comma is not where expected")
	}
	lo, hi := open+1, closeAt // the extras before the member and up to the next one
	if i > 0 {
		lo = cb + 1
	}
	if i < n-1 {
		hi = ms[i+1].Name.StartOffset
	}
	blanksAfter := func(p int) int {
		for p < hi && (src[p] == ' ' || src[p] == '\t') {
			p++
		}
		return p
	}

	// lines deletes the member's lines: the line starts after the last cut
	// in src[bs:be] and ends at the first cut in src[as:ae]; toEnd lets it
	// run to ae when there is none (only the closing brace follows).
	lines := func(bs, be, as, ae int, toEnd bool, more ...byteRange) []byteRange {
		st, stComment, ok := lastCut(src[bs:be])
		if !ok {
			return nil
		}
		start := bs + st
		end := ae
		en, enComment, ok := firstCut(src[as:ae])
		switch {
		case ok:
			end = as + en
		case !toEnd:
			return nil
		}
		if ok && enComment && !stComment {
			// A comment spanning lines follows on the member's last line:
			// it takes the member's place, after the line break and indent.
			start = skipLineBreak(src, start)
			for start < end && (src[start] == ' ' || src[start] == '\t') {
				start++
			}
		}
		return append([]byteRange{{start, end}}, more...)
	}

	var cuts []byteRange
	pBreaks := len(lineBreaks(src[lo:ns])) > 0
	switch {
	case i > 0 && !pBreaks && len(lineBreaks(src[prevEnd:cb])) > 0:
		// Comma-first: the comma before the member starts its line.
		ae := closeAt
		if ca >= 0 {
			ae = ca
		}
		cuts = lines(prevEnd, cb, ve, ae, ca < 0)
	case pBreaks && ca >= 0:
		if _, _, cut := firstCut(src[ve:ca]); cut {
			// The comma after the member starts the next line: it goes on
			// its own, with the blanks after it.
			cuts = lines(lo, ns, ve, ca, false, byteRange{ca, blanksAfter(ca + 1)})
		} else {
			cuts = lines(lo, ns, ca+1, hi, false)
		}
	case pBreaks:
		// The last member, no trailing comma: the comma before it goes.
		var more []byteRange
		if cb >= 0 {
			more = append(more, byteRange{cb, cb + 1})
		}
		cuts = lines(lo, ns, ve, closeAt, true, more...)
	}
	if cuts == nil {
		// The member shares a line with another one. A block comment that
		// spans lines, between the member and the comma that goes, stays.
		keepMulti := func(start, end int) []byteRange {
			var rs []byteRange
			from := start
			for _, c := range extraComments(src[start:end]) {
				if c.multi {
					rs = append(rs, byteRange{from, start + c.start})
					from = start + c.end
				}
			}
			return append(rs, byteRange{from, end})
		}
		switch {
		case ca >= 0:
			cuts = append([]byteRange{{ns, ve}}, keepMulti(ve, ca)...)
			cuts = append(cuts, byteRange{ca, blanksAfter(ca + 1)})
		case i > 0:
			cuts = append([]byteRange{{cb, cb + 1}}, keepMulti(cb+1, ns)...)
			cuts = append(cuts, byteRange{ns, ve})
		default:
			cuts = []byteRange{{ns, ve}}
		}
	}

	if n == 1 {
		// Nothing but whitespace left inside: {}.
		rest, err := deleteRanges(src[:closeAt], cuts)
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(rest[open+1:])) == 0 {
			cuts = []byteRange{{open + 1, closeAt}}
		}
	}
	return cuts, nil
}

// deleteRanges returns src without the given ranges, which must not
// overlap.
func deleteRanges(src []byte, cuts []byteRange) ([]byte, error) {
	rs := append([]byteRange{}, cuts...)
	sort.Slice(rs, func(a, b int) bool { return rs[a].start < rs[b].start })
	out := make([]byte, 0, len(src))
	pos := 0
	for _, r := range rs {
		if r.start < pos || r.end < r.start || r.end > len(src) {
			return nil, errors.New("internal error: overlapping edits")
		}
		out = append(out, src[pos:r.start]...)
		pos = r.end
	}
	return append(out, src[pos:]...), nil
}

// skipLineBreak returns the index after the line break ("\r\n" or "\n") at
// p, or p.
func skipLineBreak(b []byte, p int) int {
	if p < len(b) && b[p] == '\r' {
		p++
	}
	if p < len(b) && b[p] == '\n' {
		p++
	}
	return p
}

// commentSpan is a comment in an extra; multi is true for a block comment
// that spans lines.
type commentSpan struct {
	start, end int
	multi      bool
}

// extraComments returns the comments in an extra (whitespace, commas and
// comments only, so no strings).
func extraComments(extra []byte) []commentSpan {
	var out []commentSpan
	for i := 0; i < len(extra); i++ {
		switch {
		case bytes.HasPrefix(extra[i:], []byte("/*")):
			end := bytes.Index(extra[i+2:], []byte("*/"))
			if end < 0 {
				return out
			}
			e := i + 2 + end + 2
			out = append(out, commentSpan{i, e, bytes.IndexByte(extra[i:e], '\n') >= 0})
			i = e - 1
		case bytes.HasPrefix(extra[i:], []byte("//")):
			e := bytes.IndexByte(extra[i:], '\n')
			if e < 0 {
				e = len(extra) - i
			}
			out = append(out, commentSpan{start: i, end: i + e})
			i += e - 1
		}
	}
	return out
}

// lastCut finds where the member's line starts in the extra before it: at
// its last line break, or after a block comment that spans lines and ends
// on the member's line (which then stays). ok is false when the extra has
// neither: the member shares its line with what comes before.
func lastCut(extra []byte) (pos int, afterComment, ok bool) {
	pos = -1
	if b := lineBreaks(extra); len(b) > 0 {
		pos = b[len(b)-1]
	}
	for _, c := range extraComments(extra) {
		if c.multi && c.end > pos {
			pos, afterComment = c.end, true
		}
	}
	return pos, afterComment, pos >= 0
}

// firstCut finds where the member's last line ends in the extra after it:
// at its first line break, or where a block comment that spans lines
// starts (it stays). ok is false when the extra has neither.
func firstCut(extra []byte) (pos int, atComment, ok bool) {
	pos = -1
	if b := lineBreaks(extra); len(b) > 0 {
		pos = b[0]
	}
	for _, c := range extraComments(extra) {
		if c.multi && (pos < 0 || c.start < pos) {
			pos, atComment = c.start, true
			break
		}
	}
	return pos, atComment, pos >= 0
}

// jsonComments returns the comments of a JSONC text as [start, end) spans
// (a line comment without its line break), skipping strings.
func jsonComments(b []byte) []commentSpan {
	var out []commentSpan
	for i := 0; i < len(b); i++ {
		switch {
		case b[i] == '"':
			for i++; i < len(b) && b[i] != '"'; i++ {
				if b[i] == '\\' {
					i++
				}
			}
		case bytes.HasPrefix(b[i:], []byte("/*")):
			end := bytes.Index(b[i+2:], []byte("*/"))
			if end < 0 {
				return out
			}
			out = append(out, commentSpan{start: i, end: i + 2 + end + 2})
			i += 2 + end + 1
		case bytes.HasPrefix(b[i:], []byte("//")):
			e := bytes.IndexByte(b[i:], '\n')
			if e < 0 {
				e = len(b) - i
			}
			end := i + e
			if end > i && b[end-1] == '\r' {
				end--
			}
			out = append(out, commentSpan{start: i, end: end})
			i += e - 1
		}
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
	ownLo, ownHi := -1, -1
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
			} else {
				// The member's own lines: from the start of its first to the
				// end of its last.
				ownLo = bytes.LastIndexByte(src[:m.Name.StartOffset], '\n') + 1
				ownHi = len(src)
				if k := bytes.IndexByte(src[m.Value.EndOffset:], '\n'); k >= 0 {
					ownHi = m.Value.EndOffset + k
				}
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
	if ownLo < 0 {
		return fail(fmt.Sprintf("could not be compared (no %q entry before)", name))
	}
	// Every comment outside the member's own lines is still there (a block
	// comment that only starts or ends on them counts as outside), and none
	// is new.
	had, have := map[string]int{}, map[string]int{}
	for _, c := range jsonComments(src) {
		had[string(src[c.start:c.end])]++
		if c.start < ownLo || c.end > ownHi {
			have[string(src[c.start:c.end])]-- // must survive
		}
	}
	for _, c := range jsonComments(out) {
		t := string(out[c.start:c.end])
		have[t]++
		if had[t]--; had[t] < 0 {
			return fail("would hold a comment that was not there")
		}
	}
	for t, k := range have {
		if k < 0 {
			return fail(fmt.Sprintf("would lose the comment %q", t))
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
