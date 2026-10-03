package output

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

func TestTableColumns(t *testing.T) {
	row := map[string]interface{}{"name": "x", "n": 3.0, "status": "Open"}
	cases := []struct {
		fields []string
		want   []string
	}{
		{nil, []string{"n", "name", "status"}},
		{[]string{"*"}, []string{"n", "name", "status"}},
		{[]string{"name", "status"}, []string{"name", "status"}},
		{[]string{"count(name) as n"}, []string{"n"}},
		{[]string{"COUNT(name) AS n"}, []string{"n"}},
		{[]string{"`tabToDo`.`status`"}, []string{"status"}},
		{[]string{"items.item_code"}, []string{"item_code"}},
	}
	for _, c := range cases {
		if got := tableColumns(row, c.fields); !reflect.DeepEqual(got, c.want) {
			t.Errorf("tableColumns(%v) = %v, want %v", c.fields, got, c.want)
		}
	}
}

func TestFormatValue(t *testing.T) {
	orig, origDate := config.ActiveFormat, config.ActiveDateFormat
	defer func() { config.ActiveFormat, config.ActiveDateFormat = orig, origDate }()
	config.ActiveFormat, config.ActiveDateFormat = config.FormatUS, config.FormatUSDate

	cases := []struct {
		key  string
		v    interface{}
		want string
	}{
		{"name", 1234.0, "1234"},
		{"grand_total", 1234.5, "1,234.50"},
		{"name", "2025-01-02", "2025-01-02"},
		{"posting_date", "2025-01-02", "01/02/2025"},
		{"description", "evil\x1b]0;title\x07text", "evil]0;titletext"},
		{"enabled", true, "true"},
	}
	for _, c := range cases {
		if got := formatValue(c.key, c.v); got != c.want {
			t.Errorf("formatValue(%q, %v) = %q, want %q", c.key, c.v, got, c.want)
		}
	}
	if got := formatValue("x", map[string]interface{}{"a": "\x1b[2J"}); strings.Contains(got, "\x1b") {
		t.Errorf("nested escape not stripped: %q", got)
	}
}
