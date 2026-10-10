package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm/prices"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
)

const (
	exportFormat  = "foxmayn-desktop-conversation"
	exportVersion = 1

	// maxImportBytes is the largest file ImportConversation reads.
	maxImportBytes = 50 << 20
	// The most rows of each kind a file may hold.
	maxImportMessages  = 100_000
	maxImportRuns      = 100_000
	maxImportToolCalls = 200_000
	maxImportUsage     = 200_000
	// The largest cost (US dollars) and token count one usage row may hold.
	maxImportCostUSD = 10_000
	maxImportTokens  = 1_000_000_000_000

	// Longest values in a file: ids, names and statuses; titles; the site's
	// address; the collected site context; text blobs (message parts, tool
	// arguments and results, error text).
	maxShortField   = 1024
	maxTitleField   = 500
	maxURLField     = 2048
	maxContextField = 64 << 10
	maxBlobField    = 8 << 20
)

// exportFile is the JSON export of one conversation. Ids only link the rows
// of the file to each other; an import makes new ones.
type exportFile struct {
	Format       string           `json:"format"`
	Version      int              `json:"version"`
	Conversation exportConv       `json:"conversation"`
	Profile      json.RawMessage  `json:"profile"`
	Messages     []exportMessage  `json:"messages"`
	Runs         []exportRun      `json:"runs"`
	ToolCalls    []exportToolCall `json:"tool_calls"`
	Usage        []exportUsage    `json:"usage"`
}

type exportConv struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	TitleSource    string `json:"title_source"`
	Site           string `json:"site"`
	SiteURL        string `json:"site_url"`
	Mode           string `json:"mode"`
	ProviderID     string `json:"provider_id"`
	Model          string `json:"model"`
	ProfileID      string `json:"profile_id"`
	SiteContext    string `json:"site_context"`
	SiteContextKey string `json:"site_context_key"`
	Created        string `json:"created"`
	Updated        string `json:"updated"`
}

type exportMessage struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Parts   json.RawMessage `json:"parts"`
	Created string          `json:"created"`
}

type exportRun struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Steps   int    `json:"steps"`
	Error   string `json:"error"`
	Started string `json:"started"`
	Ended   string `json:"ended"`
}

type exportToolCall struct {
	ID         string `json:"id"`
	RunID      string `json:"run_id"`
	MsgID      string `json:"msg_id"`
	Tool       string `json:"tool"`
	Site       string `json:"site"`
	ArgsJSON   string `json:"args_json"`
	ResultText string `json:"result_text"`
	Status     string `json:"status"`
	Approval   string `json:"approval"`
	Started    string `json:"started"`
	Ended      string `json:"ended"`
}

type exportUsage struct {
	RunID      string   `json:"run_id"`
	Turn       int      `json:"turn"`
	Kind       string   `json:"kind"`
	Input      int      `json:"input"`
	Output     int      `json:"output"`
	Cached     int      `json:"cached"`
	CacheWrite int      `json:"cache_write"`
	CostUSD    *float64 `json:"cost_usd"`
	CostSource string   `json:"cost_source"`
	PriceDate  string   `json:"price_date"`
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(field, s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, invalid("file", fmt.Sprintf("The file has a date that cannot be read (%s).", field))
	}
	return t, nil
}

