package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
	"github.com/spf13/pflag"
)

// mcpTSites writes a config with a read-only site "Prod" and a writable
// site "dev" on two fake sites, and sets the `ffc mcp` flags for the test.
func mcpTSites(t *testing.T, sites []string, all bool, site string) (prod, dev *frappetest.Site, cfgPath string) {
	t.Helper()
	prod, dev = frappetest.New(t), frappetest.New(t)
	for _, s := range []*frappetest.Site{prod, dev} {
		s.Add("ToDo", map[string]interface{}{"name": "TD-1", "description": "a"})
	}
	creds := fmt.Sprintf("    api_key: %q\n    api_secret: %q\n", frappetest.APIKey, frappetest.APISecret)
	body := fmt.Sprintf("default_site: dev\nsites:\n  Prod:\n    url: %q\n%s    mcp:\n      read_only: true\n  dev:\n    url: %q\n%s",
		prod.URL, creds, dev.URL, creds)
	cfgPath = filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	prevCfg, prevSite, prevList, prevAll := configPath, siteName, mcpSiteList, mcpAllSites
	t.Cleanup(func() { configPath, siteName, mcpSiteList, mcpAllSites = prevCfg, prevSite, prevList, prevAll })
	configPath, siteName, mcpSiteList, mcpAllSites = cfgPath, site, sites, all
	return prod, dev, cfgPath
}

func mcpTStart(t *testing.T) *server.MCPServer {
	t.Helper()
	s, closeEnv, err := startMCP(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeEnv)
	return s
}

func TestMCPMultiSite(t *testing.T) {
	prod, dev, cfgPath := mcpTSites(t, []string{"prod", "dev"}, false, "")
	s := mcpTStart(t)

	// Write tools are registered because dev is writable, and every site
	// tool requires a site.
	tools := s.ListTools()
	del, ok := tools["delete_doc"]
	if !ok {
		t.Fatal("delete_doc not registered")
	}
	if p, _ := del.Tool.InputSchema.Properties["site"].(map[string]any); p == nil || !contains(del.Tool.InputSchema.Required, "site", false) {
		t.Errorf("delete_doc schema: %+v", del.Tool.InputSchema)
	}
	if _, ok := tools["list_sites"].Tool.InputSchema.Properties["site"]; ok {
		t.Error("list_sites takes a site")
	}

	mcpTErr(t, s, "get_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1"}, `site is required: this MCP server serves "dev", "Prod"`)
	mcpTErr(t, s, "get_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "site": "staging"}, `site "staging" is not served`)

	// A write to the read-only site is refused and sends nothing.
	before := len(prod.Requests())
	mcpTErr(t, s, "delete_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "site": "PROD"}, "MCP is read-only for this site")
	if len(prod.Requests()) != before {
		t.Error("the refused write reached the read-only site")
	}
	if _, ok := prod.Doc("ToDo", "TD-1"); !ok {
		t.Error("TD-1 deleted on Prod")
	}
	// Reads work on both; the write goes to the site named.
	mcpTOK(t, s, "get_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "site": "Prod"})
	mcpTOK(t, s, "delete_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "site": "dev"})
	if _, ok := dev.Doc("ToDo", "TD-1"); ok {
		t.Error("TD-1 still on dev")
	}
	if _, ok := prod.Doc("ToDo", "TD-1"); !ok {
		t.Error("the delete reached Prod")
	}

	var listed []map[string]interface{}
	if err := json.Unmarshal([]byte(mcpTOK(t, s, "list_sites", nil)), &listed); err != nil {
		t.Fatal(err)
	}
	want := `[{"auth":"api_key","default":true,"name":"dev","read_only":false,"url":"` + dev.URL + `"},{"auth":"api_key","default":false,"name":"Prod","read_only":true,"url":"` + prod.URL + `"}]`
	if b, _ := json.Marshal(listed); string(b) != want {
		t.Errorf("list_sites = %s\nwant        %s", b, want)
	}
	if strings.Contains(fmt.Sprint(listed), frappetest.APISecret) {
		t.Error("list_sites shows a secret")
	}

	// The audit log names the site of every call.
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(cfgPath), auditFileName))
	if err != nil {
		t.Fatal(err)
	}
	var sites []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var r auditRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		sites = append(sites, r.Tool+"@"+r.Site+":"+r.Status)
	}
	if got := strings.Join(sites, " "); got != "get_doc@:invalid get_doc@:invalid delete_doc@Prod:denied get_doc@Prod:ok delete_doc@dev:ok list_sites@:ok" {
		t.Errorf("audit = %s", got)
	}
}

