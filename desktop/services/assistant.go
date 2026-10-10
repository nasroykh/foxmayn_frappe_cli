package services

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/attach"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm/anthropic"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm/gemini"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm/openai"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm/openaicompat"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func init() {
	application.RegisterEvent[ChatDelta](EventChatDelta)
	application.RegisterEvent[ChatTool](EventChatTool)
	application.RegisterEvent[ChatApproval](EventChatApproval)
	application.RegisterEvent[ChatApprovalClosed](EventChatApprovalClosed)
	application.RegisterEvent[ChatUsage](EventChatUsage)
	application.RegisterEvent[ChatDone](EventChatDone)
	application.RegisterEvent[ChatError](EventChatError)
	application.RegisterEvent[ChatTitle](EventChatTitle)
}

// Conversation modes: what the assistant may do to the site.
const (
	ModeRead = "read" // read tools only (the default)
	ModeAsk  = "ask"  // writes too, each after the user's approval
)

// Provider kinds.
const (
	KindAnthropic  = "anthropic"
	KindOpenRouter = "openrouter"
	KindOpenAI     = "openai"
	KindGemini     = "gemini"
	KindOllama     = "ollama"
	KindLMStudio   = "lmstudio"
	KindCustom     = "custom"
)

const (
	// maxSendChars bounds one message.
	maxSendChars = 20000
	// titleChars is how much of the first message names a conversation.
	titleChars = 60
	// providerCallTimeout bounds listing models and checking a key.
	providerCallTimeout = 20 * time.Second
	// maxLabelChars bounds a provider's label.
	maxLabelChars = 80
	// maxModelChars bounds a model name.
	maxModelChars = 200
)

// ProviderInfo is a provider as the UI sees it. It never carries the key.
type ProviderInfo struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// BaseURL is empty for Anthropic's own API.
	BaseURL      string `json:"baseURL"`
	DefaultModel string `json:"defaultModel"`
	KeySet       bool   `json:"keySet"`
	// KeyLast4 is the last four characters of a long enough key.
	KeyLast4 string `json:"keyLast4,omitempty"`
	// KeyCleared is set by SaveProvider when the stored key was deleted
	// because the provider's address changed: ask for the key again.
	KeyCleared bool `json:"keyCleared,omitempty"`
}

// Model is one model a provider offers.
type Model struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Default marks the model the app picks first.
	Default bool `json:"default"`
}

// Conversation is a chat thread bound to a site, a write mode, a provider and
// a model. ProfileID is "" (no profile), a preset id or one of the user's
// profiles.
type Conversation struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Site       string    `json:"site"`
	Mode       string    `json:"mode"`
	ProviderID string    `json:"providerID"`
	Model      string    `json:"model"`
	ProfileID  string    `json:"profileID"`
	Created    time.Time `json:"created"`
	Updated    time.Time `json:"updated"`
	// Pinned and Archived are the user's marks; Ephemeral is set by a
	// profile that keeps no history (the conversation is deleted when the
	// app closes). Only ListConversations fills them.
	Pinned    bool `json:"pinned,omitempty"`
	Archived  bool `json:"archived,omitempty"`
	Ephemeral bool `json:"ephemeral,omitempty"`
}

// ChatToolCall is a tool call of an assistant message, as the chat shows it.
type ChatToolCall struct {
	// ID is the callID of chat:tool events.
	ID   string `json:"id"`
	Tool string `json:"tool"`
	Site string `json:"site"`
	// Status is "running", "ok", "error" or "stopped".
	Status string `json:"status"`
	// Approval is "", "approved", "declined", "cancelled", "ffc-approved" or
	// "ffc-declined".
	Approval string `json:"approval"`
	Summary  string `json:"summary"`
}

// ChatMessage is one visible message. Tool results and the model's thinking
// are not shown.
type ChatMessage struct {
	ID string `json:"id"`
	// Role is "user" or "assistant".
	Role    string         `json:"role"`
	Text    string         `json:"text"`
	Tools   []ChatToolCall `json:"tools"`
	Created time.Time      `json:"created"`
	// Attachments are the files sent with a user message, in order.
	Attachments []StagedAttachment `json:"attachments"`
}

// ConversationDetail is a conversation with its messages.
type ConversationDetail struct {
	Conversation Conversation  `json:"conversation"`
	Messages     []ChatMessage `json:"messages"`
	// ActiveRunID is the run answering now, if any.
	ActiveRunID string `json:"activeRunID"`
	// PausedRunID is the run waiting for Continue, if any.
	PausedRunID string `json:"pausedRunID"`
	// RunUsage is the tokens and cost of each run, shown under the run's
	// last message. Total adds up every call of the conversation.
	RunUsage []RunUsage  `json:"runUsage"`
	Total    UsageTotals `json:"total"`
}

