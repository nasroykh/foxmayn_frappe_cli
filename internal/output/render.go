package output

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"go.yaml.in/yaml/v3"
)

// Format is an output format chosen with --output.
type Format string

// Output formats. FormatTable is each command's own human-readable view; the
// others are machine formats rendered here.
const (
	FormatTable  Format = "table"
	FormatJSON   Format = "json"
	FormatNDJSON Format = "ndjson"
	FormatCSV    Format = "csv"
	FormatTSV    Format = "tsv"
	FormatYAML   Format = "yaml"
)

// Formats lists every format, in the order help texts show them.
var Formats = []Format{FormatTable, FormatJSON, FormatNDJSON, FormatCSV, FormatTSV, FormatYAML}

// ParseFormat returns the format named s (case-insensitive).
func ParseFormat(s string) (Format, error) {
	f := Format(strings.ToLower(strings.TrimSpace(s)))
	for _, known := range Formats {
		if f == known {
			return f, nil
		}
	}
	names := make([]string, len(Formats))
	for i, known := range Formats {
		names[i] = string(known)
	}
	return "", fmt.Errorf("unknown output format %q (use %s)", s, strings.Join(names, ", "))
}

// Normalize converts v to the generic JSON shape (map[string]any, []any,
// json.Number, string, bool, nil) by a JSON round trip, keeping number
// literals exact.
func Normalize(v interface{}) (interface{}, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encoding output: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out interface{}
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("encoding output: %w", err)
	}
	return out, nil
}

// Write renders v in a machine format. fields sets the CSV/TSV columns (as
// for tables: "x as y" is column y); without it a list uses the sorted union
// of its keys. clean strips control characters from CSV/TSV cells, for a
// terminal.
func Write(w io.Writer, f Format, v interface{}, fields []string, clean bool) error {
	if f == FormatJSON {
		// The value as given, so --json prints exactly what it always did
		// (struct field order included).
		return writeJSON(w, v)
	}
	v, err := Normalize(v)
	if err != nil {
		return err
	}
	switch f {
	case FormatYAML:
		b, err := yaml.Marshal(yamlNode(v))
		if err != nil {
			return fmt.Errorf("encoding YAML: %w", err)
		}
		_, err = w.Write(b)
		return err
	case FormatNDJSON, FormatCSV, FormatTSV:
		s := NewListStream(w, f, fields, clean)
		list, ok := v.([]interface{})
		if !ok {
			list = []interface{}{v}
		}
		if err := s.write(list); err != nil {
			return err
		}
		return s.Close()
	}
	return fmt.Errorf("format %q is not a machine format", f)
}

// yamlNode builds the YAML tree for a normalized value. yaml.Marshal would
// quote a json.Number as a string; here it stays a number with its literal.
func yamlNode(v interface{}) *yaml.Node {
	switch val := v.(type) {
	case map[string]interface{}:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			n.Content = append(n.Content, yamlNode(k), yamlNode(val[k]))
		}
		return n
	case []interface{}:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, item := range val {
			n.Content = append(n.Content, yamlNode(item))
		}
		return n
	case json.Number:
		tag := "!!int"
		if strings.ContainsAny(val.String(), ".eE") {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: val.String()}
	}
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprint(v)}
	}
	return &n
}

// writeJSON is the --json output: indented, like it always was.
func writeJSON(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	return nil
}

// ListStream writes list rows as they arrive (JSON, NDJSON, CSV, TSV), so a
// long paged list never sits in memory.
type ListStream struct {
	w      io.Writer
	f      Format
	fields []string
	clean  bool
	cols   []string // CSV/TSV columns, fixed by the first batch
	csv    *csv.Writer
	n      int
}

// NewListStream returns a stream for format f, which must not be table or
// YAML (see Streams).
func NewListStream(w io.Writer, f Format, fields []string, clean bool) *ListStream {
	s := &ListStream{w: w, f: f, fields: fields, clean: clean}
	if f == FormatCSV {
		s.csv = csv.NewWriter(w)
	}
	return s
}

