package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
)

// settingRetention is the settings key of the retention period, in days.
const settingRetention = "retention_days"

// sweepInterval is how often the retention sweep runs after the one at start.
const sweepInterval = 24 * time.Hour

// maxSearchQuery is the longest search text, in characters.
const maxSearchQuery = 500

// FileDialogs is what the history needs from the desktop shell to ask where
// a file goes or comes from. A path of "" means the user cancelled. The Host
// of the running app (WailsHost) has it; a Host without it makes export and
// import report CodeUnavailable.
type FileDialogs interface {
	SaveFileDialog(title, filename, filterName, pattern string) (string, error)
	OpenFileDialog(title, filterName, pattern string) (string, error)
}

var _ FileDialogs = (*WailsHost)(nil)

// SaveFileDialog asks where to save a file.
func (h *WailsHost) SaveFileDialog(title, filename, filterName, pattern string) (string, error) {
	if h.App == nil {
		return "", errors.New("the app is not running")
	}
	d := h.App.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
		Title:                title,
		Message:              title,
		Filename:             filename,
		CanCreateDirectories: true,
		Filters:              []application.FileFilter{{DisplayName: filterName, Pattern: pattern}},
		Window:               h.App.Window.Current(),
	})
	return dialogPath(d.PromptForSingleSelection())
}

// OpenFileDialog asks for a file to open.
func (h *WailsHost) OpenFileDialog(title, filterName, pattern string) (string, error) {
	if h.App == nil {
		return "", errors.New("the app is not running")
	}
	d := h.App.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:          title,
		Message:        title,
		CanChooseFiles: true,
		Filters:        []application.FileFilter{{DisplayName: filterName, Pattern: pattern}},
		Window:         h.App.Window.Current(),
	})
	return dialogPath(d.PromptForSingleSelection())
}

// dialogPath turns a Wails dialog answer into a path: the Windows dialog
// answers a cancel with an error ("cancelled by user"), the others with an
// empty path.
func dialogPath(path string, err error) (string, error) {
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "cancel") {
			return "", nil
		}
		return "", err
	}
	return path, nil
}

// SearchFilter narrows Search. Empty fields mean no limit.
type SearchFilter struct {
	Site      string `json:"site"`
	ProfileID string `json:"profileID"`
	// From and To are dates ("2026-10-09", local time); a conversation is
	// found when it was last updated on or between them.
	From string `json:"from"`
	To   string `json:"to"`
	// Archived searches the archive instead of the other conversations.
	Archived bool `json:"archived"`
}

// SearchHit is one message that matches a search.
type SearchHit struct {
	ConvID string `json:"convID"`
	Title  string `json:"title"`
	Site   string `json:"site"`
	MsgID  string `json:"msgID"`
	// Snippet is the matching words with some text around them.
	Snippet string    `json:"snippet"`
	Updated time.Time `json:"updated"`
	Pinned  bool      `json:"pinned"`
}

func parseDay(field, s string, endOfDay bool) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		return time.Time{}, invalid(field, "Use a date like 2026-10-09.")
	}
	if endOfDay {
		t = t.AddDate(0, 0, 1).Add(-time.Millisecond)
	}
	return t, nil
}

// Search finds messages by their words, best match first, in the
// conversations that are not archived (or only in the archived ones). limit
// is the most hits returned: 0 means 50, at most 200.
func (a *AssistantService) Search(query string, filter SearchFilter, limit int) ([]SearchHit, error) {
	_, st, done, err := a.enter()
	if err != nil {
		return nil, err
	}
	defer done()
	if utf8.RuneCountInString(query) > maxSearchQuery {
		return nil, invalid("query", "That search text is too long.")
	}
	from, err := parseDay("from", filter.From, false)
	if err != nil {
		return nil, err
	}
	to, err := parseDay("to", filter.To, true)
	if err != nil {
		return nil, err
	}
	hits, err := st.SearchConversations(query, store.SearchFilter{
		Site: filter.Site, ProfileID: filter.ProfileID, From: from, To: to, Archived: filter.Archived, Limit: limit,
	})
	if errors.Is(err, store.ErrBadQuery) {
		return nil, newError(CodeInvalid, "That search could not be run. Try other words.", err)
	}
	if err != nil {
		return nil, wrapStoreErr(err)
	}
	out := make([]SearchHit, len(hits))
	for i, h := range hits {
		out[i] = SearchHit{ConvID: h.ConvID, Title: h.Title, Site: h.Site, MsgID: h.MsgID, Snippet: h.Snippet, Updated: h.Updated, Pinned: h.Pinned}
	}
	return out, nil
}

// Pin pins or unpins a conversation. It does not change the conversation's
// updated time.
func (a *AssistantService) Pin(convID string, pinned bool) error {
	_, st, done, err := a.enter()
	if err != nil {
		return err
	}
	defer done()
	if err := st.SetPinned(convID, pinned); err != nil {
		return wrapStoreErr(err)
	}
	return nil
}

// Archive moves a conversation to the archive or back. Searching leaves the
// archive out unless asked for it. It does not change the conversation's
// updated time.
func (a *AssistantService) Archive(convID string, archived bool) error {
	_, st, done, err := a.enter()
	if err != nil {
		return err
	}
	defer done()
	if err := st.SetArchived(convID, archived); err != nil {
		return wrapStoreErr(err)
	}
	return nil
}