// buildExport turns the stored conversation into the export shape. profile is
// the conversation's profile as it is now ("" or a missing profile gives
// null).
func buildExport(d store.ExportData, profile *Profile) (exportFile, error) {
	c := d.Conversation
	f := exportFile{
		Format: exportFormat, Version: exportVersion,
		Conversation: exportConv{
			ID: c.ID, Title: c.Title, TitleSource: d.TitleSource, Site: c.Site, SiteURL: stripUserinfo(c.SiteURL), Mode: c.Mode, ProviderID: c.ProviderID, Model: c.Model,
			ProfileID: c.ProfileID, SiteContext: c.SiteContext, SiteContextKey: c.SiteContextKey,
			Created: fmtTime(c.Created), Updated: fmtTime(c.Updated),
		},
		Profile:   json.RawMessage("null"),
		Messages:  []exportMessage{},
		Runs:      []exportRun{},
		ToolCalls: []exportToolCall{},
		Usage:     []exportUsage{},
	}
	if profile != nil {
		b, err := json.Marshal(profile)
		if err != nil {
			return exportFile{}, newError(CodeFailed, "The profile could not be exported.", err)
		}
		f.Profile = b
	}
	for _, m := range d.Messages {
		var buf bytes.Buffer
		if err := json.Compact(&buf, []byte(m.PartsJSON)); err != nil {
			return exportFile{}, newError(CodeFailed, "A saved message could not be read.", err)
		}
		f.Messages = append(f.Messages, exportMessage{ID: m.ID, Role: m.Role, Parts: buf.Bytes(), Created: fmtTime(m.Created)})
	}
	for _, r := range d.Runs {
		f.Runs = append(f.Runs, exportRun{ID: r.ID, Status: r.Status, Steps: r.Steps, Error: r.Error, Started: fmtTime(r.Started), Ended: fmtTime(r.Ended)})
	}
	for _, t := range d.ToolCalls {
		f.ToolCalls = append(f.ToolCalls, exportToolCall{
			ID: t.ID, RunID: t.RunID, MsgID: t.MsgID, Tool: t.Tool, Site: t.Site, ArgsJSON: t.ArgsJSON, ResultText: t.ResultText,
			Status: t.Status, Approval: t.Approval, Started: fmtTime(t.Started), Ended: fmtTime(t.Ended),
		})
	}
	for _, u := range d.Usage {
		f.Usage = append(f.Usage, exportUsage{RunID: u.RunID, Turn: u.Turn, Kind: u.Kind, Input: u.Input, Output: u.Output, Cached: u.Cached,
			CacheWrite: u.CacheWrite, CostUSD: u.CostUSD, CostSource: u.CostSource, PriceDate: u.PriceDate})
	}
	return f, nil
}

// stripUserinfo removes a user name and password from an address; an
// address that cannot be read is dropped, since it could hold them in any form.
func stripUserinfo(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User = nil
	return u.String()
}

func (f exportFile) json() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return nil, newError(CodeFailed, "The conversation could not be exported.", err)
	}
	return buf.Bytes(), nil
}

// ---- import ----

// badFile is the error for a file that is not a usable export.
func badFile(msg string) *Error { return invalid("file", msg) }

func checkShort(what, s string) error {
	if len(s) > maxShortField {
		return badFile("The file has a " + what + " that is too long.")
	}
	return nil
}