// providerMaker builds the client of a provider row. A seam for tests.
type providerMaker func(p store.Provider, key string) (llm.Provider, error)

// AssistantService is the chat assistant: conversations, runs, approvals and
// providers. Provider keys stay in Go: they go to the OS keychain and to the
// provider's API, never to the web view, events, the store or error text.
type AssistantService struct {
	host       Host
	configPath string
	storePath  string // "" means store.DefaultPath
	keys       *Keys
	mk         providerMaker
	detect     func(context.Context) []openaicompat.Preset
	// noTitles turns off the automatic titles (tests that count requests).
	noTitles bool

	life     sync.RWMutex // held for reading by every call from the web view
	plocks   sync.Map     // provider id -> *sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	st       *store.Store
	engine   *Engine
	run      *runner
	startErr error
	or       orAuth // the OpenRouter browser sign-in (openrouter_auth.go)
	hist     historyState
}

// NewAssistantService returns the service over the ffc config file at
// configPath.
func NewAssistantService(host Host, configPath string) *AssistantService {
	return &AssistantService{
		host:       host,
		configPath: configPath,
		keys:       NewKeys(),
		mk:         makeProvider,
		detect:     openaicompat.DetectLocal,
	}
}

// ServiceStartup opens the store and starts the engine and the runner. A
// failure does not stop the app: the assistant's methods report it.
func (a *AssistantService) ServiceStartup(_ context.Context, _ application.ServiceOptions) error {
	if err := a.open(); err != nil {
		log.Printf("assistant: %v", err)
		a.mu.Lock()
		a.startErr = err
		a.mu.Unlock()
	}
	return nil
}

func (a *AssistantService) open() error {
	path := a.storePath
	if path == "" {
		p, err := store.DefaultPath()
		if err != nil {
			return fmt.Errorf("finding the assistant data folder: %w", err)
		}
		path = p
	}
	st, err := store.Open(path)
	if err != nil {
		return fmt.Errorf("opening the assistant store: %w", err)
	}
	eng := NewEngine(a.configPath)
	r := newRunner(st, eng, a.providerFor, a.host.Emit)
	r.check = a.checkRun
	r.titles = !a.noTitles
	r.images = func(conv store.Conversation) bool { return a.convTakesImages(st, conv) }
	r.pdfs = func(conv store.Conversation) bool { return a.convTakesPDFs(st, conv) }
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.st, a.engine, a.run = st, eng, r
	a.ctx, a.cancel = ctx, cancel
	a.mu.Unlock()
	a.startHistory(ctx)
	// A changed config (a site added, removed or re-signed-in) makes new runs
	// build their servers again.
	if s, ok := a.host.(interface {
		Subscribe(string, func(any))
	}); ok {
		s.Subscribe(EventConfigChanged, func(any) { eng.Invalidate() })
	}
	return nil
}

// ServiceShutdown stops the runs, the engine and the store. Calls that are in
// flight finish (provider checks are cancelled) before the store closes; later
// calls fail with CodeUnavailable.
func (a *AssistantService) ServiceShutdown() error {
	a.mu.Lock()
	r, eng, st, cancel := a.run, a.engine, a.st, a.cancel
	a.run, a.engine, a.st = nil, nil, nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	a.hist.stop()
	if r != nil {
		r.shutdown()
	}
	if eng != nil {
		eng.Close()
	}
	a.life.Lock()
	defer a.life.Unlock()
	if st != nil {
		endHistory(st)
		return st.Close()
	}
	return nil
}

// enter is ready for a call from the web view: it also registers the call as
// in flight, so ServiceShutdown waits for it before it closes the store. The
// caller defers the returned func.
func (a *AssistantService) enter() (*runner, *store.Store, func(), error) {
	a.life.RLock()
	r, st, err := a.ready()
	if err != nil {
		a.life.RUnlock()
		return nil, nil, nil, err
	}
	return r, st, a.life.RUnlock, nil
}

// ready returns the runner and the store, or why the assistant is not there.
func (a *AssistantService) ready() (*runner, *store.Store, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.run == nil || a.st == nil {
		e := &Error{Code: CodeUnavailable, Message: "The assistant is not available."}
		if a.startErr != nil {
			e.Detail = a.startErr.Error()
		}
		return nil, nil, e
	}
	return a.run, a.st, nil
}

