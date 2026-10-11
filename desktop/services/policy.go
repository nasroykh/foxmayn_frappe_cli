package services

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/cmd"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
)

// A site's safety rules are its ffc MCP policy (sites.<name>.mcp in the ffc
// config): what every MCP client may do on the site, the Assistant and the
// apps connected through ffc mcp alike. The Safety screen edits them here.
//
// A change that opens access (read only turned off, an allow list lifted or
// widened, a deny entry removed, fewer questions) is confirmed in a native
// dialog before anything is written: the page can call SavePolicy directly,
// so a script in the web view must not be able to loosen a site by itself.
// A change that only narrows access is saved without a question.

// SitePolicy is a site's MCP policy as the Safety screen edits it. Empty
// lists mean no limit (allow) or nothing refused (deny); an empty Confirm is
// ffc's default, if-supported.
type SitePolicy struct {
	ReadOnly      bool     `json:"readOnly"`
	Confirm       string   `json:"confirm"`
	AllowTools    []string `json:"allowTools"`
	AllowDoctypes []string `json:"allowDoctypes"`
	DenyDoctypes  []string `json:"denyDoctypes"`
	AllowMethods  []string `json:"allowMethods"`
	DenyMethods   []string `json:"denyMethods"`
}

// PolicyChange is one difference between two policies. Kind is one of the
// policyChange* kinds; Values lists the entries concerned (for confirm, the
// old mode then the new one).
type PolicyChange struct {
	Kind   string   `json:"kind"`
	Values []string `json:"values"`
}

// PolicyPreview splits the differences of a change into what it opens and
// what it narrows.
type PolicyPreview struct {
	Opens   []PolicyChange `json:"opens"`
	Narrows []PolicyChange `json:"narrows"`
}

// Kinds of PolicyChange. "Lifted" removes an allow list (no limit any more),
// "Set" adds one where there was none.
const (
	policyReadOnlyOff         = "readOnlyOff"
	policyReadOnlyOn          = "readOnlyOn"
	policyConfirmLooser       = "confirmLooser"
	policyConfirmStricter     = "confirmStricter"
	policyAllowToolsAdded     = "allowToolsAdded"
	policyAllowToolsLifted    = "allowToolsLifted"
	policyAllowToolsSet       = "allowToolsSet"
	policyAllowToolsRemoved   = "allowToolsRemoved"
	policyAllowDoctypesAdded  = "allowDoctypesAdded"
	policyAllowDoctypesLifted = "allowDoctypesLifted"
	policyAllowDoctypesSet    = "allowDoctypesSet"
	policyAllowDoctypesRemove = "allowDoctypesRemoved"
	policyDenyDoctypesRemoved = "denyDoctypesRemoved"
	policyDenyDoctypesAdded   = "denyDoctypesAdded"
	policyAllowMethodsAdded   = "allowMethodsAdded"
	policyAllowMethodsLifted  = "allowMethodsLifted"
	policyAllowMethodsSet     = "allowMethodsSet"
	policyAllowMethodsRemoved = "allowMethodsRemoved"
	policyDenyMethodsRemoved  = "denyMethodsRemoved"
	policyDenyMethodsAdded    = "denyMethodsAdded"
	// Opening changes hidden inside an allow list that otherwise narrows:
	// ffc writes a sensitive DocType only when allow_doctypes lists it, calls
	// a denied method only when allow_methods lists it, and refuses every
	// call_method while allow_doctypes is set without allow_methods.
	policySensitiveUnlocked = "sensitiveDoctypesUnlocked"
	policyDeniedUnlocked    = "deniedMethodsUnlocked"
	policyMethodsEnabled    = "serverMethodsEnabled"
)

// PolicyConfirmer asks the person, in a native dialog the web view cannot
// click, whether a change that opens access to a site may be saved, and
// whether a site may be removed with its rules. A host without it cannot
// loosen a site's rules or remove a site that has some.
type PolicyConfirmer interface {
	ConfirmPolicyChange(site string, opens []PolicyChange) (bool, error)
	ConfirmRemoveRules(site string) (bool, error)
}

// toolNamePattern is the shape of an ffc tool name.
var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// policyMu keeps two saves from asking and writing at the same time.
var policyMu sync.Mutex