func retentionDays(st *store.Store) (int, error) {
	v, err := st.GetSetting(settingRetention)
	if err != nil {
		return 0, err
	}
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || !validRetention(n) {
		return 0, nil // an unreadable value keeps everything
	}
	return n, nil
}

func validRetention(days int) bool { return days == 0 || days == 30 || days == 90 }

// GetRetention returns how many days a conversation is kept after its last
// message: 0 (forever), 90 or 30. Pinned conversations and conversations
// with a run in progress are always kept.
func (a *AssistantService) GetRetention() (int, error) {
	_, st, done, err := a.enter()
	if err != nil {
		return 0, err
	}
	defer done()
	n, err := retentionDays(st)
	if err != nil {
		return 0, wrapStoreErr(err)
	}
	return n, nil
}

// SetRetention sets the retention period: 0 (forever), 90 or 30 days. Older
// conversations are deleted when the app starts and then once a day, not at
// once.
func (a *AssistantService) SetRetention(days int) error {
	_, st, done, err := a.enter()
	if err != nil {
		return err
	}
	defer done()
	if !validRetention(days) {
		return invalid("days", "Choose forever, 90 days or 30 days.")
	}
	if err := st.SetSetting(settingRetention, strconv.Itoa(days)); err != nil {
		return wrapStoreErr(err)
	}
	return nil
}

// activeConvIDs lists the conversations with a run in progress.
func (r *runner) activeConvIDs() []string {
	var ids []string
	for _, a := range r.active {
		ids = append(ids, a.conv.ID)
	}
	return ids
}

// sweep deletes the conversations older than the retention period and
// returns how many. New runs wait while it runs, so a conversation cannot
// start a run between the check and the delete.
func (a *AssistantService) sweep() (int, error) {
	r, st, done, err := a.enter()
	if err != nil {
		return 0, err
	}
	defer done()
	days, err := retentionDays(st)
	if err != nil {
		return 0, err
	}
	if days == 0 {
		return 0, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return st.SweepRetention(time.Now().AddDate(0, 0, -days), r.activeConvIDs())
}

// historyState is the retention sweep's goroutine.
type historyState struct {
	every time.Duration // 0 means sweepInterval; tests shorten it
	done  chan struct{}
}

// startHistory removes the conversations that were not kept (a profile with
// history off) and starts the sweep: now, then every 24 hours until ctx ends.
func (a *AssistantService) startHistory(ctx context.Context) {
	_, st, err := a.ready()
	if err != nil {
		return
	}
	if _, err := st.DeleteEphemeral(); err != nil {
		log.Printf("assistant: removing conversations that were not kept: %v", err)
	}
	every := a.hist.every
	if every <= 0 {
		every = sweepInterval
	}
	a.hist.done = make(chan struct{})
	go func(done chan struct{}) {
		defer close(done)
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			if _, err := a.sweep(); err != nil {
				var se *Error
				if !errors.As(err, &se) || se.Code != CodeUnavailable {
					log.Printf("assistant: retention sweep: %v", err)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}(a.hist.done)
}

// stop waits for the sweep goroutine, which ends with the service's context.
func (h *historyState) stop() {
	if h.done != nil {
		<-h.done
	}
}

// endHistory removes the conversations that are not kept, as the store closes.
func endHistory(st *store.Store) {
	if _, err := st.DeleteEphemeral(); err != nil {
		log.Printf("assistant: removing conversations that were not kept: %v", err)
	}
}

// clipRunes cuts s to at most n characters.
func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// scrubKeys removes every provider key from data. No key is ever stored in
// a conversation, but a person may have pasted one into the chat, and a file
// that leaves the app must not carry it.
func (a *AssistantService) scrubKeys(st *store.Store, data []byte) []byte {
	ps, err := st.ListProviders()
	if err != nil {
		return data
	}
	for _, p := range ps {
		key, err := a.keys.Get(p.ID)
		if err != nil || len(key) < 8 {
			continue
		}
		data = bytes.ReplaceAll(data, []byte(key), []byte("[removed]"))
	}
	return data
}

var errNoDialogs = &Error{Code: CodeUnavailable, Message: "Choosing a file is not possible here."}

func (a *AssistantService) dialogs() (FileDialogs, error) {
	d, ok := a.host.(FileDialogs)
	if !ok {
		return nil, errNoDialogs
	}
	return d, nil
}

// readLimited reads a regular file of at most max bytes.
func readLimited(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, newError(CodeFailed, "That file could not be opened.", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, newError(CodeFailed, "That file could not be read.", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, invalid("file", "Choose a file, not a folder or a device.")
	}
	if fi.Size() > max {
		return nil, invalid("file", fmt.Sprintf("That file is too large (the limit is %d MB).", max>>20))
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, newError(CodeFailed, "That file could not be read.", err)
	}
	if int64(len(data)) > max {
		return nil, invalid("file", fmt.Sprintf("That file is too large (the limit is %d MB).", max>>20))
	}
	return data, nil
}