// A read-only policy on every served site leaves only read tools.
func TestMCPMultiSiteAllReadOnly(t *testing.T) {
	mcpTSites(t, []string{"Prod"}, false, "")
	if _, ok := mcpTStart(t).ListTools()["delete_doc"]; ok {
		t.Error("delete_doc registered for a read-only site")
	}
}

// One served site keeps today's tools: no site argument, though a matching
// one is accepted.
func TestMCPSingleSiteUnchanged(t *testing.T) {
	mcpTSites(t, nil, false, "")
	s := mcpTStart(t)
	if _, ok := s.ListTools()["get_doc"].Tool.InputSchema.Properties["site"]; ok {
		t.Error("single-site get_doc takes a site")
	}
	mcpTOK(t, s, "get_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1"})
	mcpTOK(t, s, "get_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "site": "dev"})
	mcpTErr(t, s, "get_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "site": "Prod"}, `site "Prod" is not served`)
}

func TestMCPSites(t *testing.T) {
	for _, tc := range []struct {
		name  string
		sites []string
		all   bool
		site  string
		env   bool
		want  string // the sites, or the error
	}{
		{name: "default only", want: "dev"},
		{name: "--site only", site: "prod", want: "Prod"},
		{name: "list, default first", sites: []string{"Prod", "dev"}, want: "dev Prod"},
		{name: "list without the default", sites: []string{"Prod"}, want: "Prod"},
		{name: "--site picks the default", sites: []string{"dev", "Prod"}, site: "PROD", want: "Prod dev"},
		{name: "duplicates", sites: []string{"dev", "DEV"}, want: "dev"},
		{name: "all", all: true, want: "dev Prod"},
		{name: "unknown", sites: []string{"dev", "staging"}, want: `site "staging" not found`},
		{name: "--site outside the list", sites: []string{"dev"}, site: "Prod", want: "--site Prod is not one of the served sites"},
		{name: "both", sites: []string{"dev"}, all: true, want: "use --sites or --all-sites, not both"},
		{name: "env credentials", all: true, env: true, want: "FFC_API_KEY and FFC_API_SECRET apply to one site"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mcpTSites(t, tc.sites, tc.all, tc.site)
			if tc.env {
				t.Setenv("FFC_API_KEY", "k")
				t.Setenv("FFC_API_SECRET", "s")
			}
			sites, err := mcpSites()
			got := strings.Join(sites, " ")
			if err != nil {
				got = err.Error()
			}
			if !strings.Contains(got, tc.want) || (err == nil && got != tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDaemonArgsCarryTheSites(t *testing.T) {
	prevFlags := mcpFlags
	t.Cleanup(func() { mcpFlags = prevFlags })
	mcpFlags = config.MCPPolicy{DenyDoctypes: []string{`a,"b"`}}
	got := strings.Join(daemonArgs([]string{"dev", "Prod", "x,y"}, 1), " ")
	want := `mcp --port 1 --site dev --sites=dev --sites=Prod --sites="x,y" --deny-doctypes="a,""b"""`
	if got != want {
		t.Errorf("args = %s\nwant   %s", got, want)
	}
	// The child reads them back unchanged.
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	sites := fs.StringSlice("sites", nil, "")
	deny := fs.StringSlice("deny-doctypes", nil, "")
	fs.String("site", "", "")
	fs.Int("port", 0, "")
	if err := fs.Parse(daemonArgs([]string{"dev", "Prod", "x,y"}, 1)[1:]); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*sites, "|") != "dev|Prod|x,y" || strings.Join(*deny, "|") != `a,"b"` {
		t.Errorf("parsed sites %q, deny %q", *sites, *deny)
	}
}
