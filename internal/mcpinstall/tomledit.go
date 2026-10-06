package mcpinstall

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Codex's config.toml is edited as text, never decoded and re-encoded: that
// would drop the user's comments and reorder the file. The scanner below
// knows just enough TOML to find table headers and top-level keys: it
// follows strings (multi-line ones too), comments and brackets that span
// lines, so a "[" inside a value is never taken for a header.
//
// The entry is the table [mcp_servers.<name>] and its sub-tables
// ([mcp_servers.<name>.env], ...). They are replaced by one new table at the
// place of the first, or a new table is appended; removeTOML deletes them.
// mcp_servers written in any other form (an inline table, dotted keys, keys
// under [mcp_servers], an array of tables) is refused.

const tomlServersKey = "mcp_servers"

type tomlLineKind int

const (
	tomlOther  tomlLineKind = iota // a key/value line or the rest of a value
	tomlBlank                      // empty or only a comment
	tomlHeader                     // [table] or [[array]]
	tomlCont                       // inside a multi-line string or array
)

type tomlLine struct {
	kind   tomlLineKind
	header []string // the table path of a header
}

// editTOML installs srv in a Codex config.toml.
func editTOML(old []byte, srv Server) (out []byte, replaced bool, err error) {
	bom := bytes.HasPrefix(old, utf8BOM)
	src := string(bytes.TrimPrefix(old, utf8BOM))
	if !utf8.ValidString(src) {
		return nil, false, errors.New("not valid UTF-8, so not valid TOML")
	}
	eol := "\n"
	if strings.Contains(src, "\r\n") {
		eol = "\r\n"
	}
	var lines []string
	if src != "" {
		lines = strings.Split(strings.TrimSuffix(src, "\n"), "\n")
	}

	info, err := scanTOML(lines)
	if err != nil {
		return nil, false, err
	}

	spans, err := tomlEntrySpans(info, srv.Name)
	if err != nil {
		return nil, false, err
	}

	// New lines take the file's line ending; kept lines keep their own.
	cr := ""
	if eol == "\r\n" {
		cr = "\r"
	}
	block := tomlBlock(srv)
	for i := range block {
		block[i] += cr
	}
	var res []string
	if len(spans) == 0 {
		res = append(res, lines...)
		if len(res) > 0 && strings.TrimSpace(res[len(res)-1]) != "" {
			res = append(res, cr)
		}
		res = append(res, block...)
	} else {
		replaced = true
		next := 0
		for k, s := range spans {
			// Blank and comment lines between two tables of the entry go
			// with them.
			between := k > 0
			for j := next; j < s.start && between; j++ {
				between = info[j].kind == tomlBlank
			}
			if !between {
				res = append(res, lines[next:s.start]...)
			}
			if k == 0 {
				res = append(res, block...)
			}
			next = s.end
		}
		res = append(res, lines[next:]...)
		if sameLines(lines, res) {
			return old, true, nil // up to date (a missing final newline is no change)
		}
	}
	// The result must still be a file this editor understands.
	if _, err := scanTOML(res); err != nil {
		return nil, false, fmt.Errorf("internal error: the edited file does not scan: %w", err)
	}

	var b strings.Builder
	if bom {
		b.Write(utf8BOM)
	}
	for _, l := range res {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return []byte(b.String()), replaced, nil
}

// tomlSpan is one table of an entry: its header line and the end of the
// table (exclusive).
type tomlSpan struct{ start, end int }

// tomlEntrySpans finds the tables of the entry name: [mcp_servers.<name>]
// and its sub-tables, in file order. The main table may appear only once.
func tomlEntrySpans(info []tomlLine, name string) ([]tomlSpan, error) {
	var spans []tomlSpan
	mains := 0
	for i, l := range info {
		h := l.header
		if l.kind != tomlHeader || len(h) < 2 || h[0] != tomlServersKey || h[1] != name {
			continue
		}
		if len(h) == 2 {
			mains++
		}
		end := i + 1
		for end < len(info) && info[end].kind != tomlHeader {
			end++
		}
		// Blank and comment lines right before the next header belong to it.
		for end > i+1 && info[end-1].kind == tomlBlank {
			end--
		}
		spans = append(spans, tomlSpan{i, end})
	}
	if mains > 1 {
		return nil, fmt.Errorf("[%s.%s] appears more than once", tomlServersKey, name)
	}
	return spans, nil
}

// removeTOML removes the entry name ([mcp_servers.<name>] and its
// sub-tables) from a Codex config.toml, with the scanner and refusals of
// editTOML. Every other line is kept as it is; a blank line left next to
// another by the removal is dropped, and so are blank lines left at the
// start or end of the file. found is false (and out is old) when there is no
// such entry.
func removeTOML(old []byte, name string) (out []byte, found bool, err error) {
	bom := bytes.HasPrefix(old, utf8BOM)
	src := string(bytes.TrimPrefix(old, utf8BOM))
	if !utf8.ValidString(src) {
		return nil, false, errors.New("not valid UTF-8, so not valid TOML")
	}
	var lines []string
	if src != "" {
		lines = strings.Split(strings.TrimSuffix(src, "\n"), "\n")
	}
	info, err := scanTOML(lines)
	if err != nil {
		return nil, false, err
	}
	spans, err := tomlEntrySpans(info, name)
	if err != nil {
		return nil, false, err
	}
	if len(spans) == 0 {
		return old, false, nil
	}

	blank := func(s string) bool { return strings.TrimSpace(s) == "" }
	var res []string
	// keep appends kept lines; right after a removal, blank lines that would
	// follow a blank line or start the file are skipped.
	keep := func(chunk []string, afterCut bool) {
		for afterCut && len(chunk) > 0 && blank(chunk[0]) && (len(res) == 0 || blank(res[len(res)-1])) {
			chunk = chunk[1:]
		}
		res = append(res, chunk...)
	}
	next := 0
	for k, s := range spans {
		// Blank and comment lines between two tables of the entry go with
		// them, as in editTOML.
		between := k > 0
		for j := next; j < s.start && between; j++ {
			between = info[j].kind == tomlBlank
		}
		if !between {
			keep(lines[next:s.start], k > 0)
		}
		next = s.end
	}
	tail := lines[next:]
	for len(tail) > 0 && blank(tail[len(tail)-1]) {
		tail = tail[:len(tail)-1]
	}
	if len(tail) > 0 {
		keep(lines[next:], true)
	} else {
		for len(res) > 0 && blank(res[len(res)-1]) {
			res = res[:len(res)-1] // the entry ended the file
		}
	}

	// The result must still scan, without the entry.
	rinfo, err := scanTOML(res)
	if err != nil {
		return nil, false, fmt.Errorf("internal error: the edited file does not scan: %w", err)
	}
	if left, err := tomlEntrySpans(rinfo, name); err != nil || len(left) > 0 {
		return nil, false, fmt.Errorf("internal error: the edited file still holds [%s.%s]", tomlServersKey, name)
	}

	var b strings.Builder
	if bom {
		b.Write(utf8BOM)
	}
	for i, l := range res {
		if i == len(res)-1 && !strings.HasSuffix(src, "\n") {
			// The file had no final newline: keep it that way.
			b.WriteString(strings.TrimSuffix(l, "\r"))
			break
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return []byte(b.String()), true, nil
}

func sameLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimSuffix(a[i], "\r") != strings.TrimSuffix(b[i], "\r") {
			return false
		}
	}
	return true
}

// tomlBlock is the entry as TOML lines.
func tomlBlock(srv Server) []string {
	args := make([]string, len(srv.Args))
	for i, a := range srv.Args {
		args[i] = tomlString(a)
	}
	return []string{
		"[" + tomlServersKey + "." + srv.Name + "]",
		"command = " + tomlString(srv.Command),
		"args = [" + strings.Join(args, ", ") + "]",
	}
}

// tomlString quotes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// scanTOML classifies each line and refuses the forms of mcp_servers it
// cannot edit.
func scanTOML(lines []string) ([]tomlLine, error) {
	out := make([]tomlLine, len(lines))
	var st tomlState
	cur := []string{}
	for i, raw := range lines {
		line := strings.TrimSuffix(raw, "\r")
		if st.inString() || st.depth > 0 {
			out[i].kind = tomlCont
			if err := st.scan(line, i); err != nil {
				return nil, err
			}
			continue
		}
		trimmed := strings.TrimLeft(line, " \t")
		switch {
		case trimmed == "" || trimmed[0] == '#':
			out[i].kind = tomlBlank
		case trimmed[0] == '[':
			path, array, err := parseHeader(trimmed)
			if err != nil {
				if strings.Contains(trimmed, tomlServersKey) {
					return nil, fmt.Errorf("line %d: cannot read the table header %q: %w", i+1, trimmed, err)
				}
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			if array && len(path) > 0 && path[0] == tomlServersKey {
				return nil, fmt.Errorf("line %d: %s as an array of tables ([[...]]) is not supported", i+1, tomlServersKey)
			}
			out[i] = tomlLine{kind: tomlHeader, header: path}
			cur = path
		default:
			out[i].kind = tomlOther
			keys, rest, err := parseKey(trimmed, '=')
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			full := append(append([]string{}, cur...), keys...)
			if full[0] == tomlServersKey && len(cur) < 2 {
				return nil, fmt.Errorf("line %d: %s is set with a dotted key or an inline table (%q); "+
					"only [%s.<name>] tables can be edited, so convert it or add the server by hand",
					i+1, tomlServersKey, strings.TrimSpace(line), tomlServersKey)
			}
			if err := st.scan(rest, i); err != nil {
				return nil, err
			}
		}
	}
	if st.inString() {
		return nil, errors.New("a multi-line string is not closed")
	}
	if st.depth > 0 {
		return nil, errors.New("an array or inline table is not closed")
	}
	return out, nil
}

// tomlState follows a value across lines.
type tomlState struct {
	multi byte // '"' or '\'' inside a multi-line string, else 0
	depth int  // open [ and { of values
}

func (s *tomlState) inString() bool { return s.multi != 0 }

// scan reads one line (or the value part of one) of a value.
func (s *tomlState) scan(line string, n int) error {
	for i := 0; i < len(line); {
		c := line[i]
		if s.multi != 0 {
			if c == '\\' && s.multi == '"' {
				i += 2
				continue
			}
			q := strings.Repeat(string(s.multi), 3)
			if strings.HasPrefix(line[i:], q) {
				i += 3
				// Up to two more quotes belong to the string.
				for k := 0; k < 2 && i < len(line) && line[i] == s.multi; k++ {
					i++
				}
				s.multi = 0
				continue
			}
			i++
			continue
		}
		switch c {
		case '#':
			return nil
		case '"', '\'':
			if strings.HasPrefix(line[i:], strings.Repeat(string(c), 3)) {
				s.multi = c
				i += 3
				continue
			}
			end, err := stringEnd(line, i)
			if err != nil {
				return fmt.Errorf("line %d: %w", n+1, err)
			}
			i = end
			continue
		case '[', '{':
			s.depth++
		case ']', '}':
			s.depth--
			if s.depth < 0 {
				return fmt.Errorf("line %d: unbalanced %q", n+1, c)
			}
		}
		i++
	}
	return nil
}

// stringEnd returns the index after the one-line string starting at i.
func stringEnd(line string, i int) (int, error) {
	q := line[i]
	for j := i + 1; j < len(line); j++ {
		switch {
		case q == '"' && line[j] == '\\':
			j++
		case line[j] == q:
			return j + 1, nil
		}
	}
	return 0, errors.New("a string is not closed")
}

// parseHeader reads "[a.b]" or "[[a.b]]" with an optional comment after it.
func parseHeader(s string) (path []string, array bool, err error) {
	if strings.HasPrefix(s, "[[") {
		array = true
		s = s[2:]
	} else {
		s = s[1:]
	}
	keys, rest, err := parseKey(s, ']')
	if err != nil {
		return nil, false, err
	}
	if array {
		if !strings.HasPrefix(rest, "]") {
			return nil, false, errors.New("an array table header must end with ]]")
		}
		rest = rest[1:]
	}
	rest = strings.TrimLeft(rest, " \t")
	if rest != "" && rest[0] != '#' {
		return nil, false, fmt.Errorf("unexpected text after the table header: %q", rest)
	}
	return keys, array, nil
}

// parseKey reads a dotted key up to the terminator ('=' or ']') and
// returns its parts and the text after the terminator.
func parseKey(s string, term byte) (keys []string, rest string, err error) {
	i := 0
	skip := func() {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
	}
	for {
		skip()
		if i >= len(s) {
			return nil, "", errors.New("unexpected end of line in a key")
		}
		switch c := s[i]; {
		case c == '"' || c == '\'':
			end, err := stringEnd(s, i)
			if err != nil {
				return nil, "", err
			}
			k, err := unquoteKey(s[i:end])
			if err != nil {
				return nil, "", err
			}
			keys = append(keys, k)
			i = end
		case isBareKeyChar(c):
			j := i
			for j < len(s) && isBareKeyChar(s[j]) {
				j++
			}
			keys = append(keys, s[i:j])
			i = j
		default:
			return nil, "", fmt.Errorf("unexpected %q in a key", c)
		}
		skip()
		if i >= len(s) {
			return nil, "", errors.New("unexpected end of line in a key")
		}
		switch s[i] {
		case '.':
			i++
		case term:
			return keys, s[i+1:], nil
		default:
			return nil, "", fmt.Errorf("unexpected %q in a key", s[i])
		}
	}
}

func isBareKeyChar(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// unquoteKey decodes a quoted key: a literal string as is, a basic string
// with TOML's escapes (which strconv's Go escapes cover, \e aside).
func unquoteKey(q string) (string, error) {
	if q[0] == '\'' {
		return q[1 : len(q)-1], nil
	}
	if strings.Contains(q, `\e`) || strings.Contains(q, `\x`) {
		return "", fmt.Errorf("unsupported escape in the key %s", q)
	}
	k, err := strconv.Unquote(q)
	if err != nil {
		return "", fmt.Errorf("cannot read the key %s: %w", q, err)
	}
	return k, nil
}
