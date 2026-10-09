package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Conversation is one chat thread bound to a site, write mode and model.
type Conversation struct {
	ID         string
	Title      string
	Site       string
	Mode       string
	ProviderID string
	Model      string
	// ProfileID is "" (no profile), a preset id or a profiles row.
	ProfileID string
	// SiteURL is the site's address when the conversation was created.
	SiteURL string
	// SiteContext is the short site description collected at the first
	// run; "" means not collected yet.
	SiteContext string
	Created     time.Time
	Updated     time.Time
}

// Message is one turn. PartsJSON is opaque to the store except that parts
// shaped {"text": "..."} are indexed for search.
type Message struct {
	ID        string
	ConvID    string
	Seq       int
	Role      string
	PartsJSON string
	Created   time.Time
}

// Run is one assistant run (a user message through the final answer).
type Run struct {
	ID      string
	ConvID  string
	Status  string
	Steps   int
	Error   string
	Started time.Time
	Ended   time.Time // zero while running
}

// ToolCall is one tool invocation within a run.
type ToolCall struct {
	ID         string
	RunID      string
	MsgID      string
	Tool       string
	Site       string
	ArgsJSON   string
	ResultText string
	Status     string
	Approval   string
	Started    time.Time
	Ended      time.Time // zero while running
}

// Usage is the token count of one model turn.
type Usage struct {
	RunID  string
	Turn   int
	Input  int
	Output int
	Cached int
}

// Provider holds non-secret provider settings. Keys live in the keychain.
type Provider struct {
	ID           string
	Kind         string
	Label        string
	BaseURL      string
	DefaultModel string
}

// CreateConversation inserts a conversation and returns it.
func (s *Store) CreateConversation(title, site, mode, providerID, model string) (Conversation, error) {
	return s.InsertConversation(Conversation{Title: title, Site: site, Mode: mode, ProviderID: providerID, Model: model})
}

// InsertConversation inserts c with a new ID and the current time (c's own
// ID, times and SiteContext are ignored) and returns it.
func (s *Store) InsertConversation(c Conversation) (Conversation, error) {
	now := nowMS()
	c.ID, c.SiteContext, c.Created, c.Updated = newID(), "", ms(now), ms(now)
	_, err := s.db.Exec(`INSERT INTO conversations(id,title,site,mode,provider_id,model,profile_id,site_url,created,updated) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.Title, c.Site, c.Mode, c.ProviderID, c.Model, c.ProfileID, c.SiteURL, now, now)
	if err != nil {
		return Conversation{}, fmt.Errorf("create conversation: %w", err)
	}
	return c, nil
}

const convCols = `id,title,site,mode,provider_id,model,profile_id,site_url,site_context,created,updated`

type scanner interface{ Scan(...any) error }

func scanConv(r scanner) (Conversation, error) {
	var c Conversation
	var cr, up int64
	if err := r.Scan(&c.ID, &c.Title, &c.Site, &c.Mode, &c.ProviderID, &c.Model, &c.ProfileID, &c.SiteURL, &c.SiteContext, &cr, &up); err != nil {
		return Conversation{}, err
	}
	c.Created, c.Updated = ms(cr), ms(up)
	return c, nil
}

// GetConversation returns ErrNotFound when id is unknown.
func (s *Store) GetConversation(id string) (Conversation, error) {
	c, err := scanConv(s.db.QueryRow(`SELECT `+convCols+` FROM conversations WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, fmt.Errorf("get conversation: %w", ErrNotFound)
	}
	if err != nil {
		return Conversation{}, fmt.Errorf("get conversation: %w", err)
	}
	return c, nil
}

