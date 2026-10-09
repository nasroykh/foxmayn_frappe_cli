package services

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
)

// A profile only narrows: its lists go to ffc as the options layer
// (cmd.MCPOptions.Policy, which ffc lets only tighten a site's own policy),
// its tool sets choose which of the tools that policy allows are served, and
// the app filters (deny_tools, the call_method switch) only remove tools.
// Its write mode is combined with the conversation's switch, the stricter
// one winning. Confirm is never taken from a profile: the engine always
// asks ffc for "always".

const (
	// defaultStepLimit is the step limit without a profile, and a new
	// profile's.
	defaultStepLimit = loopStepBudget
	// maxStepLimit bounds a profile's step limit.
	maxStepLimit = 100
	// maxProfileName bounds a profile's name.
	maxProfileName = 80
	// maxInstructionChars bounds a profile's or a site's instructions.
	maxInstructionChars = 4000
	// maxListEntries bounds each list of a profile.
	maxListEntries = 200
	// maxEntryChars bounds one entry of a list.
	maxEntryChars = 200
)

// knownToolsets are ffc's tool sets (internal/cmd/mcp_policy.go).
var knownToolsets = []string{"core", "lifecycle", "collab", "admin", "files", "erp"}

// Profile is a set of limits for a conversation: a built-in preset (Preset
// true, never changed; duplicate it to edit) or the user's own. Lists that
// are empty mean "no limit of this profile"; the site's policy still applies.
type Profile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Preset marks a built-in profile.
	Preset bool `json:"preset"`
	// BasedOn is the preset a user profile was duplicated from, or "".
	BasedOn string `json:"basedOn"`
	// Mode is "read" (the default) or "ask". The conversation's switch can
	// only make it stricter.
	Mode string `json:"mode"`
	// Toolsets are ffc's tool sets to serve; empty means core and
	// lifecycle.
	Toolsets      []string `json:"toolsets"`
	AllowTools    []string `json:"allowTools"`
	AllowDoctypes []string `json:"allowDoctypes"`
	DenyDoctypes  []string `json:"denyDoctypes"`
	AllowMethods  []string `json:"allowMethods"`
	DenyMethods   []string `json:"denyMethods"`
	// DenyTools are never offered to the model.
	DenyTools []string `json:"denyTools"`
	// CallMethod offers call_method when the site's policy serves it.
	CallMethod bool `json:"callMethod"`
	// StepLimit is how many tool calls a run makes before it pauses (1-100).
	StepLimit int `json:"stepLimit"`
	// ProviderID and Model, when set, are what a conversation switches to
	// when it takes this profile.
	ProviderID   string `json:"providerID"`
	Model        string `json:"model"`
	Instructions string `json:"instructions"`
	// KeepHistory false makes conversations ephemeral.
	KeepHistory bool `json:"keepHistory"`
}

// Preset ids. User profiles have 32-hex ids, so they never collide.
const (
	PresetExplore   = "explore"
	PresetAccounts  = "accounts"
	PresetSiteAdmin = "site-admin"
	PresetDataEntry = "data-entry"
	PresetLocal     = "local-model"
)

// fallbackProfile is where conversations go when their profile is deleted:
// the strictest preset, so a deletion never widens anything.
const fallbackProfile = PresetExplore

// presets are the built-in profiles, in display order.
func presets() []Profile {
	return []Profile{
		{
			ID: PresetExplore, Name: "Explore", Preset: true, Mode: ModeRead,
			StepLimit: defaultStepLimit, KeepHistory: true,
			Instructions: "Explore the site: look things up, explain what you find and point to the documents you used.",
		},
		{
			ID: PresetAccounts, Name: "Accounts helper", Preset: true, Mode: ModeAsk,
			Toolsets: []string{"core", "lifecycle", "erp"}, StepLimit: defaultStepLimit, KeepHistory: true,
			Instructions: "Help with invoices, payments and journal entries. Prepare drafts; submit or cancel a document only when the user asks for it.",
		},
		{
			ID: PresetSiteAdmin, Name: "Site admin", Preset: true, Mode: ModeRead,
			Toolsets: []string{"core", "admin"}, StepLimit: defaultStepLimit, KeepHistory: true,
			Instructions: "Check the site's health: background jobs, the error log and the scheduler. Report what needs attention first.",
		},
		{
			ID: PresetDataEntry, Name: "Data entry", Preset: true, Mode: ModeAsk,
			Toolsets: []string{"core"}, DenyTools: []string{"delete_doc", "bulk_delete"}, StepLimit: defaultStepLimit, KeepHistory: true,
			Instructions: "Create and update documents the user describes. Read the DocType's schema before you write, and never delete anything.",
		},
		{
			ID: PresetLocal, Name: "Local model", Preset: true, Mode: ModeRead,
			Toolsets: []string{"core"}, StepLimit: 15, KeepHistory: true,
			Instructions: "Use one tool at a time and keep answers short.",
		},
	}
}