// Send adds the user's message to a conversation and starts a run. It returns
// the run's id at once; the answer arrives as chat:* events. attachmentIDs
// are attachments staged in this conversation (AddAttachment,
// AddPastedImage); an id of another conversation, or one already sent, is
// refused. The text may be empty when there are attachments.
func (a *AssistantService) Send(convID, text string, attachmentIDs []string) (string, error) {
	r, st, done, err := a.enter()
	if err != nil {
		return "", err
	}
	defer done()
	if len([]rune(text)) > maxSendChars {
		return "", invalid("text", "That message is too long.")
	}
	conv, err := st.GetConversation(convID)
	if err != nil {
		return "", wrapStoreErr(err)
	}
	var parts []llm.Part
	firstName := ""
	if len(attachmentIDs) > 0 {
		if len(attachmentIDs) > attach.MaxPerMessage {
			return "", invalid("attachments", fmt.Sprintf("A message can carry at most %d attachments.", attach.MaxPerMessage))
		}
		seen := map[string]bool{}
		for _, id := range attachmentIDs {
			if seen[id] {
				return "", invalid("attachments", "The same attachment is listed twice.")
			}
			seen[id] = true
		}
		atts, err := st.StagedByID(convID, attachmentIDs)
		if errors.Is(err, store.ErrNotFound) {
			return "", invalid("attachments", "An attachment is no longer there. Attach the file again.")
		}
		if err != nil {
			return "", wrapStoreErr(err)
		}
		if parts, err = attachmentParts(atts, a.convTakesImages(st, conv), a.convTakesPDFs(st, conv)); err != nil {
			return "", err
		}
		firstName = atts[0].Name
	}
	if strings.TrimSpace(text) != "" || len(parts) == 0 {
		parts = append(parts, llm.Text{Text: text})
	}
	runID, err := r.startParts(convID, text, parts, attachmentIDs)
	if err != nil {
		return "", err
	}
	if conv.Title == "" {
		title := firstLine(text)
		if strings.TrimSpace(title) == "" {
			title = firstName
		}
		_ = st.SetFallbackTitle(convID, clip(title, titleChars))
	}
	return runID, nil
}

// Cancel stops a run. A card that is open counts as declined.
func (a *AssistantService) Cancel(runID string) {
	if r, _, done, err := a.enter(); err == nil {
		defer done()
		r.cancelRun(runID)
	}
}

// Answer settles an approval card of a conversation.
func (a *AssistantService) Answer(convID, approvalID string, approve bool) error {
	r, _, done, err := a.enter()
	if err != nil {
		return err
	}
	defer done()
	return r.answer(convID, approvalID, approve)
}

// Continue resumes a paused run with a fresh step budget.
func (a *AssistantService) Continue(runID string) error {
	r, _, done, err := a.enter()
	if err != nil {
		return err
	}
	defer done()
	return r.continueRun(runID)
}

// PendingApprovals lists the open cards of a conversation, so the UI can show
// them again after a reload.
func (a *AssistantService) PendingApprovals(convID string) []ChatApproval {
	r, _, done, err := a.enter()
	if err != nil {
		return []ChatApproval{}
	}
	defer done()
	out := r.pendingApprovals(convID)
	if out == nil {
		out = []ChatApproval{}
	}
	return out
}

func toConversation(c store.Conversation) Conversation {
	return Conversation{ID: c.ID, Title: c.Title, Site: c.Site, Mode: c.Mode, ProviderID: c.ProviderID, Model: c.Model, ProfileID: c.ProfileID, Created: c.Created, Updated: c.Updated}
}

