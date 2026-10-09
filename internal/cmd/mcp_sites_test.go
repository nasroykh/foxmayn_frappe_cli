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

// mcpTSites writes a config with a read-only site "Prod" (OAuth) and a
// writable site "dev" (API key) on two fake sites, and sets the `ffc mcp`
// flags for the test. The sites sign in differently, so a request sent with
// the other site's credentials shows in their headers.
func mcpTSites(t *testing.T, sites []string, all bool, site string) (prod, dev *frappetest.Site, cfgPath string) {
	t.Helper()
	prod, dev = frappetest.New(t), frappetest.New(t)
	for _, s := range []*frappetest.Site{prod, dev} {
		s.Add("ToDo", map[string]interface{}{"name": "TD-1", "description": "a"})
	}
	cfgPath = filepath.Join(t.TempDir(), "config.yaml")
	mcpTWriteConfig(t, cfgPath, fmt.Sprintf("default_site: dev\nsites:\n  Prod:\n    url: %q\n    access_token: %q\n    mcp:\n      read_only: true\n  dev:\n    url: %q\n    api_key: %q\n    api_secret: %q\n",
		prod.URL, frappetest.Token, dev.URL, frappetest.APIKey, frappetest.APISecret))
	prevCfg, prevSite, prevList, prevAll := configPath, siteName, mcpSiteList, mcpAllSites
	t.Cleanup(func() { configPath, siteName, mcpSiteList, mcpAllSites = prevCfg, prevSite, prevList, prevAll })
	configPath, siteName, mcpSiteList, mcpAllSites = cfgPath, site, sites, all
	return prod, dev, cfgPath
}

func mcpTWriteConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// mcpTAuth fails the test unless every request to s was signed with want.
func mcpTAuth(t *testing.T, s *frappetest.Site, want string) {
	t.Helper()
	for _, r := range s.Requests() {
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, want) {
			t.Errorf("%s %s signed with %q, want %s…", r.Method, r.Path, strings.SplitN(got, " ", 2)[0], want)
		}
	}
}

func mcpTStart(t *testing.T) *server.MCPServer {
	t.Helper()
	s, _, closeEnv, err := startMCP(context.Background(), mcpOptionsFromFlags())
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
	want := `[{"auth":"api_key","name":"dev","read_only":false,"url":"` + dev.URL + `"},{"auth":"oauth","name":"Prod","read_only":true,"url":"` + prod.URL + `"}]`
	if b, _ := json.Marshal(listed); string(b) != want {
		t.Errorf("list_sites = %s\nwant        %s", b, want)
	}
	if s := fmt.Sprint(listed); strings.Contains(s, frappetest.APISecret) || strings.Contains(s, frappetest.Token) {
		t.Error("list_sites shows a secret")
	}
	// Each site only ever got its own credentials.
	mcpTAuth(t, prod, "Bearer ")
	mcpTAuth(t, dev, "token ")

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

// A read-only policy on every served site leaves only read tools, each
// still taking a site.
func TestMCPMultiSiteAllReadOnly(t *testing.T) {
	prod, dev, cfgPath := mcpTSites(t, []string{"Prod", "dev"}, false, "")
	mcpTWriteConfig(t, cfgPath, fmt.Sprintf("sites:\n  Prod:\n    url: %q\n    access_token: %q\n    mcp: {read_only: true}\n  dev:\n    url: %q\n    access_token: %q\n    mcp: {read_only: true}\n",
		prod.URL, frappetest.Token, dev.URL, frappetest.Token))
	tools := mcpTStart(t).ListTools()
	if _, ok := tools["delete_doc"]; ok {
		t.Error("delete_doc registered when every site is read-only")
	}
	if _, ok := tools["get_doc"].Tool.InputSchema.Properties["site"]; !ok {
		t.Error("get_doc takes no site")
	}
}

// A site removed from the config after the start does not resolve to
// another site whose name only differs in case.
func TestMCPMultiSiteRenamedAway(t *testing.T) {
	prod, dev, cfgPath := mcpTSites(t, []string{"Prod", "dev"}, false, "")
	s := mcpTStart(t)
	other := frappetest.New(t)
	other.Add("ToDo", map[string]interface{}{"name": "TD-1"})
	mcpTWriteConfig(t, cfgPath, fmt.Sprintf("default_site: dev\nsites:\n  PROD:\n    url: %q\n    api_key: %q\n    api_secret: %q\n  dev:\n    url: %q\n    api_key: %q\n    api_secret: %q\n",
		other.URL, frappetest.APIKey, frappetest.APISecret, dev.URL, frappetest.APIKey, frappetest.APISecret))
	mcpTErr(t, s, "delete_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "site": "Prod"}, `site "Prod" not found`)
	if len(other.Requests()) != 0 || len(prod.Requests()) != 0 {
		t.Error("the call reached a site")
	}
}

// Only the default site signs in at start: another that is down does not
// hold up or fail the start, and its calls fail on their own.
func TestMCPMultiSiteLazyStart(t *testing.T) {
	_, dev, cfgPath := mcpTSites(t, []string{"dev", "down"}, false, "")
	mcpTWriteConfig(t, cfgPath, fmt.Sprintf("default_site: dev\nsites:\n  down:\n    url: http://127.0.0.1:1\n    username: u\n    password: p\n  dev:\n    url: %q\n    api_key: %q\n    api_secret: %q\n",
		dev.URL, frappetest.APIKey, frappetest.APISecret))
	s := mcpTStart(t)
	mcpTOK(t, s, "get_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "site": "dev"})
	res := callTool(t, s, "get_doc", map[string]interface{}{"doctype": "ToDo", "name": "TD-1", "site": "down"})
	if !res.IsError {
		t.Error("a call to the unreachable site succeeded")
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
			o := mcpOptionsFromFlags()
			sites, err := []string(nil), cleanMCPOptions(&o, true, func(string) bool { return false })
			if err == nil {
				sites, err = mcpSites(o)
			}
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
	got := strings.Join(daemonArgs(mcpOptionsFromFlags(), []string{"dev", "Prod", "x,y"}, 1), " ")
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
	if err := fs.Parse(daemonArgs(mcpOptionsFromFlags(), []string{"dev", "Prod", "x,y"}, 1)[1:]); err != nil {
		t.Fatal(err)
	}
	if strings.Join(*sites, "|") != "dev|Prod|x,y" || strings.Join(*deny, "|") != `a,"b"` {
		t.Errorf("parsed sites %q, deny %q", *sites, *deny)
	}
}