func presetByID(id string) (Profile, bool) {
	for _, p := range presets() {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

// noProfile is a conversation without a profile: the site's policy, the
// default tool sets and step limit, and the conversation's own switch.
func noProfile() Profile {
	return Profile{Mode: ModeAsk, StepLimit: defaultStepLimit, KeepHistory: true}
}

// profilePolicy is what policy_json holds. Only narrowing lists are read
// back: a stored read_only, confirm or anything else is ignored.
type profilePolicy struct {
	AllowTools    []string `json:"allow_tools,omitempty"`
	AllowDoctypes []string `json:"allow_doctypes,omitempty"`
	DenyDoctypes  []string `json:"deny_doctypes,omitempty"`
	AllowMethods  []string `json:"allow_methods,omitempty"`
	DenyMethods   []string `json:"deny_methods,omitempty"`
}

// resolveProfile returns the profile id names: "" is noProfile, a preset id
// a preset, anything else a stored profile. A stored profile that cannot be
// read, or whose limits do not parse, is an error: the run must not go on
// with fewer limits than the user chose.
func resolveProfile(st *store.Store, id string) (Profile, error) {
	if id == "" {
		return noProfile(), nil
	}
	if p, ok := presetByID(id); ok {
		return p, nil
	}
	row, err := st.GetProfile(id)
	if errors.Is(err, store.ErrNotFound) {
		return Profile{}, &Error{Code: CodeNotFound, Message: "The profile of this conversation was removed. Choose another one.", Field: "profile"}
	}
	if err != nil {
		return Profile{}, wrapStoreErr(err)
	}
	p, err := fromRow(row)
	if err != nil {
		return Profile{}, newError(CodeFailed, "The profile of this conversation could not be read.", err)
	}
	return p, nil
}

func fromRow(r store.Profile) (Profile, error) {
	p := Profile{
		ID: r.ID, Name: r.Name, BasedOn: r.Preset, Mode: r.Mode, CallMethod: r.CallMethod,
		StepLimit: r.StepLimit, ProviderID: r.ProviderID, Model: r.Model, Instructions: r.Instructions, KeepHistory: r.KeepHistory,
	}
	if err := json.Unmarshal([]byte(r.ToolsetsJSON), &p.Toolsets); err != nil {
		return Profile{}, err
	}
	if err := json.Unmarshal([]byte(r.DenyToolsJSON), &p.DenyTools); err != nil {
		return Profile{}, err
	}
	var pol profilePolicy
	if err := json.Unmarshal([]byte(r.PolicyJSON), &pol); err != nil {
		return Profile{}, err
	}
	p.AllowTools, p.AllowDoctypes, p.DenyDoctypes, p.AllowMethods, p.DenyMethods =
		pol.AllowTools, pol.AllowDoctypes, pol.DenyDoctypes, pol.AllowMethods, pol.DenyMethods
	// A row written by hand or by a future version is held to today's rules.
	if p.Mode != ModeAsk {
		p.Mode = ModeRead
	}
	if p.StepLimit < 1 || p.StepLimit > maxStepLimit {
		p.StepLimit = defaultStepLimit
	}
	for _, ts := range p.Toolsets {
		if !slices.Contains(knownToolsets, ts) {
			return Profile{}, errors.New("unknown tool set " + ts)
		}
	}
	return p.normalized(), nil
}

func toRow(p Profile) (store.Profile, error) {
	ts, err := json.Marshal(nonNil(p.Toolsets))
	if err != nil {
		return store.Profile{}, err
	}
	dt, err := json.Marshal(nonNil(p.DenyTools))
	if err != nil {
		return store.Profile{}, err
	}
	pol, err := json.Marshal(profilePolicy{
		AllowTools: p.AllowTools, AllowDoctypes: p.AllowDoctypes, DenyDoctypes: p.DenyDoctypes,
		AllowMethods: p.AllowMethods, DenyMethods: p.DenyMethods,
	})
	if err != nil {
		return store.Profile{}, err
	}
	return store.Profile{
		ID: p.ID, Name: p.Name, Preset: p.BasedOn, Mode: p.Mode, ToolsetsJSON: string(ts), PolicyJSON: string(pol),
		DenyToolsJSON: string(dt), CallMethod: p.CallMethod, StepLimit: p.StepLimit, ProviderID: p.ProviderID,
		Model: p.Model, Instructions: p.Instructions, KeepHistory: p.KeepHistory,
	}, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// normalized returns p with every list trimmed, deduplicated and sorted, and
// empty lists as nil (ffc refuses an empty non-nil list).
func (p Profile) normalized() Profile {
	for _, l := range []*[]string{&p.Toolsets, &p.AllowTools, &p.AllowDoctypes, &p.DenyDoctypes, &p.AllowMethods, &p.DenyMethods, &p.DenyTools} {
		*l = cleanList(*l)
	}
	return p
}

func cleanList(in []string) []string {
	var out []string
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return out
}

// checkProfile validates and normalises a profile the user saves.
func checkProfile(p Profile) (Profile, error) {
	p = p.normalized()
	p.Name = strings.TrimSpace(p.Name)
	switch {
	case p.Name == "":
		return Profile{}, invalid("name", "Give the profile a name.")
	case utf8.RuneCountInString(p.Name) > maxProfileName:
		return Profile{}, invalid("name", "That name is too long.")
	case hasControl(p.Name):
		return Profile{}, invalid("name", "That name has characters that are not allowed.")
	}
	switch p.Mode {
	case "":
		p.Mode = ModeRead // a new profile starts read only
	case ModeRead, ModeAsk:
	default:
		return Profile{}, invalid("mode", "Choose \"Read only\" or \"Ask before changes\".")
	}
	for _, ts := range p.Toolsets {
		if !slices.Contains(knownToolsets, ts) {
			return Profile{}, invalid("toolsets", "Unknown tool set "+ts+".")
		}
	}
	for field, l := range map[string][]string{
		"allowTools": p.AllowTools, "allowDoctypes": p.AllowDoctypes, "denyDoctypes": p.DenyDoctypes,
		"allowMethods": p.AllowMethods, "denyMethods": p.DenyMethods, "denyTools": p.DenyTools,
	} {
		if len(l) > maxListEntries {
			return Profile{}, invalid(field, "That list is too long.")
		}
		for _, v := range l {
			if utf8.RuneCountInString(v) > maxEntryChars || hasControl(v) || text.Sanitize(v) != v {
				return Profile{}, invalid(field, "An entry of that list is not valid.")
			}
		}
	}
	switch {
	case p.StepLimit == 0:
		p.StepLimit = defaultStepLimit
	case p.StepLimit < 1 || p.StepLimit > maxStepLimit:
		return Profile{}, invalid("stepLimit", "Choose a step limit from 1 to 100.")
	}
	if utf8.RuneCountInString(p.Instructions) > maxInstructionChars {
		return Profile{}, invalid("instructions", "Those instructions are too long.")
	}
	p.Instructions = strings.TrimSpace(p.Instructions)
	if p.ProviderID != "" {
		if err := validProviderID(p.ProviderID); err != nil {
			return Profile{}, err
		}
	}
	m, err := checkModel("model", p.Model)
	if err != nil {
		return Profile{}, err
	}
	p.Model = m
	if p.BasedOn != "" {
		if _, ok := presetByID(p.BasedOn); !ok {
			p.BasedOn = ""
		}
	}
	p.Preset = false
	return p, nil
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}

// effectiveMode is the stricter of the profile's mode and the conversation's
// switch: writes only when both say "ask".
func effectiveMode(profileMode, convMode string) string {
	if profileMode == ModeAsk && convMode == ModeAsk {
		return ModeAsk
	}
	return ModeRead
}

// engineSpec is what the engine serves for p in a conversation whose switch
// is convMode.
func (p Profile) engineSpec(convMode string) EngineSpec {
	mode := EngineRead
	if effectiveMode(p.Mode, convMode) == ModeAsk {
		mode = EngineAsk
	}
	return EngineSpec{
		Mode: mode,
		Policy: config.MCPPolicy{
			AllowTools: p.AllowTools, AllowDoctypes: p.AllowDoctypes, DenyDoctypes: p.DenyDoctypes,
			AllowMethods: p.AllowMethods, DenyMethods: p.DenyMethods,
		},
		Toolsets: p.Toolsets,
	}
}

// offers reports whether the app may offer a tool the session serves.
func (p Profile) offers(tool string) bool {
	if tool == "call_method" && !p.CallMethod {
		return false
	}
	return !slices.Contains(p.DenyTools, tool)
}

// ---- service methods ----

// ListPresets returns the built-in profiles.
func (a *AssistantService) ListPresets() []Profile { return presets() }

// ListProfiles returns the user's own profiles, by name.
func (a *AssistantService) ListProfiles() ([]Profile, error) {
	_, st, done, err := a.enter()
	if err != nil {
		return nil, err
	}
	defer done()
	rows, err := st.ListProfiles()
	if err != nil {
		return nil, wrapStoreErr(err)
	}
	out := make([]Profile, 0, len(rows))
	for _, r := range rows {
		p, err := fromRow(r)
		if err != nil {
			continue // shown nowhere; a conversation using it fails closed
		}
		out = append(out, p)
	}
	return out, nil
}

// SaveProfile adds (empty ID) or changes one of the user's profiles. Presets
// cannot be changed: save a copy with BasedOn set to the preset.
func (a *AssistantService) SaveProfile(p Profile) (Profile, error) {
	_, st, done, err := a.enter()
	if err != nil {
		return Profile{}, err
	}
	defer done()
	if _, ok := presetByID(p.ID); ok || p.Preset {
		return Profile{}, invalid("id", "Built-in profiles cannot be changed. Duplicate it to edit.")
	}
	if p.ID != "" {
		if _, err := st.GetProfile(p.ID); err != nil {
			return Profile{}, wrapProfileErr(err)
		}
	}
	p, err = checkProfile(p)
	if err != nil {
		return Profile{}, err
	}
	row, err := toRow(p)
	if err != nil {
		return Profile{}, newError(CodeFailed, "Could not save the profile.", err)
	}
	saved, err := st.SaveProfile(row)
	if err != nil {
		return Profile{}, wrapStoreErr(err)
	}
	return fromRow(saved)
}

// DeleteProfile removes one of the user's profiles. Its conversations move to
// the Explore preset (read only), so nothing gains rights. It is refused
// while one of them has a run in progress.
func (a *AssistantService) DeleteProfile(id string) error {
	r, st, done, err := a.enter()
	if err != nil {
		return err
	}
	defer done()
	if _, ok := presetByID(id); ok || id == "" {
		return invalid("id", "Built-in profiles cannot be deleted.")
	}
	return r.whileProfileIdle(id, func() error {
		if err := st.DeleteProfile(id, fallbackProfile); err != nil {
			return wrapProfileErr(err)
		}
		return nil
	})
}

// SetConversationProfile gives a conversation a profile ("" for none). When
// the profile names a provider the conversation switches to it, and a site
// set to local models only refuses a provider that is not local. It is
// refused while a run is active.
func (a *AssistantService) SetConversationProfile(convID, profileID string) (Conversation, error) {
	r, st, done, err := a.enter()
	if err != nil {
		return Conversation{}, err
	}
	defer done()
	var out Conversation
	err = r.whileIdle(convID, func() error {
		conv, err := st.GetConversation(convID)
		if err != nil {
			return wrapStoreErr(err)
		}
		prof, err := resolveProfile(st, profileID)
		if err != nil {
			return err
		}
		providerID, model := conv.ProviderID, conv.Model
		if prof.ProviderID != "" {
			p, err := a.provider(st, prof.ProviderID)
			if err != nil {
				return err
			}
			providerID, model = p.ID, prof.Model
			if model == "" {
				model = defaultModelOf(p)
			}
		}
		if err := a.checkConversationProvider(st, conv.Site, conv.SiteURL, providerID, model); err != nil {
			return err
		}
		if providerID != conv.ProviderID || model != conv.Model {
			if err := st.SetConversationProvider(convID, providerID, model); err != nil {
				return wrapStoreErr(err)
			}
		}
		if err := st.SetConversationProfile(convID, profileID); err != nil {
			return wrapStoreErr(err)
		}
		// A profile that keeps no history makes the conversation ephemeral.
		if err := st.SetEphemeral(convID, !prof.KeepHistory); err != nil {
			return wrapStoreErr(err)
		}
		c, err := st.GetConversation(convID)
		if err != nil {
			return wrapStoreErr(err)
		}
		out = toConversation(c)
		return nil
	})
	return out, err
}

func wrapProfileErr(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return &Error{Code: CodeNotFound, Message: "That profile no longer exists.", Field: "profile"}
	}
	return wrapStoreErr(err)
}

// whileProfileIdle runs fn when no run is active on a conversation of the
// profile, and keeps new runs from starting until fn returns.
func (r *runner) whileProfileIdle(profileID string, fn func() error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.active {
		if a.conv.ProfileID == profileID {
			return &Error{Code: CodeInvalid, Message: "The assistant is answering in a conversation that uses this profile. Stop it first."}
		}
	}
	return fn()
}