func policyOf(p *config.MCPPolicy) SitePolicy {
	if p == nil {
		return SitePolicy{}
	}
	return SitePolicy{
		ReadOnly: p.ReadOnly, Confirm: p.Confirm,
		AllowTools: cleanList(p.AllowTools), AllowDoctypes: cleanList(p.AllowDoctypes), DenyDoctypes: cleanList(p.DenyDoctypes),
		AllowMethods: cleanList(p.AllowMethods), DenyMethods: cleanList(p.DenyMethods),
	}
}

func (p SitePolicy) config() *config.MCPPolicy {
	return &config.MCPPolicy{
		ReadOnly: p.ReadOnly, Confirm: p.Confirm,
		AllowTools: p.AllowTools, AllowDoctypes: p.AllowDoctypes, DenyDoctypes: p.DenyDoctypes,
		AllowMethods: p.AllowMethods, DenyMethods: p.DenyMethods,
	}
}

// normalized trims, sorts and dedups the lists (empty lists become nil) and
// writes ffc's default confirm mode as "".
func (p SitePolicy) normalized() SitePolicy {
	for _, l := range []*[]string{&p.AllowTools, &p.AllowDoctypes, &p.DenyDoctypes, &p.AllowMethods, &p.DenyMethods} {
		*l = cleanList(*l)
	}
	p.Confirm = strings.TrimSpace(p.Confirm)
	if p.Confirm == config.ConfirmIfSupported {
		p.Confirm = ""
	}
	return p
}

func (p SitePolicy) equal(q SitePolicy) bool {
	p, q = p.normalized(), q.normalized()
	return p.ReadOnly == q.ReadOnly && p.Confirm == q.Confirm &&
		slices.Equal(p.AllowTools, q.AllowTools) && slices.Equal(p.AllowDoctypes, q.AllowDoctypes) &&
		slices.Equal(p.DenyDoctypes, q.DenyDoctypes) && slices.Equal(p.AllowMethods, q.AllowMethods) &&
		slices.Equal(p.DenyMethods, q.DenyMethods)
}

// checkPolicy validates and normalises a policy the person saves.
func checkPolicy(p SitePolicy) (SitePolicy, error) {
	p = p.normalized()
	switch p.Confirm {
	case "", config.ConfirmAlways, config.ConfirmNever:
	default:
		return SitePolicy{}, keyedInvalid("confirm", "policy.confirm")
	}
	lists := []struct {
		field string
		l     []string
		ok    func(string) bool
	}{
		{"allowTools", p.AllowTools, toolNamePattern.MatchString},
		{"allowDoctypes", p.AllowDoctypes, validDoctype},
		{"denyDoctypes", p.DenyDoctypes, validDoctype},
		// ffc refuses a method name with a slash or a space (checkMethodName).
		{"allowMethods", p.AllowMethods, validMethod},
		{"denyMethods", p.DenyMethods, validMethod},
	}
	for _, x := range lists {
		if len(x.l) > maxListEntries {
			return SitePolicy{}, keyedInvalid(x.field, "policy.tooMany")
		}
		for _, v := range x.l {
			if utf8.RuneCountInString(v) > maxEntryChars || hasControl(v) || text.Sanitize(v) != v || !x.ok(v) {
				return SitePolicy{}, keyedInvalid(x.field, "policy.badEntry", "entry", v)
			}
		}
	}
	return p, nil
}

// validDoctype refuses what reads as two names in a list or a dialog: a
// comma, a line break or a run of spaces.
func validDoctype(dt string) bool {
	return !strings.ContainsAny(dt, ",\n\r\t") && !strings.Contains(dt, "  ")
}

func validMethod(m string) bool {
	return !strings.ContainsAny(m, "/ \t")
}