// parseImport reads a JSON export and checks it. The result holds the file's
// own ids (to link rows) and nothing from outside the file's content: the
// caller makes new ids and decides what the conversation may point to.
func parseImport(data []byte) (store.ExportData, error) {
	if int64(len(data)) > maxImportBytes {
		return store.ExportData{}, badFile(fmt.Sprintf("That file is too large (the limit is %d MB).", maxImportBytes>>20))
	}
	var f exportFile
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&f); err != nil {
		return store.ExportData{}, badFile("That is not a conversation file from Foxmayn Frappe Desktop.")
	}
	if _, err := dec.Token(); err != io.EOF {
		return store.ExportData{}, badFile("That is not a conversation file from Foxmayn Frappe Desktop.")
	}
	if f.Format != exportFormat {
		return store.ExportData{}, badFile("That is not a conversation file from Foxmayn Frappe Desktop.")
	}
	if f.Version != exportVersion {
		return store.ExportData{}, badFile(fmt.Sprintf("This file is version %d, which this version of the app cannot import.", f.Version))
	}
	if len(f.Messages) > maxImportMessages || len(f.Runs) > maxImportRuns || len(f.ToolCalls) > maxImportToolCalls || len(f.Usage) > maxImportUsage {
		return store.ExportData{}, badFile("The file holds more than this app imports.")
	}
	c := f.Conversation
	if c.Site == "" || len(c.Site) > maxShortField {
		return store.ExportData{}, badFile("The file does not name a site.")
	}
	if utf8.RuneCountInString(c.Title) > maxTitleField || len(c.SiteURL) > maxURLField {
		return store.ExportData{}, badFile("The file has a title or address that is too long.")
	}
	switch c.TitleSource {
	case store.TitleFallback, store.TitleAuto, store.TitleUser:
	default:
		return store.ExportData{}, badFile("The file has a title of an unknown origin.")
	}
	for what, s := range map[string]string{"provider": c.ProviderID, "model": c.Model, "profile": c.ProfileID} {
		if err := checkShort(what, s); err != nil {
			return store.ExportData{}, err
		}
	}
	var out store.ExportData
	out.Conversation = store.Conversation{
		Title: c.Title, Site: c.Site, SiteURL: stripUserinfo(c.SiteURL),
		// Whatever the file says, an imported conversation starts read only:
		// its write mode is a safety setting the file must not grant.
		Mode:       ModeRead,
		ProviderID: c.ProviderID, Model: c.Model, ProfileID: c.ProfileID,
		// The site context goes into the system prompt when its key matches
		// the one the app computes (a preset's, for one): never take either
		// from a file. The first run collects its own.
		SiteContext: "", SiteContextKey: "",
	}
	for i, m := range f.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			return store.ExportData{}, badFile(fmt.Sprintf("Message %d has an unknown author.", i+1))
		}
		if err := checkShort("message id", m.ID); err != nil {
			return store.ExportData{}, err
		}
		if len(m.Parts) > maxBlobField {
			return store.ExportData{}, badFile(fmt.Sprintf("Message %d is too large.", i+1))
		}
		parts, err := llm.UnmarshalParts(string(m.Parts))
		if err != nil || len(m.Parts) == 0 || m.Parts[0] != '[' {
			return store.ExportData{}, badFile(fmt.Sprintf("Message %d could not be read.", i+1))
		}
		// The attachments are not in the file: an image becomes a note, so a
		// later run never points at an attachment that is not there. Thinking
		// is not kept: it is the model's, and a file cannot speak for it.
		kept := parts[:0]
		for _, p := range parts {
			switch v := p.(type) {
			case llm.Thinking:
				continue
			case llm.Image:
				p = llm.Text{Text: "[image not included: " + clipRunes(v.MediaType, 40) + "]"}
			}
			kept = append(kept, p)
		}
		parts = kept
		stored, err := llm.MarshalParts(parts)
		if err != nil {
			return store.ExportData{}, badFile(fmt.Sprintf("Message %d could not be read.", i+1))
		}
		created, err := parseTime("message", m.Created)
		if err != nil {
			return store.ExportData{}, err
		}
		out.Messages = append(out.Messages, store.Message{ID: m.ID, Role: m.Role, PartsJSON: stored, Created: created})
	}
	for _, r := range f.Runs {
		if err := checkShort("run id", r.ID); err != nil {
			return store.ExportData{}, err
		}
		if len(r.Error) > maxContextField || r.Steps < 0 {
			return store.ExportData{}, badFile("A run in the file cannot be read.")
		}
		started, err := parseTime("run", r.Started)
		if err != nil {
			return store.ExportData{}, err
		}
		ended, err := parseTime("run", r.Ended)
		if err != nil {
			return store.ExportData{}, err
		}
		status := r.Status
		switch status {
		case RunDone, RunError, RunCancelled:
		default:
			status = RunCancelled // a run that was running or paused is not resumable here
		}
		out.Runs = append(out.Runs, store.Run{ID: r.ID, Status: status, Steps: r.Steps, Error: r.Error, Started: started, Ended: ended})
	}
	for _, t := range f.ToolCalls {
		for what, s := range map[string]string{"tool call id": t.ID, "run id": t.RunID, "message id": t.MsgID, "tool name": t.Tool, "site": t.Site, "approval": t.Approval, "status": t.Status} {
			if err := checkShort(what, s); err != nil {
				return store.ExportData{}, err
			}
		}
		if len(t.ArgsJSON) > maxBlobField || len(t.ResultText) > maxBlobField || !json.Valid([]byte(t.ArgsJSON)) {
			return store.ExportData{}, badFile("A tool call in the file cannot be read.")
		}
		started, err := parseTime("tool call", t.Started)
		if err != nil {
			return store.ExportData{}, err
		}
		ended, err := parseTime("tool call", t.Ended)
		if err != nil {
			return store.ExportData{}, err
		}
		status := t.Status
		switch status {
		case ToolOK, ToolError, ToolStopped:
		default:
			status = ToolStopped
		}
		out.ToolCalls = append(out.ToolCalls, store.ToolCall{ID: t.ID, RunID: t.RunID, MsgID: t.MsgID, Tool: t.Tool, Site: t.Site,
			ArgsJSON: t.ArgsJSON, ResultText: t.ResultText, Status: status,
			// Nobody approved this call here: the file's word for it is not kept.
			Approval: "", Started: started, Ended: ended})
	}
	for _, u := range f.Usage {
		for what, s := range map[string]string{"run id": u.RunID, "usage kind": u.Kind, "cost source": u.CostSource, "price date": u.PriceDate} {
			if err := checkShort(what, s); err != nil {
				return store.ExportData{}, err
			}
		}
		// Only the kinds and cost sources the app writes; a cost within
		// reason (not NaN, not infinite); token counts that cannot overflow a
		// total. An imported row is history: it keeps its source, and the
		// run under it is marked as imported (store.ImportedPrefix).
		switch u.Kind {
		case "", store.UsageTurn, store.UsageTitle:
		default:
			return store.ExportData{}, badFile("A usage row in the file has a kind this app does not know.")
		}
		switch u.CostSource {
		case "", prices.SourceProvider, prices.SourceTable, prices.SourceLocal, prices.SourceUnknown:
		default:
			return store.ExportData{}, badFile("A usage row in the file has a cost source this app does not know.")
		}
		if u.CostUSD != nil && (math.IsNaN(*u.CostUSD) || math.IsInf(*u.CostUSD, 0) || *u.CostUSD < 0 || *u.CostUSD > maxImportCostUSD) {
			return store.ExportData{}, badFile("A usage row in the file has a cost that cannot be right.")
		}
		if u.Turn < 0 || u.Input < 0 || u.Output < 0 || u.Cached < 0 || u.CacheWrite < 0 ||
			u.Turn > maxImportTokens || u.Input > maxImportTokens || u.Output > maxImportTokens || u.Cached > maxImportTokens || u.CacheWrite > maxImportTokens {
			return store.ExportData{}, badFile("A usage row in the file cannot be read.")
		}
		out.Usage = append(out.Usage, store.Usage{RunID: u.RunID, Turn: u.Turn, Kind: u.Kind, Input: u.Input, Output: u.Output, Cached: u.Cached,
			CacheWrite: u.CacheWrite, CostUSD: u.CostUSD, CostSource: u.CostSource, PriceDate: u.PriceDate})
	}
	return out, nil
}

