package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ConvState is what the history list adds to a conversation: pinned and
// archived are the user's, ephemeral comes from a profile that keeps no
// history.
type ConvState struct {
	Pinned    bool
	Archived  bool
	Ephemeral bool
}

// ConvStates returns the state of every conversation, by id.
func (s *Store) ConvStates() (map[string]ConvState, error) {
	rows, err := s.db.Query(`SELECT id,pinned,archived,ephemeral FROM conversations`)
	if err != nil {
		return nil, fmt.Errorf("list conversation states: %w", err)
	}
	defer rows.Close()
	out := map[string]ConvState{}
	for rows.Next() {
		var id string
		var st ConvState
		if err := rows.Scan(&id, &st.Pinned, &st.Archived, &st.Ephemeral); err != nil {
			return nil, fmt.Errorf("list conversation states: %w", err)
		}
		out[id] = st
	}
	if err := rowsErr(rows, "list conversation states"); err != nil {
		return nil, err
	}
	return out, nil
}

// GetConvState returns ErrNotFound when id is unknown.
func (s *Store) GetConvState(id string) (ConvState, error) {
	var st ConvState
	err := s.db.QueryRow(`SELECT pinned,archived,ephemeral FROM conversations WHERE id=?`, id).Scan(&st.Pinned, &st.Archived, &st.Ephemeral)
	if errors.Is(err, sql.ErrNoRows) {
		return ConvState{}, fmt.Errorf("get conversation state: %w", ErrNotFound)
	}
	if err != nil {
		return ConvState{}, fmt.Errorf("get conversation state: %w", err)
	}
	return st, nil
}

// SetPinned pins or unpins a conversation without touching the updated time.
func (s *Store) SetPinned(id string, on bool) error {
	return execOne("pin conversation", s.db, `UPDATE conversations SET pinned=? WHERE id=?`, on, id)
}

// SetArchived archives or restores a conversation without touching the
// updated time.
func (s *Store) SetArchived(id string, on bool) error {
	return execOne("archive conversation", s.db, `UPDATE conversations SET archived=? WHERE id=?`, on, id)
}

// SetEphemeral marks a conversation as one that is not kept (see
// DeleteEphemeral), without touching the updated time.
func (s *Store) SetEphemeral(id string, on bool) error {
	return execOne("set conversation ephemeral", s.db, `UPDATE conversations SET ephemeral=? WHERE id=?`, on, id)
}

