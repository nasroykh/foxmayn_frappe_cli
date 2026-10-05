//go:build contract

package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// contractCache pins what the local cache relies on: the DocType and report
// lists come back whole with limit_page_length=0 (reports with ref_doctype),
// a cached schema prints exactly what a fetch prints, and completion then
// offers the site's names and fields.
func contractCache(t *testing.T, sc *config.SiteConfig) {
	cacheTEnv(t)
	cfg := contractConfig(t, sc)

	r := runFFC(t, cfg, "", "--json", "cache", "warm", "--doctypes", contractDT)
	var w struct {
		Doctypes, Reports int
		Schemas           []string
	}
	if r.Err != nil || json.Unmarshal([]byte(r.Stdout), &w) != nil {
		t.Fatalf("cache warm: %v\n%s%s", r.Err, r.Stdout, r.Stderr)
	}
	// A bench has hundreds of DocTypes and reports; more than one page of
	// the API's default 20 proves the whole list came back.
	if w.Doctypes < 100 || w.Reports < 20 || len(w.Schemas) != 1 {
		t.Errorf("warm: %+v", w)
	}
	site, err := config.Load("contract", cfg)
	if err != nil {
		t.Fatal(err)
	}
	refs := 0
	for _, it := range readListCache(site, "Report", time.Now()) {
		if it.RefDoctype != "" {
			refs++
		}
	}
	if refs == 0 {
		t.Error("no cached report has its ref_doctype")
	}

	fresh := runFFC(t, cfg, "", "--json", "get-schema", "-d", contractDT, "--refresh")
	cached := runFFC(t, cfg, "", "--json", "get-schema", "-d", contractDT)
	if fresh.Err != nil || cached.Err != nil || fresh.Stdout != cached.Stdout || fresh.Stderr != cached.Stderr {
		t.Errorf("cached get-schema differs from a fetch: %v %v", fresh.Err, cached.Err)
	}
	tf := runFFC(t, cfg, "", "get-schema", "-d", contractDT, "--refresh")
	tc := runFFC(t, cfg, "", "get-schema", "-d", contractDT)
	if tf.Stdout != tc.Stdout {
		t.Errorf("cached table differs:\n%s\n---\n%s", tf.Stdout, tc.Stdout)
	}

	r = runFFC(t, "", "", "__complete", "--config", cfg, "list-docs", "-d", "FFC Contract")
	if !strings.Contains(r.Stdout, contractDT+"\n") || !strings.Contains(r.Stdout, contractChild+"\n") {
		t.Errorf("doctype completion: %q", r.Stdout)
	}
	r = runFFC(t, "", "", "__complete", "--config", cfg, "list-docs", "-d", contractDT, "--fields", "name,custom_")
	if !strings.Contains(r.Stdout, "name,custom_note\n") {
		t.Errorf("fields completion: %q", r.Stdout)
	}
}
