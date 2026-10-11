package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// policyHost is a host that answers the policy dialog and records what it
// was asked.
type policyHost struct {
	fakeHost
	answer  bool
	asked   [][]PolicyChange
	removes []string
}

func (h *policyHost) ConfirmPolicyChange(site string, opens []PolicyChange) (bool, error) {
	h.asked = append(h.asked, opens)
	return h.answer, nil
}

func (h *policyHost) ConfirmRemoveRules(site string) (bool, error) {
	h.removes = append(h.removes, site)
	return h.answer, nil
}

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

func policySites(t *testing.T, answer bool) (*SitesService, *policyHost, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(policyConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &policyHost{answer: answer}
	return NewSitesService(h, path), h, path
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

func TestSavePolicyNarrowingWritesWithoutAsking(t *testing.T) {
	s, h, path := policySites(t, false)
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
	got, err := s.SavePolicy("prod", base, want)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.asked) != 0 {
		t.Fatalf("asked for a narrowing change: %v", h.asked)
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

func TestSavePolicyOpeningAsksAndDecliningWritesNothing(t *testing.T) {
	s, h, path := policySites(t, false)
	before, _ := os.ReadFile(path)
	base, _ := s.GetPolicy("prod")
	loose := SitePolicy{} // read only off, User no longer refused
	_, err := s.SavePolicy("prod", base, loose)
	if errorCode(t, err) != CodeCancelled {
		t.Fatalf("declined: %v", err)
	}
	if len(h.asked) != 1 || kinds(h.asked[0]) != "readOnlyOff= denyDoctypesRemoved=User" {
		t.Fatalf("asked %v", h.asked)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatalf("a declined change was written:\n%s", after)
	}

	h.answer = true
	got, err := s.SavePolicy("prod", base, loose)
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
}

// A host without the native dialog cannot loosen a site; it can narrow one.
func TestSavePolicyWithoutDialogRefusesOpening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte(policyConfig), 0o600)
	s := NewSitesService(&fakeHost{}, path)
	base, _ := s.GetPolicy("prod")
	if _, err := s.SavePolicy("prod", base, SitePolicy{}); errorCode(t, err) != CodeUnavailable {
		t.Fatalf("opening without a dialog: %v", err)
	}
	if _, err := s.SavePolicy("dev", SitePolicy{}, SitePolicy{ReadOnly: true}); err != nil {
		t.Fatalf("narrowing without a dialog: %v", err)
	}
}

// The screen's base must match the file: a change made elsewhere is never
// overwritten unseen, and an approved change is refused when the file moved
// on while the dialog was open.
func TestSavePolicyRefusesStaleBase(t *testing.T) {
	s, h, path := policySites(t, true)
	base, _ := s.GetPolicy("prod")
	if _, err := s.SavePolicy("prod", SitePolicy{}, base); errorCode(t, err) != CodeInvalid {
		t.Fatalf("stale base: %v", err)
	}

	// The file changes while the dialog is open.
	h.answer = true
	s2 := NewSitesService(&changingHost{policyHost: h, path: path}, path)
	if _, err := s2.SavePolicy("prod", base, SitePolicy{}); errorCode(t, err) != CodeInvalid {
		t.Fatalf("file changed during the dialog: %v", err)
	}
	if site, _ := config.LoadSite("prod", path); site.MCP == nil || site.MCP.Confirm != "always" {
		t.Fatalf("the other change was overwritten: %+v", site.MCP)
	}
}

// changingHost edits the file while its dialog is open.
type changingHost struct {
	*policyHost
	path string
}

func (h *changingHost) ConfirmPolicyChange(site string, opens []PolicyChange) (bool, error) {
	err := config.Edit(h.path, func(f *config.File) error {
		return f.SetSiteMCP(site, &config.MCPPolicy{ReadOnly: true, Confirm: "always"})
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

func TestSavePolicyUnknownSite(t *testing.T) {
	s, _, _ := policySites(t, true)
	if _, err := s.SavePolicy("nope", SitePolicy{}, SitePolicy{ReadOnly: true}); errorCode(t, err) != CodeNotFound {
		t.Fatalf("unknown site: %v", err)
	}
}

func TestPolicyDialogText(t *testing.T) {
	txt := policyChangeText("prod", []PolicyChange{
		{Kind: policyReadOnlyOff, Values: []string{}},
		{Kind: policyConfirmLooser, Values: []string{"if-supported", "never"}},
		{Kind: policyDenyDoctypesRemoved, Values: []string{"User", "Role\x07"}},
		{Kind: "future", Values: []string{"x"}},
	})
	for _, want := range []string{"prod", "Read only is turned off", "“when the app can ask” to “never”", "User", "Another change"} {
		if !strings.Contains(txt, want) {
			t.Errorf("missing %q in:\n%s", want, txt)
		}
	}
	if strings.ContainsRune(txt, '\x07') {
		t.Error("a control character reached the dialog")
	}
	many := make([]string, policyDialogValues+3)
	for i := range many {
		many[i] = "D" + strings.Repeat("x", i)
	}
	if txt := policyChangeText("p", []PolicyChange{{Kind: policyAllowDoctypesAdded, Values: many}}); !strings.Contains(txt, "and 3 more") {
		t.Errorf("long list not cut:\n%s", txt)
	}
}

func TestQuestionButtons(t *testing.T) {
	win := questionButtons("windows", "Open access", "Cancel", false)
	if win[0].label != "Yes" || !win[0].yes || win[1].label != "No" || !win[1].isDefault || win[0].isDefault {
		t.Fatalf("windows: %+v", win)
	}
	if mac := questionButtons("darwin", "Attach", "Cancel", true); mac[0].label != "Attach" || !mac[0].isDefault || mac[1].label != "Cancel" || !mac[1].isCancel {
		t.Fatalf("darwin: %+v", mac)
	}
}

func TestPolicyDialogPutsUnlocksFirst(t *testing.T) {
	txt := policyChangeText("p", []PolicyChange{
		{Kind: policyAllowDoctypesAdded, Values: []string{"ToDo"}},
		{Kind: policySensitiveUnlocked, Values: []string{"User"}},
	})
	if strings.Index(txt, "Sensitive") > strings.Index(txt, "in addition") || !strings.Contains(txt, "do not open access") {
		t.Fatalf("order or warning:\n%s", txt)
	}
	if got := policyText("a\u202eb\u200bc"); got != "abc" {
		t.Fatalf("policyText %q", got)
	}
}

func TestSavePolicyBusyWhileAsking(t *testing.T) {
	s, _, _ := policySites(t, true)
	policyMu.Lock()
	_, err := s.SavePolicy("dev", SitePolicy{}, SitePolicy{ReadOnly: true})
	policyMu.Unlock()
	if errorCode(t, err) != CodeUnavailable {
		t.Fatalf("busy: %v", err)
	}
}

func TestPolicyRefusesWhatItCannotShow(t *testing.T) {
	for name, mcp := range map[string]string{
		"blank":  "      deny_doctypes: [\" ToDo\", \"\\u00a0\"]\n",
		"anchor": "      deny_doctypes: &dd [User]\n",
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		os.WriteFile(path, []byte("sites:\n  a:\n    url: https://a\n    mcp:\n"+mcp), 0o600)
		s := NewSitesService(&policyHost{answer: true}, path)
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
		if _, err := s.SavePolicy("a", base, next); errorCode(t, err) != CodeInvalid {
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

func TestRemoveSiteWithRulesAsks(t *testing.T) {
	ctx := context.Background()

	// Declined: nothing is removed.
	s, h, _ := policySites(t, false)
	_, err := s.Remove(ctx, "prod")
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeCancelled || e.Key != "policy.declined" {
		t.Fatalf("declined remove: %v", err)
	}
	if !slices.Equal(h.removes, []string{"prod"}) {
		t.Fatalf("asked %v", h.removes)
	}
	if _, err := s.GetPolicy("prod"); err != nil {
		t.Fatalf("site gone after a declined remove: %v", err)
	}

	// A site without rules is removed without a question.
	if _, err := s.Remove(ctx, "dev"); err != nil {
		t.Fatal(err)
	}
	if len(h.removes) != 1 {
		t.Fatalf("asked for a site without rules: %v", h.removes)
	}

	// Confirmed: the site and its rules are gone.
	s, h, path := policySites(t, true)
	if _, err := s.Remove(ctx, "prod"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "prod:") || len(h.removes) != 1 {
		t.Fatalf("site kept or not asked (%v):\n%s", h.removes, b)
	}

	// A host that cannot ask cannot remove a site with rules.
	s, _, _ = policySites(t, true)
	s.host = &fakeHost{}
	if _, err := s.Remove(ctx, "prod"); !errors.As(err, &e) || e.Key != "policy.cannotAskRemove" {
		t.Fatalf("remove without a dialog: %v", err)
	}
}
