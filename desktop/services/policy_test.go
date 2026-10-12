package services

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

const policyConfig = `# sites
default_site: prod
sites:
  prod: # live
    url: https://prod.example
    api_key: k
    api_secret: s
    mcp:
      read_only: true
      deny_doctypes: [User]
  dev:
    url: https://dev.example
    api_key: k
    api_secret: s
`

func policySites(t *testing.T) (*SitesService, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(policyConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	return NewSitesService(&fakeHost{}, path), path
}

func kinds(cs []PolicyChange) string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Kind+"="+strings.Join(c.Values, ","))
	}
	return strings.Join(out, " ")
}

func TestComparePolicies(t *testing.T) {
	cases := []struct {
		name          string
		old, new      SitePolicy
		opens, narrow string
	}{
		{"same", SitePolicy{ReadOnly: true}, SitePolicy{ReadOnly: true}, "", ""},
		{"read only off", SitePolicy{ReadOnly: true}, SitePolicy{}, "readOnlyOff=", ""},
		{"read only on", SitePolicy{}, SitePolicy{ReadOnly: true}, "", "readOnlyOn="},
		{"never asks", SitePolicy{}, SitePolicy{Confirm: "never"}, "confirmLooser=if-supported,never", ""},
		{"always asks", SitePolicy{Confirm: "never"}, SitePolicy{Confirm: "always"}, "", "confirmStricter=never,always"},
		{"if-supported is the default", SitePolicy{Confirm: "if-supported"}, SitePolicy{}, "", ""},
		{"allow list set", SitePolicy{}, SitePolicy{AllowDoctypes: []string{"ToDo"}}, "", "allowDoctypesSet=ToDo"},
		{"allow list lifted", SitePolicy{AllowDoctypes: []string{"ToDo"}}, SitePolicy{}, "allowDoctypesLifted=ToDo serverMethodsEnabled=", ""},
		{"allow list widened and narrowed",
			SitePolicy{AllowMethods: []string{"a.b", "c.d"}}, SitePolicy{AllowMethods: []string{"c.d", "e.f"}},
			"allowMethodsAdded=e.f", "allowMethodsRemoved=a.b"},
		{"deny entry removed", SitePolicy{DenyDoctypes: []string{"User", "File"}}, SitePolicy{DenyDoctypes: []string{"File", "Role"}},
			"denyDoctypesRemoved=User", "denyDoctypesAdded=Role"},
		// An allow list that narrows can still unlock what ffc refuses by default.
		{"sensitive DocType unlocked", SitePolicy{}, SitePolicy{AllowDoctypes: []string{"ToDo", "user"}, AllowMethods: []string{"x.y"}},
			"sensitiveDoctypesUnlocked=user", "allowDoctypesSet=ToDo,user allowMethodsSet=x.y"},
		{"sensitive already listed", SitePolicy{AllowDoctypes: []string{"User"}, AllowMethods: []string{"x.y"}},
			SitePolicy{AllowDoctypes: []string{"ToDo", "User"}, AllowMethods: []string{"x.y"}},
			"allowDoctypesAdded=ToDo", ""},
		{"denied method unlocked", SitePolicy{}, SitePolicy{AllowMethods: []string{"frappe.*"}},
			"deniedMethodsUnlocked=frappe.*", "allowMethodsSet=frappe.*"},
		{"call_method enabled again", SitePolicy{AllowDoctypes: []string{"ToDo"}}, SitePolicy{AllowDoctypes: []string{"ToDo"}, AllowMethods: []string{"x.y"}},
			"serverMethodsEnabled=", "allowMethodsSet=x.y"},
		{"call_method blocked", SitePolicy{}, SitePolicy{AllowDoctypes: []string{"ToDo"}}, "", "allowDoctypesSet=ToDo"},
		// A different spelling is a different entry: it asks rather than not.
		{"case counts", SitePolicy{AllowTools: []string{"get_doc"}}, SitePolicy{AllowTools: []string{"GET_DOC"}},
			"allowToolsAdded=GET_DOC", "allowToolsRemoved=get_doc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pv := comparePolicies(c.old, c.new)
			if got := kinds(pv.Opens); got != c.opens {
				t.Errorf("opens %q, want %q", got, c.opens)
			}
			if got := kinds(pv.Narrows); got != c.narrow {
				t.Errorf("narrows %q, want %q", got, c.narrow)
			}
		})
	}
}