// NewConversation starts a conversation on a site. mode is "read" (the
// default) or "ask"; an empty model means the provider's default.
func (a *AssistantService) NewConversation(site, mode, providerID, model string) (Conversation, error) {
	_, st, done, err := a.enter()
	if err != nil {
		return Conversation{}, err
	}
	defer done()
	switch mode {
	case "":
		mode = ModeRead
	case ModeRead, ModeAsk:
	default:
		return Conversation{}, invalid("mode", "Choose \"Read only\" or \"Ask before changes\".")
	}
	if site == "" {
		return Conversation{}, invalid("site", "Choose a site.")
	}
	cfg, err := config.Read(a.configPath)
	if err != nil {
		return Conversation{}, &Error{Code: CodeNotFound, Message: "That site is not in your list.", Field: "site"}
	}
	sc, ok := cfg.Sites[site]
	if !ok {
		return Conversation{}, &Error{Code: CodeNotFound, Message: "That site is not in your list.", Field: "site"}
	}
	p, err := a.provider(st, providerID)
	if err != nil {
		return Conversation{}, err
	}
	model, err = checkModel("model", model)
	if err != nil {
		return Conversation{}, err
	}
	if model == "" {
		model = defaultModelOf(p)
	}
	if err := a.checkLocalOnly(st, site, sc.URL, p, model); err != nil {
		return Conversation{}, err
	}
	c, err := st.InsertConversation(store.Conversation{Site: site, Mode: mode, ProviderID: p.ID, Model: model, SiteURL: sc.URL})
	if err != nil {
		return Conversation{}, wrapStoreErr(err)
	}
	return toConversation(c), nil
}

// ListConversations returns every conversation, most recent first.
func (a *AssistantService) ListConversations() ([]Conversation, error) {
	_, st, done, err := a.enter()
	if err != nil {
		return nil, err
	}
	defer done()
	rows, err := st.ListConversations()
	if err != nil {
		return nil, wrapStoreErr(err)
	}
	states, err := st.ConvStates()
	if err != nil {
		return nil, wrapStoreErr(err)
	}
	out := make([]Conversation, len(rows))
	for i, c := range rows {
		out[i] = toConversation(c)
		s := states[c.ID]
		out[i].Pinned, out[i].Archived, out[i].Ephemeral = s.Pinned, s.Archived, s.Ephemeral
	}
	return out, nil
}

// GetConversation returns a conversation with its messages in the shape the
// chat shows. The model's thinking and its signatures never leave Go.
func (a *AssistantService) GetConversation(id string) (ConversationDetail, error) {
	r, st, done, err := a.enter()
	if err != nil {
		return ConversationDetail{}, err
	}
	defer done()
	c, err := st.GetConversation(id)
	if err != nil {
		return ConversationDetail{}, wrapStoreErr(err)
	}
	msgs, err := st.ListMessages(id)
	if err != nil {
		return ConversationDetail{}, wrapStoreErr(err)
	}
	runs, err := st.ListRuns(id)
	if err != nil {
		return ConversationDetail{}, wrapStoreErr(err)
	}
	active := r.activeRunOf(id)
	// The tool calls of each assistant message, in the order the model made
	// them.
	byMsg := map[string][]store.ToolCall{}
	for _, run := range runs {
		calls, err := st.ListToolCalls(run.ID)
		if err != nil {
			return ConversationDetail{}, wrapStoreErr(err)
		}
		for _, tc := range calls {
			byMsg[tc.MsgID] = append(byMsg[tc.MsgID], tc)
		}
	}
	atts, err := st.MessageAttachments(id)
	if err != nil {
		return ConversationDetail{}, wrapStoreErr(err)
	}
	d := ConversationDetail{Conversation: toConversation(c), Messages: []ChatMessage{}, ActiveRunID: active}
	if n := len(runs); n > 0 && runs[n-1].Status == RunPaused && active == "" {
		d.PausedRunID = runs[n-1].ID
	}
	for _, m := range msgs {
		parts, err := llm.UnmarshalParts(m.PartsJSON)
		if err != nil {
			return ConversationDetail{}, newError(CodeFailed, "A saved message could not be read.", err)
		}
		cm := ChatMessage{ID: m.ID, Role: m.Role, Tools: []ChatToolCall{}, Created: m.Created, Attachments: []StagedAttachment{}}
		rows := map[string]store.Attachment{}
		for _, at := range atts[m.ID] {
			rows[at.ID] = at
		}
		var text []string
		next := 0
		for _, p := range parts {
			switch v := p.(type) {
			case llm.Text:
				if v.AttachmentID != "" {
					cm.Attachments = append(cm.Attachments, chatAttachment(rows, v.AttachmentID, attachmentName(v.Text), "text/plain"))
					continue
				}
				text = append(text, v.Text)
			case llm.Image:
				cm.Attachments = append(cm.Attachments, chatAttachment(rows, v.AttachmentID, "image", v.MediaType))
			case llm.Document:
				cm.Attachments = append(cm.Attachments, chatAttachment(rows, v.AttachmentID, v.Name, v.MediaType))
			case llm.ToolUse:
				rows := byMsg[m.ID]
				tc := ChatToolCall{Tool: v.Name, Site: c.Site, Status: ToolStopped}
				if next < len(rows) {
					tc = chatToolCall(rows[next], active)
				}
				next++
				cm.Tools = append(cm.Tools, tc)
			}
		}
		cm.Text = strings.Join(text, "")
		if cm.Text == "" && len(cm.Tools) == 0 && len(cm.Attachments) == 0 {
			continue // tool results and thinking only
		}
		d.Messages = append(d.Messages, cm)
	}
	if d.RunUsage, d.Total, err = conversationUsage(st, runs, d.Messages, id); err != nil {
		return ConversationDetail{}, wrapStoreErr(err)
	}
	return d, nil
}

