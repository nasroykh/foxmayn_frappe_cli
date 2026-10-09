package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
)

// SiteSettings are the app's own settings for a site. They are keyed by the
// site's name and keep its address, so they still apply after the site is
// renamed with ffc (same address, new name).
type SiteSettings struct {
	Site string `json:"site"`
	// URL is the site's address (from the ffc config; read only).
	URL string `json:"url"`
	// Instructions go to the model in every conversation on the site.
	Instructions string `json:"instructions"`
	// LocalOnly lets conversations on the site use only a model server on
	// this computer.
	LocalOnly bool `json:"localOnly"`
	// LocalOnlyFrom names the saved settings of another site name with the
	// same address that make this site local only (the site was renamed).
	// Such a site stays local only until those settings are changed.
	LocalOnlyFrom string `json:"localOnlyFrom,omitempty"`
}

// errLocalOnly is the refusal of a cloud provider on a local-only site.
func errLocalOnly() *Error {
	return &Error{Code: CodeInvalid, Field: "provider",
		Message: "This site is set to use local models only. Choose a provider that runs on this computer (Ollama, LM Studio or a local server) and a model that is not an Ollama cloud model."}
}

// sameSiteURL compares two site addresses, ignoring case and a final slash.
// An empty address matches nothing.
func sameSiteURL(a, b string) bool {
	norm := func(s string) string { return strings.ToLower(strings.TrimRight(strings.TrimSpace(s), "/")) }
	return norm(a) != "" && norm(a) == norm(b)
}

// siteSettingsFor merges the saved settings that apply to a site: those of
// its name, and those of an earlier name of it: another name with one of
// its addresses that is no longer in the ffc config (configured, from
// configSites). Another site of the config with the same address (a second
// login) is its own site and lends nothing. The site's own instructions win
// (else the newest earlier name's); LocalOnly is set if any match has it,
// so a rename never turns it off.
func siteSettingsFor(st *store.Store, site string, configured map[string]string, urls ...string) (SiteSettings, error) {
	rows, err := st.ListSiteSettings()
	if err != nil {
		return SiteSettings{}, err
	}
	out := SiteSettings{Site: site}
	for _, u := range urls {
		if u != "" {
			out.URL = u
			break
		}
	}
	var own, other *store.SiteSettings
	for i := range rows {
		r := &rows[i]
		match := r.Site == site
		if _, inConfig := configured[r.Site]; !match && !inConfig {
			for _, u := range urls {
				match = match || sameSiteURL(r.URL, u)
			}
		}
		if !match {
			continue
		}
		switch {
		case r.Site == site:
			own = r
		case other == nil:
			other = r // rows are newest first
		}
		if r.LocalOnly && !out.LocalOnly {
			out.LocalOnly = true
			out.LocalOnlyFrom = r.Site
		}
	}
	switch {
	case own != nil:
		out.Instructions = own.Instructions
	case other != nil:
		out.Instructions = other.Instructions
	}
	if own != nil && own.LocalOnly || !out.LocalOnly {
		out.LocalOnlyFrom = ""
	}
	return out, nil
}

// configSites maps the site names of the ffc config at path to their
// addresses. An unreadable config gives an empty map, which makes every
// other saved name count as an earlier name (the stricter reading).
func configSites(path string) map[string]string {
	out := map[string]string{}
	cfg, err := config.Read(path)
	if err != nil || cfg == nil {
		return out
	}
	for name, sc := range cfg.Sites {
		out[name] = sc.URL
	}
	return out
}

// settingsOf is siteSettingsFor with the ffc config at path.
func settingsOf(st *store.Store, path, site, convURL string) (SiteSettings, error) {
	cs := configSites(path)
	return siteSettingsFor(st, site, cs, convURL, cs[site])
}

