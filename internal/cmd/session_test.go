package cmd

import (
	"encoding/json"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

func TestCLIEndsPasswordSessions(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"get-doc", []string{"get-doc", "-d", "ToDo", "-n", "a"}, false},
		{"get-doc error", []string{"get-doc", "-d", "ToDo", "-n", "missing"}, true},
		{"ping", []string{"ping"}, false},
		{"bulk-delete", []string{"bulk-delete", "-d", "ToDo", "--names", "a,b", "--yes"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			site := frappetest.New(t)
			site.Add("ToDo", map[string]interface{}{"name": "a"}, map[string]interface{}{"name": "b"})
			r := runFFC(t, fakeConfig(t, site, "password"), "", append([]string{"--json"}, tc.args...)...)
			if (r.Err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", r.Err, tc.wantErr)
			}
			if in, out := site.Logins(), site.Logouts(); in != 1 || out != 1 {
				t.Errorf("logins = %d, logouts = %d; want 1 and 1", in, out)
			}
		})
	}
}

func TestSiteListJSONDefaultIsBool(t *testing.T) {
	out, err := runCLI(t, "http://127.0.0.1:1", "site", "list")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got := map[string]interface{}{}
	for _, r := range rows {
		got[r["name"].(string)] = r["default"]
	}
	if got["t"] != true || got["other"] != false {
		t.Errorf("default flags = %v, want t=true other=false", got)
	}
}