func chatToolCall(tc store.ToolCall, activeRun string) ChatToolCall {
	status := tc.Status
	if status == ToolRunning && tc.RunID != activeRun {
		status = ToolStopped // its run ended without recording an outcome
	}
	summary := ""
	if status == ToolOK || status == ToolRunning {
		summary = summarizeArgs([]byte(tc.ArgsJSON))
	} else {
		summary = clip(firstLine(tc.ResultText), loopSummaryLimit)
	}
	return ChatToolCall{ID: tc.ID, Tool: tc.Tool, Site: tc.Site, Status: status, Approval: tc.Approval, Summary: redactSecrets(summary)}
}

// DeleteConversation removes a conversation and everything in it. A
// conversation with a run in progress cannot be deleted.
func (a *AssistantService) DeleteConversation(id string) error {
	r, st, done, err := a.enter()
	if err != nil {
		return err
	}
	defer done()
	return r.whileIdle(id, func() error {
		// A title call still out for it ends with the conversation.
		r.cancelTitleLocked(id)
		if err := st.DeleteConversation(id); err != nil {
			return wrapStoreErr(err)
		}
		return nil
	})
}

// SetConversationMode switches a conversation between "read" (read only) and
// "ask" (ask before changes). It is refused while a run is active.
func (a *AssistantService) SetConversationMode(id, mode string) error {
	r, st, done, err := a.enter()
	if err != nil {
		return err
	}
	defer done()
	if mode != ModeRead && mode != ModeAsk {
		return invalid("mode", "Choose \"Read only\" or \"Ask before changes\".")
	}
	return r.whileIdle(id, func() error {
		if err := st.SetConversationMode(id, mode); err != nil {
			return wrapStoreErr(err)
		}
		return nil
	})
}

// ---- providers ----

// callCtx bounds a call to a provider; it also ends when the service shuts
// down.
func (a *AssistantService) callCtx() (context.Context, context.CancelFunc) {
	a.mu.Lock()
	parent := a.ctx
	a.mu.Unlock()
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, providerCallTimeout)
}