// DeleteEphemeral removes every ephemeral conversation and returns how many.
func (s *Store) DeleteEphemeral() (int, error) {
	res, err := s.db.Exec(`DELETE FROM conversations WHERE ephemeral=1`)
	if err != nil {
		return 0, fmt.Errorf("delete ephemeral conversations: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// GetSetting returns the value of an app setting, "" when it is not set.
func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get setting: %w", err)
	}
	return v, nil
}

// SetSetting saves an app setting.
func (s *Store) SetSetting(key, value string) error {
	if _, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil {
		return fmt.Errorf("save setting: %w", err)
	}
	return nil
}

// SweepRetention deletes the conversations last updated before cutoff, with
// everything in them. Pinned conversations, conversations with a paused run
// and the ids in keep (conversations with a run in progress) stay. It returns
// how many were deleted. OptimizeSearch compacts the search index afterwards.
func (s *Store) SweepRetention(cutoff time.Time, keep []string) (int, error) {
	q := `DELETE FROM conversations WHERE updated<? AND pinned=0
		AND id NOT IN (SELECT conv_id FROM runs WHERE status='paused')`
	args := []any{cutoff.UnixMilli()}
	if len(keep) > 0 {
		q += ` AND id NOT IN (` + strings.TrimSuffix(strings.Repeat("?,", len(keep)), ",") + `)`
		for _, id := range keep {
			args = append(args, id)
		}
	}
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return 0, fmt.Errorf("sweep conversations: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// OptimizeSearch compacts the search index. It can take a while on a large
// history, so a caller does it after a sweep that deleted something, not
// while it holds a lock the runs wait for.
func (s *Store) OptimizeSearch() error {
	if _, err := s.db.Exec(`INSERT INTO messages_fts(messages_fts) VALUES('optimize')`); err != nil {
		return fmt.Errorf("optimize search index: %w", err)
	}
	return nil
}

// SearchFilter narrows a search. Zero values mean no limit on that field.
type SearchFilter struct {
	Site      string
	ProfileID string
	// From and To bound the conversation's updated time (both inclusive).
	From time.Time
	To   time.Time
	// Archived searches the archived conversations instead of the others.
	Archived bool
	// Limit is the most hits returned: 0 means 50, at most 200.
	Limit int
}

// SearchHit is one message that matches a search.
type SearchHit struct {
	ConvID  string
	Title   string
	Site    string
	MsgID   string
	Snippet string
	Updated time.Time
	Pinned  bool
}

// SearchConversations returns the messages whose indexed text contains every
// word of query, best match first, with a snippet of the match. Ephemeral
// conversations are not searched.
func (s *Store) SearchConversations(query string, f SearchFilter) ([]SearchHit, error) {
	q := ftsQuery(query)
	if q == "" {
		return nil, nil
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	where := []string{"messages_fts MATCH ?", "c.ephemeral=0", "c.archived=?"}
	args := []any{q, f.Archived}
	if f.Site != "" {
		where = append(where, "c.site=?")
		args = append(args, f.Site)
	}
	if f.ProfileID != "" {
		where = append(where, "c.profile_id=?")
		args = append(args, f.ProfileID)
	}
	if !f.From.IsZero() {
		where = append(where, "c.updated>=?")
		args = append(args, f.From.UnixMilli())
	}
	if !f.To.IsZero() {
		where = append(where, "c.updated<=?")
		args = append(args, f.To.UnixMilli())
	}
	args = append(args, limit)
	rows, err := s.db.Query(`SELECT m.conv_id,c.title,c.site,m.id,snippet(messages_fts,0,'','','…',16),c.updated,c.pinned
		FROM messages_fts JOIN messages m ON m.n=messages_fts.rowid
		JOIN conversations c ON c.id=m.conv_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY bm25(messages_fts), c.updated DESC, m.conv_id, m.seq LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadQuery, err)
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		var up int64
		if err := rows.Scan(&h.ConvID, &h.Title, &h.Site, &h.MsgID, &h.Snippet, &up, &h.Pinned); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBadQuery, err)
		}
		h.Snippet = strings.Join(strings.Fields(h.Snippet), " ")
		h.Updated = ms(up)
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadQuery, err)
	}
	return out, nil
}

// UsageRow is one usage row with every column (Usage has the token counts
// only).
type UsageRow struct {
	RunID      string
	Turn       int
	Kind       string
	Input      int
	Output     int
	Cached     int
	CacheWrite int
	CostUSD    *float64
	CostSource string
	PriceDate  string
}

// ListUsageRows returns a run's usage rows, every kind, by turn.
func (s *Store) ListUsageRows(runID string) ([]UsageRow, error) {
	rows, err := s.db.Query(`SELECT run_id,turn,kind,input,output,cached,cache_write,cost_usd,cost_source,price_date FROM usage WHERE run_id=? ORDER BY turn, kind`, runID)
	if err != nil {
		return nil, fmt.Errorf("list usage rows: %w", err)
	}
	defer rows.Close()
	var out []UsageRow
	for rows.Next() {
		var u UsageRow
		var cost sql.NullFloat64
		if err := rows.Scan(&u.RunID, &u.Turn, &u.Kind, &u.Input, &u.Output, &u.Cached, &u.CacheWrite, &cost, &u.CostSource, &u.PriceDate); err != nil {
			return nil, fmt.Errorf("list usage rows: %w", err)
		}
		if cost.Valid {
			c := cost.Float64
			u.CostUSD = &c
		}
		out = append(out, u)
	}
	if err := rowsErr(rows, "list usage rows"); err != nil {
		return nil, err
	}
	return out, nil
}

// ExportData is a conversation with everything that belongs to it, or the
// parts of a file to import: Import uses the ids only to tell which message
// or run a row points to.
type ExportData struct {
	Conversation Conversation
	Messages     []Message
	Runs         []Run
	ToolCalls    []ToolCall
	Usage        []UsageRow
}

// ReadConversation returns a conversation with its messages, runs, tool
// calls and usage. ErrNotFound when id is unknown.
func (s *Store) ReadConversation(id string) (ExportData, error) {
	var d ExportData
	var err error
	if d.Conversation, err = s.GetConversation(id); err != nil {
		return ExportData{}, err
	}
	if d.Messages, err = s.ListMessages(id); err != nil {
		return ExportData{}, err
	}
	if d.Runs, err = s.ListRuns(id); err != nil {
		return ExportData{}, err
	}
	for _, r := range d.Runs {
		calls, err := s.ListToolCalls(r.ID)
		if err != nil {
			return ExportData{}, err
		}
		d.ToolCalls = append(d.ToolCalls, calls...)
		u, err := s.ListUsageRows(r.ID)
		if err != nil {
			return ExportData{}, err
		}
		d.Usage = append(d.Usage, u...)
	}
	return d, nil
}

// ErrBadImport is returned by ImportConversation for data that does not hold
// together (a duplicate id, a row that points to nothing).
var ErrBadImport = errors.New("store: inconsistent conversation data")

func endMS(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixMilli()
}

// ImportedPrefix starts the id of every message ImportConversation stores. A
// message with it came from a file, so it is shown but never replayed to a
// model as a turn of its own (see IsImportedID).
const ImportedPrefix = "imp_"

// IsImportedID reports whether a message id belongs to an imported message.
func IsImportedID(id string) bool { return strings.HasPrefix(id, ImportedPrefix) }

// ImportConversation stores d as a new conversation. Every id is made anew:
// the ids in d only link its rows to each other, and a row that points to an
// id d does not hold is ErrBadImport. The conversation's own times are now;
// its messages, runs and tool calls keep theirs. Everything is stored or
// nothing is.
func (s *Store) ImportConversation(d ExportData) (Conversation, error) {
	c := d.Conversation
	now := nowMS()
	c.ID, c.Created, c.Updated = newID(), ms(now), ms(now)
	tx, err := s.db.Begin()
	if err != nil {
		return Conversation{}, fmt.Errorf("import conversation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO conversations(id,title,site,mode,provider_id,model,profile_id,site_url,site_context,site_context_key,created,updated) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.Title, c.Site, c.Mode, c.ProviderID, c.Model, c.ProfileID, c.SiteURL, c.SiteContext, c.SiteContextKey, now, now); err != nil {
		return Conversation{}, fmt.Errorf("import conversation: %w", err)
	}
	msgs := make(map[string]string, len(d.Messages))
	for i, m := range d.Messages {
		if _, dup := msgs[m.ID]; dup || m.ID == "" {
			return Conversation{}, fmt.Errorf("import conversation: message id %q: %w", m.ID, ErrBadImport)
		}
		id := ImportedPrefix + newID()
		msgs[m.ID] = id
		if _, err := tx.Exec(`INSERT INTO messages(id,conv_id,seq,role,parts_json,created) VALUES(?,?,?,?,?,?)`,
			id, c.ID, i+1, m.Role, m.PartsJSON, endMS(m.Created)); err != nil {
			return Conversation{}, fmt.Errorf("import conversation: %w", err)
		}
	}
	runs := make(map[string]string, len(d.Runs))
	for _, r := range d.Runs {
		if _, dup := runs[r.ID]; dup || r.ID == "" {
			return Conversation{}, fmt.Errorf("import conversation: run id %q: %w", r.ID, ErrBadImport)
		}
		id := newID()
		runs[r.ID] = id
		if _, err := tx.Exec(`INSERT INTO runs(id,conv_id,status,steps,error,started,ended) VALUES(?,?,?,?,?,?,?)`,
			id, c.ID, r.Status, r.Steps, r.Error, endMS(r.Started), endMS(r.Ended)); err != nil {
			return Conversation{}, fmt.Errorf("import conversation: %w", err)
		}
	}
	for _, t := range d.ToolCalls {
		runID, ok := runs[t.RunID]
		if !ok {
			return Conversation{}, fmt.Errorf("import conversation: tool call %q points to an unknown run: %w", t.ID, ErrBadImport)
		}
		var msg any
		if t.MsgID != "" {
			id, ok := msgs[t.MsgID]
			if !ok {
				return Conversation{}, fmt.Errorf("import conversation: tool call %q points to an unknown message: %w", t.ID, ErrBadImport)
			}
			msg = id
		}
		if _, err := tx.Exec(`INSERT INTO tool_calls(id,run_id,msg_id,tool,site,args_json,result_text,status,approval,started,ended) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			newID(), runID, msg, t.Tool, t.Site, t.ArgsJSON, t.ResultText, t.Status, t.Approval, endMS(t.Started), endMS(t.Ended)); err != nil {
			return Conversation{}, fmt.Errorf("import conversation: %w", err)
		}
	}
	for _, u := range d.Usage {
		runID, ok := runs[u.RunID]
		if !ok {
			return Conversation{}, fmt.Errorf("import conversation: usage points to an unknown run: %w", ErrBadImport)
		}
		var cost any
		if u.CostUSD != nil {
			cost = *u.CostUSD
		}
		kind := u.Kind
		if kind == "" {
			kind = "turn"
		}
		if _, err := tx.Exec(`INSERT INTO usage(run_id,turn,kind,input,output,cached,cache_write,cost_usd,cost_source,price_date) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			runID, u.Turn, kind, u.Input, u.Output, u.Cached, u.CacheWrite, cost, u.CostSource, u.PriceDate); err != nil {
			return Conversation{}, fmt.Errorf("import conversation: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Conversation{}, fmt.Errorf("import conversation: %w", err)
	}
	return c, nil
}