// comparePolicies lists what going from old to new opens and narrows.
func comparePolicies(old, new SitePolicy) PolicyPreview {
	old, new = old.normalized(), new.normalized()
	var pv PolicyPreview
	open := func(kind string, v ...string) {
		pv.Opens = append(pv.Opens, PolicyChange{Kind: kind, Values: nonNilList(v)})
	}
	narrow := func(kind string, v ...string) {
		pv.Narrows = append(pv.Narrows, PolicyChange{Kind: kind, Values: nonNilList(v)})
	}

	switch {
	case old.ReadOnly && !new.ReadOnly:
		open(policyReadOnlyOff)
	case !old.ReadOnly && new.ReadOnly:
		narrow(policyReadOnlyOn)
	}
	if o, n := confirmRank(old.Confirm), confirmRank(new.Confirm); n < o {
		open(policyConfirmLooser, confirmName(old.Confirm), confirmName(new.Confirm))
	} else if n > o {
		narrow(policyConfirmStricter, confirmName(old.Confirm), confirmName(new.Confirm))
	}
	allow := func(o, n []string, added, lifted, set, removed string) {
		switch {
		case len(o) == 0 && len(n) == 0:
		case len(o) == 0:
			narrow(set, n...)
		case len(n) == 0:
			open(lifted, o...)
		default:
			if a := minus(n, o); len(a) > 0 {
				open(added, a...)
			}
			if r := minus(o, n); len(r) > 0 {
				narrow(removed, r...)
			}
		}
	}
	deny := func(o, n []string, removed, added string) {
		if r := minus(o, n); len(r) > 0 {
			open(removed, r...)
		}
		if a := minus(n, o); len(a) > 0 {
			narrow(added, a...)
		}
	}
	allow(old.AllowTools, new.AllowTools, policyAllowToolsAdded, policyAllowToolsLifted, policyAllowToolsSet, policyAllowToolsRemoved)
	allow(old.AllowDoctypes, new.AllowDoctypes, policyAllowDoctypesAdded, policyAllowDoctypesLifted, policyAllowDoctypesSet, policyAllowDoctypesRemove)
	deny(old.DenyDoctypes, new.DenyDoctypes, policyDenyDoctypesRemoved, policyDenyDoctypesAdded)
	allow(old.AllowMethods, new.AllowMethods, policyAllowMethodsAdded, policyAllowMethodsLifted, policyAllowMethodsSet, policyAllowMethodsRemoved)
	deny(old.DenyMethods, new.DenyMethods, policyDenyMethodsRemoved, policyDenyMethodsAdded)
	if u := unlocked(old.AllowDoctypes, new.AllowDoctypes, cmd.SensitiveDoctype, true); len(u) > 0 {
		open(policySensitiveUnlocked, u...)
	}
	if u := unlocked(old.AllowMethods, new.AllowMethods, cmd.UnlocksDeniedMethod, false); len(u) > 0 {
		open(policyDeniedUnlocked, u...)
	}
	if methodsBlocked(old) && !methodsBlocked(new) {
		open(policyMethodsEnabled)
	}
	pv.Opens, pv.Narrows = nonNilChanges(pv.Opens), nonNilChanges(pv.Narrows)
	return pv
}

// unlocked lists the entries of new that unlock something (by special) and
// that old did not already list. fold matches entries as ffc matches
// DocTypes, ignoring case.
func unlocked(old, new []string, special func(string) bool, fold bool) []string {
	var out []string
	for _, v := range new {
		if !special(v) {
			continue
		}
		had := false
		for _, o := range old {
			if o == v || fold && strings.EqualFold(o, v) {
				had = true
			}
		}
		if !had {
			out = append(out, v)
		}
	}
	return out
}

// methodsBlocked reports whether ffc refuses every call_method under p: an
// allow_doctypes list with no allow_methods list (a method could reach any
// DocType).
func methodsBlocked(p SitePolicy) bool {
	return len(p.AllowDoctypes) > 0 && len(p.AllowMethods) == 0
}

// confirmRank orders the confirm modes from the loosest.
func confirmRank(c string) int {
	switch c {
	case config.ConfirmNever:
		return 0
	case config.ConfirmAlways:
		return 2
	}
	return 1 // "" and if-supported
}

func confirmName(c string) string {
	if c == "" {
		return config.ConfirmIfSupported
	}
	return c
}

// minus is a without the entries of b (exact match: a different spelling
// counts as a different entry, which errs towards asking).
func minus(a, b []string) []string {
	var out []string
	for _, v := range a {
		if !slices.Contains(b, v) {
			out = append(out, v)
		}
	}
	return out
}