func TestCheckPolicy(t *testing.T) {
	bad := []SitePolicy{
		{Confirm: "sometimes"},
		{AllowTools: []string{"Get Doc"}},
		{AllowMethods: []string{"frappe/client"}},
		{DenyMethods: []string{"a b"}},
		{DenyDoctypes: []string{"User​"}},
		{AllowDoctypes: []string{"To\x01Do"}},
		{AllowDoctypes: []string{strings.Repeat("x", maxEntryChars+1)}},
	}
	for _, p := range bad {
		if _, err := checkPolicy(p); errorCode(t, err) != CodeInvalid {
			t.Errorf("%+v: %v", p, err)
		}
	}
	got, err := checkPolicy(SitePolicy{Confirm: " if-supported ", DenyDoctypes: []string{" User", "File", "User", ""}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Confirm != "" || strings.Join(got.DenyDoctypes, ",") != "File,User" {
		t.Fatalf("normalized %+v", got)
	}
}

func TestSavePolicyNarrowingNeedsNoConfirmation(t *testing.T) {
	s, path := policySites(t)
	base, err := s.GetPolicy("prod")
	if err != nil {
		t.Fatal(err)
	}
	if !base.ReadOnly || strings.Join(base.DenyDoctypes, ",") != "User" {
		t.Fatalf("read %+v", base)
	}
	want := base
	want.DenyDoctypes = []string{"File", "User"}
	want.Confirm = "always"
	got, err := s.SavePolicy("prod", base, want, false)
	if err != nil {
		t.Fatal(err)
	}
	if !got.equal(want) {
		t.Fatalf("stored %+v, want %+v", got, want)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "# live") || !strings.Contains(string(raw), "# sites") {
		t.Fatalf("comments lost:\n%s", raw)
	}
	// What ffc reads is what was saved.
	site, err := config.LoadSite("prod", path)
	if err != nil {
		t.Fatal(err)
	}
	if !policyOf(site.MCP).equal(want) {
		t.Fatalf("ffc reads %+v", site.MCP)
	}
}

// A change that opens access is saved only with opensConfirmed, which the
// screen sets after showing PreviewPolicy's opens; narrowing never needs it.
func TestSavePolicyOpeningNeedsConfirmation(t *testing.T) {
	s, path := policySites(t)
	before, _ := os.ReadFile(path)
	base, _ := s.GetPolicy("prod")
	loose := SitePolicy{} // read only off, User no longer refused
	_, err := s.SavePolicy("prod", base, loose, false)
	if errorCode(t, err) != CodeInvalid || errKey(err) != "policy.confirmNeeded" {
		t.Fatalf("unconfirmed opening: %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatalf("an unconfirmed change was written:\n%s", after)
	}

	got, err := s.SavePolicy("prod", base, loose, true)
	if err != nil {
		t.Fatal(err)
	}
	if !got.equal(SitePolicy{}) {
		t.Fatalf("stored %+v", got)
	}
	// An empty policy removes the mcp key.
	if site, _ := config.LoadSite("prod", path); site.MCP != nil {
		t.Fatalf("mcp kept: %+v", site.MCP)
	}

	// Confirming a change that only narrows is harmless.
	if _, err := s.SavePolicy("dev", SitePolicy{}, SitePolicy{ReadOnly: true}, true); err != nil {
		t.Fatalf("narrowing with confirmation: %v", err)
	}
	if _, err := s.SavePolicy("dev", SitePolicy{ReadOnly: true}, SitePolicy{ReadOnly: true, Confirm: "always"}, false); err != nil {
		t.Fatalf("narrowing without confirmation: %v", err)
	}
}

// The screen's base must match the file: a change made elsewhere is never
// overwritten unseen.
func TestSavePolicyRefusesStaleBase(t *testing.T) {
	s, path := policySites(t)
	base, _ := s.GetPolicy("prod")
	if _, err := s.SavePolicy("prod", SitePolicy{}, base, true); errorCode(t, err) != CodeInvalid {
		t.Fatalf("stale base: %v", err)
	}

	// The file changes after the screen read it.
	err := config.Edit(path, func(f *config.File) error {
		return f.SetSiteMCP("prod", &config.MCPPolicy{ReadOnly: true, Confirm: "always"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePolicy("prod", base, SitePolicy{}, true); errorCode(t, err) != CodeInvalid {
		t.Fatalf("file changed since the read: %v", err)
	}
	if site, _ := config.LoadSite("prod", path); site.MCP == nil || site.MCP.Confirm != "always" {
		t.Fatalf("the other change was overwritten: %+v", site.MCP)
	}
}

func TestSavePolicyUnknownSite(t *testing.T) {
	s, _ := policySites(t)
	if _, err := s.SavePolicy("nope", SitePolicy{}, SitePolicy{ReadOnly: true}, false); errorCode(t, err) != CodeNotFound {
		t.Fatalf("unknown site: %v", err)
	}
}

func TestPolicyRefusesWhatItCannotShow(t *testing.T) {
	for name, mcp := range map[string]string{
		"blank":  "      deny_doctypes: [\" ToDo\", \"\\u00a0\"]\n",
		"anchor": "      deny_doctypes: &dd [User]\n",
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		os.WriteFile(path, []byte("sites:\n  a:\n    url: https://a\n    mcp:\n"+mcp), 0o600)
		s := NewSitesService(&fakeHost{}, path)
		base, err := s.GetPolicy("a")
		if name == "blank" {
			if errorCode(t, err) != CodeInvalid {
				t.Errorf("blank: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		next := base
		next.DenyDoctypes = append(next.DenyDoctypes, "File")
		if _, err := s.SavePolicy("a", base, next, true); errorCode(t, err) != CodeInvalid {
			t.Errorf("anchor: %v", err)
		}
	}
	if validDoctype("Customer, Supplier") || validDoctype("Sales  Invoice") || !validDoctype("Sales Invoice") {
		t.Error("validDoctype")
	}
}

// SitePolicy carries every field of ffc's policy: a field ffc adds would
// otherwise be deleted by every save from this screen.
func TestSitePolicyCoversMCPPolicy(t *testing.T) {
	if n := reflect.TypeOf(config.MCPPolicy{}).NumField(); n != reflect.TypeOf(SitePolicy{}).NumField() {
		t.Fatalf("config.MCPPolicy has %d fields, SitePolicy %d: map the new one in policyOf, config and comparePolicies", n, reflect.TypeOf(SitePolicy{}).NumField())
	}
}

// A site with rules is removed without a question (the screen says so
// first), and its rules go with it.
func TestRemoveSiteWithRules(t *testing.T) {
	s, path := policySites(t)
	if _, err := s.Remove(context.Background(), "prod"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "prod:") || strings.Contains(string(b), "deny_doctypes") {
		t.Fatalf("site or rules kept:\n%s", b)
	}
	if _, err := s.GetPolicy("prod"); errorCode(t, err) != CodeNotFound {
		t.Fatalf("GetPolicy after remove: %v", err)
	}
}
