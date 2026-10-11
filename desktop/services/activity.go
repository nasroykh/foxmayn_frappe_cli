package services

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
)

// The Activity screen reads ffc's MCP audit log (mcp-audit.jsonl next to the
// config file, and the older half rotated to .1): one line per tool call by
// any MCP client, the Assistant and the connected apps alike. ffc writes it
// with arguments already reduced (data and args to their keys and size,
// secrets cut); this side only reads, never writes or clears.

// ActivityService serves the audit log to the Activity screen.
type ActivityService struct {
	path string // the audit log; .1 is its older half
}

// NewActivityService reads the audit log of the ffc config at configPath.
func NewActivityService(configPath string) *ActivityService {
	return &ActivityService{path: filepath.Join(filepath.Dir(configPath), "mcp-audit.jsonl")}
}

// ActivityEntry is one tool call.
type ActivityEntry struct {
	Time       time.Time `json:"time"`
	Site       string    `json:"site"`
	Client     string    `json:"client"`
	Tool       string    `json:"tool"`
	RunID      string    `json:"runID"`
	Via        string    `json:"via"`
	Doctypes   []string  `json:"doctypes"`
	Names      []string  `json:"names"`
	NamesTotal int       `json:"namesTotal"`
	Method     string    `json:"method"`
	// Args are the arguments as ffc logged them, as indented JSON ("" when
	// none).
	Args       string `json:"args"`
	Status     string `json:"status"`
	Confirm    string `json:"confirm"`
	Error      string `json:"error"`
	DurationMS int64  `json:"durationMS"`
}

// ActivityQuery filters the log. Empty fields match everything; Since and
// Until bound the time (zero: open). Offset skips that many matching
// entries, newest first; Limit is 1 to activityMaxLimit (0: the default).
type ActivityQuery struct {
	Site   string    `json:"site"`
	Client string    `json:"client"`
	Tool   string    `json:"tool"`
	Status string    `json:"status"`
	RunID  string    `json:"runID"`
	Since  time.Time `json:"since"`
	Until  time.Time `json:"until"`
	Offset int       `json:"offset"`
	Limit  int       `json:"limit"`
}

// ActivityPage is one page of matching entries, newest first, with the
// values the filters can take (from the whole log, not only this page).
type ActivityPage struct {
	Entries []ActivityEntry `json:"entries"`
	// Total is how many entries match.
	Total int `json:"total"`
	// Skipped counts lines that could not be read (damaged or too long).
	Skipped  int      `json:"skipped"`
	Sites    []string `json:"sites"`
	Clients  []string `json:"clients"`
	Tools    []string `json:"tools"`
	Statuses []string `json:"statuses"`
	// Exists is false when there is no log yet (no MCP call ever made).
	Exists bool `json:"exists"`
}

const (
	activityDefaultLimit = 50
	activityMaxLimit     = 200
	// activityMaxFile bounds what is read of one log file. ffc rotates at
	// 10 MiB; a bigger file (another writer) is read from its end.
	activityMaxFile = 16 << 20
	// activityMaxLine bounds one line (ffc keeps them under 8 KiB).
	activityMaxLine = 64 << 10
	// activityMaxArgs bounds the arguments shown for one entry.
	activityMaxArgs = 16 << 10
	// activityMaxField bounds a text field of an entry.
	activityMaxField = 1000
	activityMaxList  = 50
)

// auditLine is a line of mcp-audit.jsonl (internal/cmd/mcp_audit.go).
type auditLine struct {
	Time       time.Time       `json:"time"`
	Site       string          `json:"site"`
	Client     string          `json:"client"`
	Tool       string          `json:"tool"`
	RunID      string          `json:"run_id"`
	Via        string          `json:"via"`
	Doctypes   []string        `json:"doctypes"`
	Names      []string        `json:"names"`
	NamesTotal int             `json:"names_total"`
	Method     string          `json:"method"`
	Args       json.RawMessage `json:"args"`
	Status     string          `json:"status"`
	Confirm    string          `json:"confirm"`
	Error      string          `json:"error"`
	DurationMS int64           `json:"duration_ms"`
}