// ListConversations returns all conversations, most recently updated first.
func (s *Store) ListConversations() ([]Conversation, error) {
	rows, err := s.db.Query(`SELECT ` + convCols + ` FROM conversations ORDER BY updated DESC, created DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		c, err := scanConv(rows)
		if err != nil {
			return nil, fmt.Errorf("list conversations: %w", err)
		}
		out = append(out, c)
	}
	if err := rowsErr(rows, "list conversations"); err != nil {
		return nil, err
	}
	return out, nil
}

// RenameConversation changes the title without touching the updated time.
func (s *Store) RenameConversation(id, title string) error {
	return execOne("rename conversation", s.db, `UPDATE conversations SET title=? WHERE id=?`, title, id)
}

// DeleteConversation removes the conversation and, by cascade, its messages,
// runs, tool calls, usage and search entries.
func (s *Store) DeleteConversation(id string) error {
	return execOne("delete conversation", s.db, `DELETE FROM conversations WHERE id=?`, id)
}

// AppendMessage adds a message with the next per-conversation seq (from 1) and
// bumps the conversation's updated time.
func (s *Store) AppendMessage(convID, role, partsJSON string) (Message, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Message{}, fmt.Errorf("append message: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := nowMS()
	if err := execOne("append message", tx, `UPDATE conversations SET updated=? WHERE id=?`, now, convID); err != nil {
		return Message{}, err
	}
	m := Message{ID: newID(), ConvID: convID, Role: role, PartsJSON: partsJSON, Created: ms(now)}
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq),0)+1 FROM messages WHERE conv_id=?`, convID).Scan(&m.Seq); err != nil {
		return Message{}, fmt.Errorf("append message: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO messages(id,conv_id,seq,role,parts_json,created) VALUES(?,?,?,?,?,?)`,
		m.ID, convID, m.Seq, role, partsJSON, now); err != nil {
		return Message{}, fmt.Errorf("append message: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Message{}, fmt.Errorf("append message: %w", err)
	}
	return m, nil
}

// UpdateMessageParts replaces a message's parts (and its search entry) and
// bumps the conversation's updated time.
func (s *Store) UpdateMessageParts(id, partsJSON string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("update message: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := execOne("update message", tx, `UPDATE messages SET parts_json=? WHERE id=?`, partsJSON, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE conversations SET updated=? WHERE id=(SELECT conv_id FROM messages WHERE id=?)`, nowMS(), id); err != nil {
		return fmt.Errorf("update message: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("update message: %w", err)
	}
	return nil
}

// DeleteMessage removes one message and its search entry.
func (s *Store) DeleteMessage(id string) error {
	return execOne("delete message", s.db, `DELETE FROM messages WHERE id=?`, id)
}

func scanMessages(rows *sql.Rows, what string) ([]Message, error) {
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var cr int64
		if err := rows.Scan(&m.ID, &m.ConvID, &m.Seq, &m.Role, &m.PartsJSON, &cr); err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		m.Created = ms(cr)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return out, nil
}

// ListMessages returns a conversation's messages in seq order.
func (s *Store) ListMessages(convID string) ([]Message, error) {
	rows, err := s.db.Query(`SELECT id,conv_id,seq,role,parts_json,created FROM messages WHERE conv_id=? ORDER BY seq`, convID)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	return scanMessages(rows, "list messages")
}

// CreateRun starts a run with status "running".
func (s *Store) CreateRun(convID string) (Run, error) {
	now := nowMS()
	r := Run{ID: newID(), ConvID: convID, Status: "running", Started: ms(now)}
	if _, err := s.db.Exec(`INSERT INTO runs(id,conv_id,status,steps,error,started) VALUES(?,?,?,0,'',?)`, r.ID, convID, r.Status, now); err != nil {
		return Run{}, fkNotFound("create run", err)
	}
	return r, nil
}

// FinishRun records the final status, error text and step count.
func (s *Store) FinishRun(id, status, errText string, steps int) error {
	return execOne("finish run", s.db, `UPDATE runs SET status=?,error=?,steps=?,ended=? WHERE id=?`, status, errText, steps, nowMS(), id)
}

// GetRun returns ErrNotFound when id is unknown.
func (s *Store) GetRun(id string) (Run, error) {
	var r Run
	var st, en int64
	err := s.db.QueryRow(`SELECT id,conv_id,status,steps,error,started,ended FROM runs WHERE id=?`, id).
		Scan(&r.ID, &r.ConvID, &r.Status, &r.Steps, &r.Error, &st, &en)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, fmt.Errorf("get run: %w", ErrNotFound)
	}
	if err != nil {
		return Run{}, fmt.Errorf("get run: %w", err)
	}
	r.Started = ms(st)
	if en != 0 {
		r.Ended = ms(en)
	}
	return r, nil
}

// InsertToolCall records a started call (status as given, usually "running").
// ID and Started are set by the store; msgID may be empty.
func (s *Store) InsertToolCall(runID, msgID, tool, site, argsJSON, status string) (ToolCall, error) {
	now := nowMS()
	t := ToolCall{ID: newID(), RunID: runID, MsgID: msgID, Tool: tool, Site: site, ArgsJSON: argsJSON, Status: status, Started: ms(now)}
	var msg any
	if msgID != "" {
		msg = msgID
	}
	if _, err := s.db.Exec(`INSERT INTO tool_calls(id,run_id,msg_id,tool,site,args_json,status,started) VALUES(?,?,?,?,?,?,?,?)`,
		t.ID, runID, msg, tool, site, argsJSON, status, now); err != nil {
		return ToolCall{}, fkNotFound("insert tool call", err)
	}
	return t, nil
}

// FinishToolCall records the result text, final status and approval outcome.
func (s *Store) FinishToolCall(id, resultText, status, approval string) error {
	return execOne("finish tool call", s.db, `UPDATE tool_calls SET result_text=?,status=?,approval=?,ended=? WHERE id=?`, resultText, status, approval, nowMS(), id)
}

// ListToolCalls returns a run's calls in start order.
func (s *Store) ListToolCalls(runID string) ([]ToolCall, error) {
	rows, err := s.db.Query(`SELECT id,run_id,COALESCE(msg_id,''),tool,site,args_json,result_text,status,approval,started,ended FROM tool_calls WHERE run_id=? ORDER BY started, rowid`, runID)
	if err != nil {
		return nil, fmt.Errorf("list tool calls: %w", err)
	}
	defer rows.Close()
	var out []ToolCall
	for rows.Next() {
		var t ToolCall
		var st, en int64
		if err := rows.Scan(&t.ID, &t.RunID, &t.MsgID, &t.Tool, &t.Site, &t.ArgsJSON, &t.ResultText, &t.Status, &t.Approval, &st, &en); err != nil {
			return nil, fmt.Errorf("list tool calls: %w", err)
		}
		t.Started = ms(st)
		if en != 0 {
			t.Ended = ms(en)
		}
		out = append(out, t)
	}
	if err := rowsErr(rows, "list tool calls"); err != nil {
		return nil, err
	}
	return out, nil
}

// AddUsage records (or replaces) the token usage of one turn of a run.
func (s *Store) AddUsage(u Usage) error {
	_, err := s.db.Exec(`INSERT INTO usage(run_id,turn,input,output,cached) VALUES(?,?,?,?,?)
		ON CONFLICT(run_id,turn) DO UPDATE SET input=excluded.input,output=excluded.output,cached=excluded.cached`,
		u.RunID, u.Turn, u.Input, u.Output, u.Cached)
	if err != nil {
		return fmt.Errorf("add usage: %w", err)
	}
	return nil
}

// ListUsage returns a run's usage rows by turn.
func (s *Store) ListUsage(runID string) ([]Usage, error) {
	rows, err := s.db.Query(`SELECT run_id,turn,input,output,cached FROM usage WHERE run_id=? ORDER BY turn`, runID)
	if err != nil {
		return nil, fmt.Errorf("list usage: %w", err)
	}
	defer rows.Close()
	var out []Usage
	for rows.Next() {
		var u Usage
		if err := rows.Scan(&u.RunID, &u.Turn, &u.Input, &u.Output, &u.Cached); err != nil {
			return nil, fmt.Errorf("list usage: %w", err)
		}
		out = append(out, u)
	}
	if err := rowsErr(rows, "list usage"); err != nil {
		return nil, err
	}
	return out, nil
}

// UpsertProvider inserts or replaces a provider by ID.
func (s *Store) UpsertProvider(p Provider) error {
	_, err := s.db.Exec(`INSERT INTO providers(id,kind,label,base_url,default_model) VALUES(?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,label=excluded.label,base_url=excluded.base_url,default_model=excluded.default_model`,
		p.ID, p.Kind, p.Label, p.BaseURL, p.DefaultModel)
	if err != nil {
		return fmt.Errorf("save provider: %w", err)
	}
	return nil
}

// ListProviders returns providers ordered by label then ID.
func (s *Store) ListProviders() ([]Provider, error) {
	rows, err := s.db.Query(`SELECT id,kind,label,base_url,default_model FROM providers ORDER BY label, id`)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		var p Provider
		if err := rows.Scan(&p.ID, &p.Kind, &p.Label, &p.BaseURL, &p.DefaultModel); err != nil {
			return nil, fmt.Errorf("list providers: %w", err)
		}
		out = append(out, p)
	}
	if err := rowsErr(rows, "list providers"); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteProvider removes a provider by ID.
func (s *Store) DeleteProvider(id string) error {
	return execOne("delete provider", s.db, `DELETE FROM providers WHERE id=?`, id)
}

// ErrBadQuery is returned by Search when SQLite rejects the query.
var ErrBadQuery = errors.New("store: invalid search query")

// ftsQuery turns user text into an FTS5 query: every whitespace-separated
// token becomes a quoted string (so "SINV-0001", "a.b" or "it's" are literal
// phrases) and all tokens must match.
func ftsQuery(text string) string {
	fields := strings.Fields(text)
	for i, f := range fields {
		fields[i] = `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
	}
	return strings.Join(fields, " ")
}

// Search returns messages whose indexed text contains every word of query,
// best match first (bm25), then most recently updated conversation first.
// limit <= 0 means 50. An empty query returns nil.
func (s *Store) Search(query string, limit int) ([]Message, error) {
	q := ftsQuery(query)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT m.id,m.conv_id,m.seq,m.role,m.parts_json,m.created
		FROM messages_fts JOIN messages m ON m.n=messages_fts.rowid
		JOIN conversations c ON c.id=m.conv_id
		WHERE messages_fts MATCH ?
		ORDER BY bm25(messages_fts), c.updated DESC, m.conv_id, m.seq LIMIT ?`, q, limit)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadQuery, err)
	}
	msgs, err := scanMessages(rows, "search")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadQuery, err)
	}
	return msgs, nil
}