// ---- the service calls ----

// ImportResult is the answer of ImportConversation.
type ImportResult struct {
	// Cancelled is true when the person closed the file dialog.
	Cancelled    bool         `json:"cancelled"`
	Conversation Conversation `json:"conversation"`
}

var slugBad = regexp.MustCompile(`[^a-z0-9]+`)

func fileSlug(title string) string {
	s := strings.Trim(slugBad.ReplaceAllString(strings.ToLower(title), "-"), "-")
	s = clipRunes(s, 40)
	if s == "" {
		return "conversation"
	}
	return s
}

// ExportConversation saves a conversation to a file the person chooses.
// format is "json" (everything, to import again) or "md" (to read). It
// returns the path, or "" when the person cancelled. The file
// holds no provider key.
func (a *AssistantService) ExportConversation(convID, format string) (string, error) {
	if format != "json" && format != "md" {
		return "", invalid("format", "Choose JSON or Markdown.")
	}
	dlg, err := a.dialogs()
	if err != nil {
		return "", err
	}
	// The dialog can stay open for a long time: do not hold the service while
	// it does.
	title, err := func() (string, error) {
		_, st, done, err := a.enter()
		if err != nil {
			return "", err
		}
		defer done()
		c, err := st.GetConversation(convID)
		if err != nil {
			return "", wrapStoreErr(err)
		}
		return c.Title, nil
	}()
	if err != nil {
		return "", err
	}
	filter, pattern, name := "JSON", "*.json", fileSlug(title)+".json"
	if format == "md" {
		filter, pattern, name = "Markdown", "*.md", fileSlug(title)+".md"
	}
	path, err := dlg.SaveFileDialog(dialogT("exportTitle"), name, filter, pattern)
	if err != nil {
		return "", newError(CodeFailed, "The file dialog failed.", err)
	}
	if path == "" {
		return "", nil
	}
	_, st, done, err := a.enter()
	if err != nil {
		return "", err
	}
	defer done()
	data, err := a.exportBytes(st, convID, format)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, data); err != nil {
		return "", newError(CodeFailed, "The file could not be saved.", err)
	}
	return path, nil
}