// lockProvider serialises the changes to one provider (its row and its key).
func (a *AssistantService) lockProvider(id string) func() {
	m, _ := a.plocks.LoadOrStore(id, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// checkModel trims a model name and refuses an overlong one or one with
// control characters. An empty name is fine.
func checkModel(field, m string) (string, error) {
	m = strings.TrimSpace(m)
	if utf8.RuneCountInString(m) > maxModelChars {
		return "", invalid(field, "That model name is too long.")
	}
	for _, r := range m {
		if unicode.IsControl(r) {
			return "", invalid(field, "That model name has characters that are not allowed.")
		}
	}
	return m, nil
}

// officialBase is the one host a hosted provider talks to ("" for none).
func officialBase(kind string) string {
	switch kind {
	case KindAnthropic:
		return "https://api.anthropic.com"
	case KindOpenRouter:
		return openaicompat.OpenRouter.BaseURL
	case KindOpenAI:
		return openai.BaseURL
	case KindGemini:
		return strings.TrimRight(gemini.BaseURL, "/")
	}
	return ""
}

func kindLabel(kind string) string {
	switch kind {
	case KindAnthropic:
		return "Anthropic"
	case KindOpenRouter:
		return openaicompat.OpenRouter.Label
	case KindOllama:
		return openaicompat.Ollama.Label
	case KindLMStudio:
		return openaicompat.LMStudio.Label
	case KindOpenAI:
		return "OpenAI"
	case KindGemini:
		return "Google Gemini"
	}
	return "Custom"
}

func kindBaseURL(kind string) string {
	switch kind {
	case KindOpenRouter:
		return openaicompat.OpenRouter.BaseURL
	case KindOllama:
		return openaicompat.Ollama.BaseURL
	case KindLMStudio:
		return openaicompat.LMStudio.BaseURL
	}
	return ""
}

func validKind(kind string) bool {
	switch kind {
	case KindAnthropic, KindOpenRouter, KindOllama, KindLMStudio, KindCustom, KindOpenAI, KindGemini:
		return true
	}
	return false
}

// keyRequired reports whether a provider of this kind cannot work without a
// key.
func keyRequired(kind string) bool {
	return kind == KindAnthropic || kind == KindOpenRouter || kind == KindOpenAI || kind == KindGemini
}

func defaultModelOf(p store.Provider) string {
	if p.DefaultModel != "" {
		return p.DefaultModel
	}
	if p.Kind == KindAnthropic {
		return anthropic.DefaultModel
	}
	return ""
}

// checkBaseURL accepts https URLs, and http only for this machine.
func checkBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Hostname() == "" {
		return invalid("baseURL", "Enter a full address, such as https://api.example.com/v1.")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		switch strings.ToLower(u.Hostname()) {
		case "localhost", "127.0.0.1", "::1":
			return nil
		}
		return invalid("baseURL", "Use https, unless the server runs on this computer.")
	}
	return invalid("baseURL", "Enter a full address, such as https://api.example.com/v1.")
}

func (a *AssistantService) info(p store.Provider) ProviderInfo {
	pi := ProviderInfo{ID: p.ID, Kind: p.Kind, Label: p.Label, BaseURL: p.BaseURL, DefaultModel: p.DefaultModel}
	// A keychain that cannot be read shows as "no key" here; using the
	// provider reports the real problem.
	if st, err := a.keys.Status(p.ID); err == nil {
		pi.KeySet, pi.KeyLast4 = st.Set, st.Last4
	}
	return pi
}

// provider returns the stored provider id.
func (a *AssistantService) provider(st *store.Store, id string) (store.Provider, error) {
	if err := validProviderID(id); err != nil {
		return store.Provider{}, err
	}
	list, err := st.ListProviders()
	if err != nil {
		return store.Provider{}, wrapStoreErr(err)
	}
	for _, p := range list {
		if p.ID == id {
			return p, nil
		}
	}
	return store.Provider{}, &Error{Code: CodeNotFound, Message: "That provider is not set up.", Field: "provider"}
}

// ListProviders returns the saved providers, with whether each has a key.
func (a *AssistantService) ListProviders() ([]ProviderInfo, error) {
	_, st, done, err := a.enter()
	if err != nil {
		return nil, err
	}
	defer done()
	list, err := st.ListProviders()
	if err != nil {
		return nil, wrapStoreErr(err)
	}
	out := make([]ProviderInfo, len(list))
	for i, p := range list {
		out[i] = a.info(p)
	}
	return out, nil
}

// SaveProvider adds or changes a provider. An empty ID takes the kind's name
// (a custom provider gets a numbered one). The key is set apart, with SetKey.
// Anthropic, OpenRouter, OpenAI and Gemini are pinned to their own hosts
// (stored as an empty address, except OpenRouter's). When a provider's
// address changes the stored key is deleted in the same step (KeyCleared), so
// a key can never be sent to a host the user did not give it to.
func (a *AssistantService) SaveProvider(p ProviderInfo) (ProviderInfo, error) {
	_, st, done, err := a.enter()
	if err != nil {
		return ProviderInfo{}, err
	}
	defer done()
	if !validKind(p.Kind) {
		return ProviderInfo{}, invalid("kind", "Choose a provider type.")
	}
	id := strings.TrimSpace(p.ID)
	if id == "" {
		id = p.Kind
		if p.Kind == KindCustom {
			existing, err := st.ListProviders()
			if err != nil {
				return ProviderInfo{}, wrapStoreErr(err)
			}
			taken := map[string]bool{}
			for _, e := range existing {
				taken[e.ID] = true
			}
			for n := 2; taken[id]; n++ {
				id = fmt.Sprintf("%s-%d", p.Kind, n)
			}
		}
	}
	if err := validProviderID(id); err != nil {
		return ProviderInfo{}, err
	}
	defer a.lockProvider(id)()
	old, oldErr := a.provider(st, id)
	if oldErr == nil && old.Kind != p.Kind {
		return ProviderInfo{}, invalid("kind", "A provider with this name exists with another type.")
	}
	label := clip(strings.TrimSpace(p.Label), maxLabelChars)
	if label == "" {
		label = kindLabel(p.Kind)
	}
	base := strings.TrimSpace(p.BaseURL)
	if off := officialBase(p.Kind); off != "" {
		// A hosted provider has one host; any other address is refused.
		if base != "" && strings.TrimRight(base, "/") != off {
			return ProviderInfo{}, invalid("baseURL", "This provider's address cannot be changed.")
		}
		base = ""
		if p.Kind == KindOpenRouter {
			base = off
		}
	} else {
		if base == "" {
			base = kindBaseURL(p.Kind)
		}
		if base == "" {
			return ProviderInfo{}, invalid("baseURL", "Enter the server's address.")
		}
		if err := checkBaseURL(base); err != nil {
			return ProviderInfo{}, err
		}
	}
	model, err := checkModel("defaultModel", p.DefaultModel)
	if err != nil {
		return ProviderInfo{}, err
	}
	if model == "" && p.Kind == KindAnthropic {
		model = anthropic.DefaultModel
	}
	row := store.Provider{ID: id, Kind: p.Kind, Label: label, BaseURL: base, DefaultModel: model}
	cleared := false
	if oldErr == nil && old.BaseURL != base {
		if st2, err := a.keys.Status(id); err == nil && st2.Set {
			if err := a.keys.Delete(id); err != nil {
				return ProviderInfo{}, err
			}
			cleared = true
		} else if err != nil {
			// Unknown state: remove whatever is there.
			if err := a.keys.Delete(id); err != nil {
				return ProviderInfo{}, err
			}
		}
	}
	if err := st.UpsertProvider(row); err != nil {
		return ProviderInfo{}, wrapStoreErr(err)
	}
	pi := a.info(row)
	pi.KeyCleared = cleared
	return pi, nil
}

// DeleteProvider removes a provider and its key.
func (a *AssistantService) DeleteProvider(id string) error {
	_, st, done, err := a.enter()
	if err != nil {
		return err
	}
	defer done()
	if err := validProviderID(id); err != nil {
		return err
	}
	defer a.lockProvider(id)()
	if _, err := a.provider(st, id); err != nil {
		return err
	}
	if err := a.keys.Delete(id); err != nil {
		return err
	}
	if err := st.DeleteProvider(id); err != nil {
		return wrapStoreErr(err)
	}
	return nil
}

// SetKey checks a key against the provider and saves it in the OS keychain
// only when the provider accepts it. A key the provider refuses is CodeAuth;
// any other failure to check (timeout, network, server error) is CodeNetwork.
// Either way nothing is saved and the earlier key stays.
func (a *AssistantService) SetKey(providerID, key string) error {
	_, st, done, err := a.enter()
	if err != nil {
		return err
	}
	defer done()
	if err := validProviderID(providerID); err != nil {
		return err
	}
	defer a.lockProvider(providerID)()
	p, err := a.provider(st, providerID)
	if err != nil {
		return err
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	return a.verifyAndSaveKey(ctx, p, key)
}

// verifyAndSaveKey is SetKey's check and save, for a key typed in or one
// from a browser sign-in: the provider must accept the key (a Models call
// within ctx) before it goes to the keychain. The caller holds
// lockProvider(p.ID).
func (a *AssistantService) verifyAndSaveKey(ctx context.Context, p store.Provider, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return invalid("key", "Paste the API key.")
	}
	if len(key) > maxKeyBytes {
		return invalid("key", "That key is too long.")
	}
	prov, err := a.mk(p, key)
	if err != nil {
		return scrubKey(toServiceError(err), key)
	}
	if _, err := prov.Models(ctx); err != nil {
		var se *Error
		switch {
		case llm.IsAuth(err):
			se = newError(CodeAuth, "The provider did not accept the API key.", err)
		case ctx.Err() != nil:
			se = newError(CodeNetwork, "The provider did not answer in time. The key was not saved.", nil)
		default:
			se = newError(CodeNetwork, "The key could not be checked. It was not saved.", err)
		}
		return scrubKey(se, key)
	}
	return a.keys.Set(p.ID, key)
}

// scrubKey removes the key from an error's text, whatever it holds.
func scrubKey(e *Error, key string) *Error {
	c := *e
	c.Message = strings.ReplaceAll(c.Message, key, "[hidden]")
	c.Detail = strings.ReplaceAll(c.Detail, key, "[hidden]")
	return &c
}

// KeyStatus says whether a provider has a key, and its last four characters.
func (a *AssistantService) KeyStatus(providerID string) (KeyStatus, error) {
	_, _, done, err := a.enter()
	if err != nil {
		return KeyStatus{}, err
	}
	defer done()
	return a.keys.Status(providerID)
}

// DetectLocal looks for Ollama and LM Studio on this computer. It saves
// nothing: SaveProvider adds one.
func (a *AssistantService) DetectLocal() []ProviderInfo {
	ctx, cancel := a.callCtx()
	defer cancel()
	out := []ProviderInfo{}
	for _, p := range a.detect(ctx) {
		out = append(out, ProviderInfo{ID: p.ID, Kind: p.ID, Label: p.Label, BaseURL: p.BaseURL})
	}
	return out
}

// ListModels lists the models a provider offers.
func (a *AssistantService) ListModels(providerID string) ([]Model, error) {
	_, st, done, err := a.enter()
	if err != nil {
		return nil, err
	}
	defer done()
	p, err := a.provider(st, providerID)
	if err != nil {
		return nil, err
	}
	key, err := a.keyFor(p)
	if err != nil {
		return nil, err
	}
	prov, err := a.mk(p, key)
	if err != nil {
		return nil, scrubKey(toServiceError(err), key)
	}
	ctx, cancel := a.callCtx()
	defer cancel()
	list, err := prov.Models(ctx)
	if err != nil {
		return nil, scrubKey(toServiceError(err), key)
	}
	def := defaultModelOf(p)
	out := make([]Model, len(list))
	for i, m := range list {
		out[i] = Model{ID: m.ID, Label: m.Label, Default: def != "" && m.ID == def}
	}
	return out, nil
}

// keyFor reads a provider's key. A kind that works without one gets "".
func (a *AssistantService) keyFor(p store.Provider) (string, error) {
	key, err := a.keys.Get(p.ID)
	if err == nil {
		return key, nil
	}
	var se *Error
	if errors.As(err, &se) && se.Code == CodeNotFound {
		if keyRequired(p.Kind) {
			return "", &Error{Code: CodeAuth, Message: "Add the API key for " + p.Label + " first.", Field: "key"}
		}
		return "", nil
	}
	return "", err
}

// providerFor is the runner's providerFunc: the client and model of a
// conversation. Local servers get the whole tool list like the others; a
// small model may call tools poorly, which the chat shows as it happens.
func (a *AssistantService) providerFor(conv store.Conversation) (llm.Provider, string, error) {
	_, st, err := a.ready()
	if err != nil {
		return nil, "", err
	}
	p, err := a.provider(st, conv.ProviderID)
	if err != nil {
		return nil, "", &Error{Code: CodeNotFound, Message: "The provider of this conversation was removed. Set it up again or start a new conversation.", Field: "provider"}
	}
	// The run checked the site and the local-only rule (checkRun) just
	// before; checked here too, so no caller gets a client that skips it.
	if err := a.checkLocalOnly(st, conv.Site, conv.SiteURL, p, conv.Model); err != nil {
		return nil, "", err
	}
	key, err := a.keyFor(p)
	if err != nil {
		return nil, "", err
	}
	prov, err := a.mk(p, key)
	if err != nil {
		return nil, "", scrubKey(toServiceError(err), key)
	}
	model := conv.Model
	if model == "" {
		model = defaultModelOf(p)
	}
	if model == "" {
		return nil, "", invalid("model", "Choose a model for this conversation.")
	}
	return prov, model, nil
}

// makeProvider builds the client of a provider row.
func makeProvider(p store.Provider, key string) (llm.Provider, error) {
	switch p.Kind {
	case KindAnthropic:
		// Pinned to the official host: a stored address is never used.
		return anthropic.New(key), nil
	case KindOpenAI:
		// Pinned too: the Responses API at api.openai.com only.
		return openai.New(key), nil
	case KindGemini:
		// Pinned too: the Gemini API (never Vertex AI).
		g, err := gemini.New(key)
		if err != nil {
			return nil, newError(CodeAuth, "Add the API key for Google Gemini first.", nil)
		}
		return g, nil
	case KindOpenRouter, KindOllama, KindLMStudio, KindCustom:
		preset := openaicompat.Custom(p.BaseURL)
		switch p.Kind {
		case KindOpenRouter:
			preset = openaicompat.OpenRouter
		case KindOllama:
			preset = openaicompat.Ollama
		case KindLMStudio:
			preset = openaicompat.LMStudio
		}
		// OpenRouter is pinned to its own host too.
		if p.Kind != KindOpenRouter && p.BaseURL != "" {
			preset.BaseURL = p.BaseURL
		}
		return openaicompat.New(preset, key), nil
	}
	return nil, invalid("kind", "Unknown provider type.")
}