// isLocalProvider reports whether p's model server runs on this computer:
// Ollama, LM Studio or a custom server whose address is a loopback IP or
// localhost. A hosted provider and any kind this app does not know are not
// local.
func isLocalProvider(p store.Provider) bool {
	switch p.Kind {
	case KindOllama, KindLMStudio, KindCustom:
	default:
		return false
	}
	base := p.BaseURL
	if base == "" {
		base = kindBaseURL(p.Kind)
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isCloudModel reports whether a model name is one a local server passes on
// to a hosted service: Ollama's cloud models are tagged "cloud" after the
// last ':' or '-' ("gpt-oss:120b-cloud", "kimi-k2:cloud"), and the local
// daemon sends them to ollama.com. UNVERIFIED beyond Ollama's published
// model names (2026-10): the rule follows the tag naming, not an API.
func isCloudModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	i := strings.LastIndexAny(m, ":-")
	return i >= 0 && m[i+1:] == "cloud"
}

// checkLocalOnly is the one local-only check: it refuses p with model ("" is
// the provider's default) for a conversation on site (created at convURL)
// when the site is set to local models only. Every path that picks or uses
// a provider for a conversation goes through it: NewConversation,
// SetConversationProfile and checkRun, which runs before a run starts and
// before each of its model turns. A settings read failure refuses too.
func (a *AssistantService) checkLocalOnly(st *store.Store, site, convURL string, p store.Provider, model string) error {
	ss, err := settingsOf(st, a.configPath, site, convURL)
	if err != nil {
		return wrapStoreErr(err)
	}
	if model == "" {
		model = defaultModelOf(p)
	}
	if ss.LocalOnly && (!isLocalProvider(p) || isCloudModel(model)) {
		return errLocalOnly()
	}
	return nil
}

// checkConversationProvider runs checkLocalOnly for a stored provider id.
func (a *AssistantService) checkConversationProvider(st *store.Store, site, convURL, providerID, model string) error {
	p, err := a.provider(st, providerID)
	if err != nil {
		return err
	}
	return a.checkLocalOnly(st, site, convURL, p, model)
}

// checkRun is the runner's check before a run starts and before each model
// turn: the conversation's site must still have the address it was created
// with (a conversation stored before addresses were kept gets the current
// one), and its provider and model must still pass checkLocalOnly.
func (a *AssistantService) checkRun(conv *store.Conversation) error {
	_, st, err := a.ready()
	if err != nil {
		return err
	}
	cfgURL := configSites(a.configPath)[conv.Site]
	switch {
	case conv.SiteURL == "" && cfgURL != "":
		if err := st.SetConversationSiteURL(conv.ID, cfgURL); err != nil {
			return wrapStoreErr(err)
		}
		conv.SiteURL = cfgURL
	case cfgURL != "" && !sameSiteURL(cfgURL, conv.SiteURL):
		return &Error{Code: CodeInvalid, Field: "site",
			Message: "The site " + conv.Site + " now points to another address than when this conversation began. Start a new conversation for it."}
	}
	p, err := a.provider(st, conv.ProviderID)
	if err != nil {
		return &Error{Code: CodeNotFound, Message: "The provider of this conversation was removed. Set it up again or start a new conversation.", Field: "provider"}
	}
	return a.checkLocalOnly(st, conv.Site, conv.SiteURL, p, conv.Model)
}

// GetSiteSettings returns the settings that apply to a site of the ffc
// config.
func (a *AssistantService) GetSiteSettings(site string) (SiteSettings, error) {
	_, st, done, err := a.enter()
	if err != nil {
		return SiteSettings{}, err
	}
	defer done()
	u, err := a.knownSite(site)
	if err != nil {
		return SiteSettings{}, err
	}
	ss, err := settingsOf(st, a.configPath, site, u)
	if err != nil {
		return SiteSettings{}, wrapStoreErr(err)
	}
	return ss, nil
}

// SaveSiteSettings saves a site's instructions and its local-only switch. The
// address is taken from the ffc config, never from the caller. Turning
// local-only off also turns it off in settings saved under an earlier name
// of the site (same address, no longer in the ffc config), which would
// otherwise keep it on. Another configured site with the same address keeps
// its own setting.
func (a *AssistantService) SaveSiteSettings(s SiteSettings) (SiteSettings, error) {
	_, st, done, err := a.enter()
	if err != nil {
		return SiteSettings{}, err
	}
	defer done()
	u, err := a.knownSite(s.Site)
	if err != nil {
		return SiteSettings{}, err
	}
	instr := strings.TrimSpace(s.Instructions)
	if utf8.RuneCountInString(instr) > maxInstructionChars {
		return SiteSettings{}, invalid("instructions", "Those instructions are too long.")
	}
	if err := st.SaveSiteSettings(store.SiteSettings{Site: s.Site, URL: u, Instructions: instr, LocalOnly: s.LocalOnly}); err != nil {
		return SiteSettings{}, wrapStoreErr(err)
	}
	if !s.LocalOnly {
		// The user turned local-only off for this address: settings kept
		// under an old name of the site (a rename) stop forcing it.
		rows, err := st.ListSiteSettings()
		if err != nil {
			return SiteSettings{}, wrapStoreErr(err)
		}
		cs := configSites(a.configPath)
		for _, r := range rows {
			if _, inConfig := cs[r.Site]; r.Site != s.Site && !inConfig && r.LocalOnly && sameSiteURL(r.URL, u) {
				r.LocalOnly = false
				if err := st.SaveSiteSettings(r); err != nil {
					return SiteSettings{}, wrapStoreErr(err)
				}
			}
		}
	}
	ss, err := settingsOf(st, a.configPath, s.Site, u)
	if err != nil {
		return SiteSettings{}, wrapStoreErr(err)
	}
	return ss, nil
}

// knownSite returns the address of a site of the ffc config.
func (a *AssistantService) knownSite(site string) (string, error) {
	notFound := &Error{Code: CodeNotFound, Message: "That site is not in your list.", Field: "site"}
	if site == "" {
		return "", invalid("site", "Choose a site.")
	}
	cfg, err := config.Read(a.configPath)
	if err != nil || cfg == nil {
		return "", notFound
	}
	sc, ok := cfg.Sites[site]
	if !ok {
		return "", notFound
	}
	return sc.URL, nil
}

// ---- site context ----

// siteContextNone is stored when nothing could be collected, so a site that
// refuses every read is not asked again on each run.
const siteContextNone = "-"

const (
	siteContextValue = 200 // characters of one value
	siteContextRoles = 30  // roles listed
	siteContextApps  = 20  // apps listed
)

// siteContextKey identifies what the site context of a conversation is
// collected under: the profile's narrowing (its policy, tool sets and the
// tools it hides) and the site's own MCP policy in the ffc config. A stored
// context with another key is collected again, so editing a profile in
// place or changing the site's policy never keeps lines it would refuse now.
func siteContextKey(configPath, site string, prof Profile) string {
	var policy any = "unreadable"
	if cfg, err := config.Read(configPath); err == nil && cfg != nil {
		policy = cfg.Sites[site].MCP
	}
	b, _ := json.Marshal(struct {
		Spec   string   `json:"spec"`
		Deny   []string `json:"deny"`
		Call   bool     `json:"call"`
		Policy any      `json:"policy"`
	}{prof.engineSpec(ModeRead).hash(), prof.DenyTools, prof.CallMethod, policy})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// siteContext returns the conversation's site context, collecting it through
// the run's own session the first time, or again when its key changed
// (audited under the run's id). Lines the policy refuses, or whose call
// fails, are left out.
func (a *activeRun) siteContext(ctx context.Context) string {
	if a.r.noSiteContext {
		return ""
	}
	key := siteContextKey(a.r.engine.configPath, a.conv.Site, a.prof)
	if c := a.conv.SiteContext; c != "" && a.conv.SiteContextKey == key {
		if c == siteContextNone {
			return ""
		}
		return c
	}
	out := collectSiteContext(ctx, a.session, a.runID, a.prof)
	if ctx.Err() != nil {
		return out // cut short: try again on the next run
	}
	saved := out
	if saved == "" {
		saved = siteContextNone
	}
	// Best effort: when it cannot be stored it is read again next run.
	if a.r.store.SetConversationSiteContext(a.conv.ID, saved, key) == nil {
		a.conv.SiteContext, a.conv.SiteContextKey = saved, key
	}
	return out
}

func collectSiteContext(ctx context.Context, sess *EngineSession, runID string, prof Profile) string {
	read := func(tool string, args map[string]any) map[string]any {
		if !prof.offers(tool) {
			return nil
		}
		cls, err := sess.Classify(tool, args)
		if err != nil || cls.Denied != "" || cls.Action != "read" {
			return nil
		}
		res, err := sess.Call(ctx, runID, tool, args)
		if err != nil || res == nil || res.IsError {
			return nil
		}
		var m map[string]any
		if json.Unmarshal([]byte(callResultText(res)), &m) != nil {
			return nil
		}
		return m
	}
	var lines []string
	add := func(label string, v any) {
		s, ok := v.(string)
		if !ok {
			return
		}
		if s = contextValue(s); s != "" {
			lines = append(lines, "- "+label+": "+s)
		}
	}
	if w := read("whoami", map[string]any{}); w != nil {
		add("Signed in as", w["user"])
		if roles, ok := w["roles"].([]any); ok && len(roles) > 0 {
			var names []string
			for _, r := range roles {
				if s, ok := r.(string); ok && contextValue(s) != "" {
					names = append(names, contextValue(s))
				}
			}
			more := ""
			if len(names) > siteContextRoles {
				more = fmt.Sprintf(" and %d more", len(names)-siteContextRoles)
				names = names[:siteContextRoles]
			}
			if len(names) > 0 {
				lines = append(lines, "- Roles: "+strings.Join(names, ", ")+more)
			}
		}
		if srv, ok := w["server"].(map[string]any); ok {
			add("Frappe version", srv["frappe"])
			if apps, ok := srv["apps"].(map[string]any); ok {
				var names []string
				for name, v := range apps {
					ver := ""
					if m, ok := v.(map[string]any); ok {
						ver, _ = m["version"].(string)
					}
					if n := contextValue(name); n != "" {
						names = append(names, strings.TrimSpace(n+" "+contextValue(ver)))
					}
				}
				slices.Sort(names)
				if len(names) > siteContextApps {
					names = names[:siteContextApps]
				}
				if len(names) > 0 {
					lines = append(lines, "- Installed apps: "+strings.Join(names, ", "))
				}
			}
		}
	}
	if gd := read("get_doc", map[string]any{"doctype": "Global Defaults", "name": "Global Defaults"}); gd != nil {
		add("Default company", gd["default_company"])
		add("Default currency", gd["default_currency"])
	}
	if len(lines) == 0 {
		return ""
	}
	return "Site context, read from the site when this conversation started:\n" +
		`<site_context untrusted="true">` + "\n" + strings.Join(lines, "\n") + "\n</site_context>"
}

// contextValue makes a value from the site safe for one line of the system
// text: control and invisible characters removed, one line, clipped, and
// every '<' escaped (escapeUntrusted) so it cannot close the wrapper.
func contextValue(s string) string {
	s = strings.Join(strings.Fields(text.Sanitize(s)), " ")
	return escapeUntrusted(clip(s, siteContextValue))
}

// ---- prompt preview ----

// PromptPreview is what the model gets at a conversation's next run: the
// system text in its order, the tools offered and the limits in force.
type PromptPreview struct {
	System string   `json:"system"`
	Tools  []string `json:"tools"`
	// Mode is the write mode in force: the stricter of the profile's and
	// the conversation's ("read" or "ask").
	Mode        string `json:"mode"`
	StepLimit   int    `json:"stepLimit"`
	ProfileName string `json:"profileName"`
	LocalOnly   bool   `json:"localOnly"`
	// SiteContextPending is set when the site context is collected at the
	// next run, so the text does not hold it yet.
	SiteContextPending bool `json:"siteContextPending"`
}

// PromptPreview builds the next run's prompt of a conversation the way the
// run does. It opens an engine session (which may sign in to the site) but
// calls no tool: the site context shown is the one stored.
func (a *AssistantService) PromptPreview(convID string) (PromptPreview, error) {
	_, st, done, err := a.enter()
	if err != nil {
		return PromptPreview{}, err
	}
	defer done()
	conv, err := st.GetConversation(convID)
	if err != nil {
		return PromptPreview{}, wrapStoreErr(err)
	}
	prof, err := resolveProfile(st, conv.ProfileID)
	if err != nil {
		return PromptPreview{}, err
	}
	a.mu.Lock()
	eng, parent := a.engine, a.ctx
	a.mu.Unlock()
	if eng == nil || parent == nil {
		return PromptPreview{}, &Error{Code: CodeUnavailable, Message: "The assistant is not available."}
	}
	ctx, cancel := context.WithTimeout(parent, engineBuildTimeout)
	defer cancel()
	spec := prof.engineSpec(conv.Mode)
	sess, err := eng.OpenSpec(ctx, conv.Site, spec, nil)
	if err != nil {
		return PromptPreview{}, err
	}
	defer sess.Close()
	tools, _, err := offerTools(ctx, sess, prof)
	if err != nil {
		return PromptPreview{}, err
	}
	ss, err := settingsOf(st, a.configPath, conv.Site, conv.SiteURL)
	if err != nil {
		return PromptPreview{}, wrapStoreErr(err)
	}
	siteCtx := conv.SiteContext
	pending := siteCtx == "" || conv.SiteContextKey != siteContextKey(a.configPath, conv.Site, prof)
	if pending || siteCtx == siteContextNone {
		siteCtx = ""
	}
	out := PromptPreview{
		System:             systemText(conv.Site, sess.Instructions(), siteCtx, prof, ss.Instructions),
		Tools:              []string{},
		Mode:               effectiveMode(prof.Mode, conv.Mode),
		StepLimit:          prof.StepLimit,
		ProfileName:        prof.Name,
		LocalOnly:          ss.LocalOnly,
		SiteContextPending: pending,
	}
	for _, t := range tools {
		out.Tools = append(out.Tools, t.Name)
	}
	return out, nil
}