func nonNilList(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func nonNilChanges(v []PolicyChange) []PolicyChange {
	if v == nil {
		return []PolicyChange{}
	}
	return v
}

// GetPolicy returns a site's safety rules. Rules the screen cannot show
// faithfully (a list entry that is blank or repeated, as only a hand edit
// writes) are refused: saving over them would change them unseen.
func (s *SitesService) GetPolicy(name string) (SitePolicy, error) {
	site, _, err := s.lookup(name)
	if err != nil {
		return SitePolicy{}, err
	}
	if !faithful(site.MCP) {
		return SitePolicy{}, keyed(CodeInvalid, "policy.handEdited", nil)
	}
	return policyOf(site.MCP).normalized(), nil
}

// faithful reports whether every list entry of p survives cleanList: a
// blank entry is dropped by it, yet ffc counts it (an allow list of only
// [" "] refuses everything).
func faithful(p *config.MCPPolicy) bool {
	if p == nil {
		return true
	}
	for _, l := range [][]string{p.AllowTools, p.AllowDoctypes, p.DenyDoctypes, p.AllowMethods, p.DenyMethods} {
		for _, v := range l {
			if strings.TrimSpace(v) == "" {
				return false
			}
		}
	}
	return true
}

// PreviewPolicy checks a policy and says what saving it would open and
// narrow compared with the site's current rules.
func (s *SitesService) PreviewPolicy(name string, p SitePolicy) (PolicyPreview, error) {
	p, err := checkPolicy(p)
	if err != nil {
		return PolicyPreview{}, err
	}
	cur, err := s.GetPolicy(name)
	if err != nil {
		return PolicyPreview{}, err
	}
	return comparePolicies(cur, p), nil
}

// SavePolicy replaces a site's safety rules. base is the policy the screen
// showed: when the file changed since (another window, ffc, an editor), the
// save is refused so nothing is overwritten unseen. A change that opens
// access asks first in a native dialog; declining saves nothing. It returns
// the rules as stored.
func (s *SitesService) SavePolicy(name string, base, p SitePolicy) (SitePolicy, error) {
	// A save that comes while another waits for its dialog is refused, not
	// queued: the person must not answer a question meant for another.
	if !policyMu.TryLock() {
		return SitePolicy{}, keyed(CodeUnavailable, "policy.busy", nil)
	}
	defer policyMu.Unlock()
	p, err := checkPolicy(p)
	if err != nil {
		return SitePolicy{}, err
	}
	cur, err := s.GetPolicy(name)
	if err != nil {
		return SitePolicy{}, err
	}
	if !cur.equal(base) {
		return SitePolicy{}, keyed(CodeInvalid, "policy.changed", nil)
	}
	if cur.equal(p) {
		return cur, nil
	}
	if opens := comparePolicies(cur, p).Opens; len(opens) > 0 {
		// Asked before the config lock is taken: the dialog may stay open
		// longer than the lock may be held.
		c, ok := s.host.(PolicyConfirmer)
		if !ok {
			return SitePolicy{}, keyed(CodeUnavailable, "policy.cannotAsk", nil)
		}
		yes, err := c.ConfirmPolicyChange(name, opens)
		if err != nil {
			return SitePolicy{}, keyed(CodeFailed, "policy.askFailed", err)
		}
		if !yes {
			return SitePolicy{}, keyed(CodeCancelled, "policy.declined", nil)
		}
	}
	err = config.Edit(s.path, func(f *config.File) error {
		site, ok := f.Site(name)
		if !ok {
			return &Error{Code: CodeNotFound, Message: fmt.Sprintf("There is no site called %q any more.", name)}
		}
		// What was confirmed is the change from cur: refuse if the file moved
		// on while the dialog was open.
		if !faithful(site.MCP) || !policyOf(site.MCP).equal(cur) {
			return keyed(CodeInvalid, "policy.changed", nil)
		}
		if err := f.SetSiteMCP(name, p.config()); err != nil {
			return keyed(CodeInvalid, "policy.anchors", err)
		}
		// What ffc will read must be what was asked for.
		if after, ok := f.Site(name); !ok || !policyOf(after.MCP).equal(p) {
			return keyed(CodeFailed, "policy.saveFailed", nil)
		}
		return nil
	})
	if err != nil {
		return SitePolicy{}, wrapPolicyErr(err)
	}
	return s.GetPolicy(name)
}

func wrapPolicyErr(err error) error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return keyed(CodeFailed, "policy.saveFailed", err)
}