// writeFileAtomic writes data to a temporary file (0600) in path's folder and
// renames it over path, so a failure leaves the old file or nothing, never half
// of a new one.
func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".foxmayn-export-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// exportBytes is the content of an export file.
func (a *AssistantService) exportBytes(st *store.Store, convID, format string) ([]byte, error) {
	d, err := st.ReadConversation(convID)
	if err != nil {
		return nil, wrapStoreErr(err)
	}
	var profile *Profile
	if d.Conversation.ProfileID != "" {
		if p, err := resolveProfile(st, d.Conversation.ProfileID); err == nil {
			profile = &p
		}
	}
	f, err := buildExport(d, profile)
	if err != nil {
		return nil, err
	}
	var data []byte
	if format == "md" {
		data = []byte(renderMarkdown(f))
	} else if data, err = f.json(); err != nil {
		return nil, err
	}
	return a.scrubKeys(st, data)
}

// ImportConversation reads a conversation file (made by ExportConversation)
// the person chooses and adds it as a new conversation, read only, with new
// ids. Images are not in the file and become notes. A file over 50 MB, of
// another format or version, or that does not hold together is refused.
func (a *AssistantService) ImportConversation() (ImportResult, error) {
	dlg, err := a.dialogs()
	if err != nil {
		return ImportResult{}, err
	}
	path, err := dlg.OpenFileDialog(dialogT("importTitle"), dialogT("importFilter"), "*.json")
	if err != nil {
		return ImportResult{}, newError(CodeFailed, "The file dialog failed.", err)
	}
	if path == "" {
		return ImportResult{Cancelled: true}, nil
	}
	data, err := readLimited(path, maxImportBytes)
	if err != nil {
		return ImportResult{}, err
	}
	c, err := a.importBytes(data)
	if err != nil {
		return ImportResult{}, err
	}
	return ImportResult{Conversation: c}, nil
}

// importBytes parses and stores an export.
func (a *AssistantService) importBytes(data []byte) (Conversation, error) {
	d, err := parseImport(data)
	if err != nil {
		return Conversation{}, err
	}
	// A model is kept only when the provider offers it now. When the list
	// cannot be read (offline) the model is cleared and the conversation uses
	// the provider's default.
	if c := &d.Conversation; c.ProviderID != "" && c.Model != "" {
		keep := false
		if models, err := a.ListModels(c.ProviderID); err == nil {
			for _, m := range models {
				if m.ID == c.Model {
					keep = true
				}
			}
		}
		if !keep {
			c.Model = ""
		}
	}
	_, st, done, err := a.enter()
	if err != nil {
		return Conversation{}, err
	}
	defer done()
	// The file's provider and profile are names: keep them only when they
	// are here.
	c := &d.Conversation
	if c.ProviderID != "" {
		ps, err := st.ListProviders()
		if err != nil {
			return Conversation{}, wrapStoreErr(err)
		}
		found := false
		for _, p := range ps {
			if p.ID == c.ProviderID {
				found = true
			}
		}
		if !found {
			c.ProviderID, c.Model = "", ""
		}
	}
	ephemeral := false
	if c.ProfileID != "" {
		if p, err := resolveProfile(st, c.ProfileID); err != nil {
			c.ProfileID = ""
		} else {
			ephemeral = !p.KeepHistory
		}
	}
	stored, err := st.ImportConversation(d)
	if errors.Is(err, store.ErrBadImport) {
		return Conversation{}, badFile("The file does not hold together: a row points to something that is not in it.")
	}
	if err != nil {
		return Conversation{}, wrapStoreErr(err)
	}
	// A profile that keeps no history makes the conversation ephemeral, as
	// choosing it does.
	if ephemeral {
		if err := st.SetEphemeral(stored.ID, true); err != nil {
			return Conversation{}, wrapStoreErr(err)
		}
	}
	out := toConversation(stored)
	out.Ephemeral = ephemeral
	return out, nil
}