// ErrNotPaused is returned by ResumeRun when the run is not paused (or is
// unknown), for instance because another caller resumed it first.
var ErrNotPaused = errors.New("store: run is not paused")

// ResumeRun atomically turns a paused run back into a running one. Of two
// concurrent callers only one succeeds.
func (s *Store) ResumeRun(id string) error {
	res, err := s.db.Exec(`UPDATE runs SET status='running',ended=0 WHERE id=? AND status='paused'`, id)
	if err != nil {
		return fmt.Errorf("resume run: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("resume run: %w", ErrNotPaused)
	}
	return nil
}

// AbandonPausedRuns marks the conversation's paused runs done: the user went
// on without continuing them.
func (s *Store) AbandonPausedRuns(convID string) error {
	if _, err := s.db.Exec(`UPDATE runs SET status='done',ended=? WHERE conv_id=? AND status='paused'`, nowMS(), convID); err != nil {
		return fmt.Errorf("abandon paused runs: %w", err)
	}
	return nil
}

// SetConversationMode changes a conversation's write mode without touching
// the updated time.
func (s *Store) SetConversationMode(id, mode string) error {
	return execOne("set conversation mode", s.db, `UPDATE conversations SET mode=? WHERE id=?`, mode, id)
}

// ListRuns returns a conversation's runs, oldest first.
func (s *Store) ListRuns(convID string) ([]Run, error) {
	rows, err := s.db.Query(`SELECT id,conv_id,status,steps,error,started,ended FROM runs WHERE conv_id=? ORDER BY started, rowid`, convID)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		var st, en int64
		if err := rows.Scan(&r.ID, &r.ConvID, &r.Status, &r.Steps, &r.Error, &st, &en); err != nil {
			return nil, fmt.Errorf("list runs: %w", err)
		}
		r.Started = ms(st)
		if en != 0 {
			r.Ended = ms(en)
		}
		out = append(out, r)
	}
	if err := rowsErr(rows, "list runs"); err != nil {
		return nil, err
	}
	return out, nil
}