// Streams reports whether f can be written row by row.
func Streams(f Format) bool {
	return f == FormatJSON || f == FormatNDJSON || f == FormatCSV || f == FormatTSV
}

// Rows writes one batch of rows.
func (s *ListStream) Rows(rows []map[string]interface{}) error {
	v, err := Normalize(rows)
	if err != nil {
		return err
	}
	list, _ := v.([]interface{})
	return s.write(list)
}

func (s *ListStream) write(list []interface{}) error {
	switch s.f {
	case FormatJSON:
		for _, item := range list {
			sep := ",\n"
			if s.n == 0 {
				sep = "[\n"
			}
			b, err := json.MarshalIndent(item, "  ", "  ")
			if err != nil {
				return fmt.Errorf("encoding JSON: %w", err)
			}
			if _, err := fmt.Fprintf(s.w, "%s  %s", sep, b); err != nil {
				return err
			}
			s.n++
		}
		return nil
	case FormatNDJSON:
		for _, item := range list {
			b, err := json.Marshal(item)
			if err != nil {
				return fmt.Errorf("encoding JSON: %w", err)
			}
			if _, err := fmt.Fprintf(s.w, "%s\n", b); err != nil {
				return err
			}
			s.n++
		}
		return nil
	}
	// CSV and TSV.
	if len(list) == 0 {
		return nil
	}
	if s.cols == nil {
		s.cols = columns(list, s.fields)
		if err := s.record(s.cols); err != nil {
			return err
		}
	}
	for _, item := range list {
		row := make([]string, len(s.cols))
		if m, ok := item.(map[string]interface{}); ok {
			for i, c := range s.cols {
				row[i] = cell(m[c])
			}
		} else {
			row[0] = cell(item)
		}
		if err := s.record(row); err != nil {
			return err
		}
		s.n++
	}
	return nil
}

// Record writes one CSV/TSV record as given: no columns from a first
// batch, no header. Commands that lay out their own rows (export) use it.
func (s *ListStream) Record(cells []string) error {
	return s.record(cells)
}

func (s *ListStream) record(fields []string) error {
	if s.clean {
		for i := range fields {
			fields[i] = text.Sanitize(fields[i])
		}
	}
	if s.csv != nil {
		if err := s.csv.Write(fields); err != nil {
			return err
		}
		s.csv.Flush()
		return s.csv.Error()
	}
	for i := range fields {
		fields[i] = tsvEscaper.Replace(fields[i])
	}
	_, err := fmt.Fprintln(s.w, strings.Join(fields, "\t"))
	return err
}

// Close ends the output: the closing bracket of a JSON array.
func (s *ListStream) Close() error {
	if s.f != FormatJSON {
		return nil
	}
	end := "\n]\n"
	if s.n == 0 {
		end = "[]\n"
	}
	_, err := io.WriteString(s.w, end)
	return err
}

// tsvEscaper keeps one record per line: tabs, newlines and backslashes in a
// value are written as \t, \n, \r and \\ (the PostgreSQL/MySQL text form).
var tsvEscaper = strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`)

// columns returns the CSV/TSV columns: the requested fields, else the sorted
// union of the rows' keys, else "value" for a list of scalars.
func columns(list []interface{}, fields []string) []string {
	if cols := tableColumns(nil, fields); len(cols) > 0 {
		return cols
	}
	seen := map[string]bool{}
	var cols []string
	for _, item := range list {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		for k := range m {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	if len(cols) == 0 {
		return []string{"value"}
	}
	sort.Strings(cols)
	return cols
}

// Cell is a value as CSV/TSV text, as ListStream writes it.
func Cell(v interface{}) string { return cell(v) }

// cell is a value as CSV/TSV text: numbers as sent, nested values as JSON,
// null as empty. No locale formatting: this is data, not display.
func cell(v interface{}) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case json.Number:
		return val.String()
	case bool:
		return strconv.FormatBool(val)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