// Query returns a page of the log, newest first.
func (s *ActivityService) Query(q ActivityQuery) (ActivityPage, error) {
	limit := q.Limit
	switch {
	case limit == 0:
		limit = activityDefaultLimit
	case limit < 0 || limit > activityMaxLimit:
		return ActivityPage{}, keyedInvalid("limit", "activity.limit")
	}
	if q.Offset < 0 {
		return ActivityPage{}, keyedInvalid("offset", "activity.limit")
	}
	page := ActivityPage{Entries: []ActivityEntry{}, Sites: []string{}, Clients: []string{}, Tools: []string{}, Statuses: []string{}}
	var lines []auditLine
	// Older half first, so lines end up oldest to newest.
	for _, p := range []string{s.path + ".1", s.path} {
		got, skipped, exists, err := readAuditFile(p)
		if err != nil {
			return ActivityPage{}, keyed(CodeFailed, "activity.readFailed", err)
		}
		page.Exists = page.Exists || exists
		page.Skipped += skipped
		lines = append(lines, got...)
	}
	facet := func(list *[]string, v string) {
		if v != "" && !slices.Contains(*list, v) {
			*list = append(*list, v)
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		facet(&page.Sites, l.Site)
		facet(&page.Clients, l.Client)
		facet(&page.Tools, l.Tool)
		facet(&page.Statuses, l.Status)
		if !q.matches(l) {
			continue
		}
		if page.Total >= q.Offset && len(page.Entries) < limit {
			page.Entries = append(page.Entries, entryOf(l))
		}
		page.Total++
	}
	for _, l := range [][]string{page.Sites, page.Clients, page.Tools, page.Statuses} {
		slices.Sort(l)
	}
	return page, nil
}

func (q ActivityQuery) matches(l auditLine) bool {
	switch {
	case q.Site != "" && l.Site != q.Site,
		q.Client != "" && l.Client != q.Client,
		q.Tool != "" && l.Tool != q.Tool,
		q.Status != "" && l.Status != q.Status,
		q.RunID != "" && l.RunID != q.RunID,
		!q.Since.IsZero() && l.Time.Before(q.Since),
		!q.Until.IsZero() && !l.Time.Before(q.Until):
		return false
	}
	return true
}

// readAuditFile reads the lines of one log file. A missing file is no lines
// and exists false; a file that is not a regular file is refused (a link in
// the config folder must not make the app read elsewhere). Lines that do
// not parse, or are too long, are counted and skipped.
func readAuditFile(path string) ([]auditLine, int, bool, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	if !fi.Mode().IsRegular() {
		return nil, 0, true, errors.New("the activity log is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, true, err
	}
	defer f.Close()
	var r io.Reader = f
	partial := false
	if size := fi.Size(); size > activityMaxFile {
		if _, err := f.Seek(size-activityMaxFile, io.SeekStart); err != nil {
			return nil, 0, true, err
		}
		partial = true
	}
	br := bufio.NewReaderSize(io.LimitReader(r, activityMaxFile), 64<<10)
	var out []auditLine
	skipped := 0
	first := true
	for {
		line, tooLong, err := readLine(br)
		if len(line) > 0 || tooLong {
			switch {
			case first && partial:
				// The read started inside a line.
			case tooLong:
				skipped++
			default:
				var l auditLine
				if json.Unmarshal(line, &l) != nil || l.Tool == "" || l.Time.IsZero() {
					skipped++
				} else {
					out = append(out, l)
				}
			}
		}
		first = false
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, true, err
		}
	}
	return out, skipped, true, nil
}

// readLine reads one line, without its line break. A line longer than
// activityMaxLine is consumed and reported as too long.
func readLine(br *bufio.Reader) ([]byte, bool, error) {
	var buf []byte
	tooLong := false
	for {
		chunk, isPrefix, err := br.ReadLine()
		if !tooLong {
			buf = append(buf, chunk...)
			if len(buf) > activityMaxLine {
				buf, tooLong = nil, true
			}
		}
		if err != nil {
			return bytes.TrimSpace(buf), tooLong, err
		}
		if !isPrefix {
			return bytes.TrimSpace(buf), tooLong, nil
		}
	}
}

// entryOf makes a log line safe to show: every string sanitized and cut,
// lists bounded, arguments re-indented and bounded.
func entryOf(l auditLine) ActivityEntry {
	e := ActivityEntry{
		Time: l.Time, Site: field(l.Site), Client: field(l.Client), Tool: field(l.Tool), RunID: field(l.RunID),
		Via: field(l.Via), Method: field(l.Method), Status: field(l.Status), Confirm: field(l.Confirm),
		Error: field(l.Error), NamesTotal: l.NamesTotal, DurationMS: l.DurationMS,
		Doctypes: fieldList(l.Doctypes), Names: fieldList(l.Names),
	}
	if len(l.Args) > 0 && string(l.Args) != "null" {
		var v any
		if json.Unmarshal(l.Args, &v) == nil {
			if b, err := json.MarshalIndent(sanitizeJSON(v), "", "  "); err == nil {
				e.Args = string(b)
			}
		}
		if len(e.Args) > activityMaxArgs {
			cut := activityMaxArgs
			for cut > 0 && !utf8.RuneStart(e.Args[cut]) {
				cut--
			}
			e.Args = e.Args[:cut] + "\n…"
		}
	}
	return e
}

func field(s string) string {
	s = text.Sanitize(s)
	if utf8.RuneCountInString(s) > activityMaxField {
		s = string([]rune(s)[:activityMaxField]) + "…"
	}
	return s
}

func fieldList(l []string) []string {
	out := []string{}
	for i, v := range l {
		if i == activityMaxList {
			break
		}
		out = append(out, field(v))
	}
	return out
}

// sanitizeJSON sanitizes every string of a decoded JSON value, keys included.
func sanitizeJSON(v any) any {
	switch t := v.(type) {
	case string:
		return text.Sanitize(t)
	case []any:
		for i := range t {
			t[i] = sanitizeJSON(t[i])
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[strings.TrimSpace(text.Sanitize(k))] = sanitizeJSON(x)
		}
		return out
	}
	return v
}
