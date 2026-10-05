package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
)

const (
	auditFileName  = "mcp-audit.jsonl"
	auditMaxBytes  = 10 << 20 // rotate to .1 past this size
	auditLineBytes = 8 << 10  // a longer line drops its args
	auditMaxNames  = 20
	auditMaxError  = 500 // runes
	auditMaxValue  = 512 // bytes of a filters value
)

// Audit statuses.
const (
	auditOK      = "ok"
	auditError   = "error"   // the site or ffc failed the call
	auditDenied  = "denied"  // the policy refused it; nothing was sent
	auditInvalid = "invalid" // bad arguments; nothing was sent
)

// auditRecord is one line of the MCP audit log: who called which tool on
// what, and how it ended. It never holds secrets or whole documents.
type auditRecord struct {
	Time       time.Time   `json:"time"`
	Site       string      `json:"site,omitempty"`
	Client     string      `json:"client,omitempty"` // as the MCP client names itself
	Tool       string      `json:"tool"`
	Doctypes   []string    `json:"doctypes,omitempty"`
	Names      []string    `json:"names,omitempty"`
	NamesTotal int         `json:"names_total,omitempty"` // set when names were cut
	Method     string      `json:"method,omitempty"`
	Args       interface{} `json:"args,omitempty"`
	Status     string      `json:"status"`
	Error      string      `json:"error,omitempty"`
	DurationMS int64       `json:"duration_ms"`
}

// auditLog appends records to mcp-audit.jsonl next to the config file.
type auditLog struct {
	path string
	mu   sync.Mutex
}

// newAuditLog returns the audit log under the config directory.
func newAuditLog() (*auditLog, error) {
	cfg, err := resolveCfgPath()
	if err != nil {
		return nil, err
	}
	return &auditLog{path: filepath.Join(filepath.Dir(cfg), auditFileName)}, nil
}

// write appends rec, built for a call with arguments args. A failure is
// reported on stderr (never stdout, the stdio JSON-RPC channel) and does not
// fail the call: the call has already happened.
func (l *auditLog) write(rec auditRecord, args map[string]interface{}) {
	if l == nil {
		return
	}
	rec.Args = auditArgs(args)
	rec.Error = hideSecretValues(rec.Error, args)
	if r := []rune(rec.Error); len(r) > auditMaxError {
		rec.Error = string(r[:auditMaxError]) + "…"
	}
	if len(rec.Names) > auditMaxNames {
		rec.NamesTotal, rec.Names = len(rec.Names), rec.Names[:auditMaxNames]
	}
	line, err := json.Marshal(rec)
	if err == nil && len(line) > auditLineBytes {
		rec.Args = map[string]interface{}{"omitted_bytes": len(line)}
		line, err = json.Marshal(rec)
	}
	if err == nil {
		err = l.append(append(line, '\n'))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: writing the MCP audit log: %v\n", err)
	}
}

func (l *auditLog) append(line []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if fi, err := os.Stat(l.path); err == nil && fi.Size()+int64(len(line)) > auditMaxBytes {
		if err := l.rotate(); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	// One O_APPEND write per line keeps lines whole when several ffc MCP
	// servers share the file.
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(line)
	if err := f.Close(); werr == nil {
		werr = err
	}
	return werr
}

// rotate moves a full log to .1 under the config lock, so two servers do
// not both rotate (the second would move the fresh file over the history).
func (l *auditLog) rotate() error {
	unlock, err := config.Lock(l.path)
	if err != nil {
		return err
	}
	defer unlock()
	if fi, err := os.Stat(l.path); err != nil || fi.Size() <= auditMaxBytes/2 {
		return nil // another server rotated it while we waited
	}
	return os.Rename(l.path, l.path+".1")
}

// auditArgs is the logged form of a tool's arguments: secrets redacted,
// document data and method arguments reduced to their keys and size, long
// filters cut.
func auditArgs(args map[string]interface{}) interface{} {
	if len(args) == 0 {
		return nil
	}
	red, _ := client.RedactArgs(args).(map[string]interface{})
	for _, k := range []string{"data", "args"} {
		if v, ok := red[k]; ok {
			red[k] = shape(v)
		}
	}
	if v, ok := red["filters"]; ok {
		if b, _ := json.Marshal(v); len(b) > auditMaxValue {
			red["filters"] = string(b[:auditMaxValue]) + "…"
		}
	}
	return red
}

// shape describes a document or argument object without its values.
func shape(v interface{}) interface{} {
	b, _ := json.Marshal(v)
	switch val := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return map[string]interface{}{"keys": keys, "bytes": len(b)}
	case []interface{}:
		return map[string]interface{}{"items": len(val), "bytes": len(b)}
	}
	return map[string]interface{}{"bytes": len(b)}
}

// hideSecretValues removes from msg the values of secret arguments, which
// a server error may echo back ("Value 'hunter2' is not valid").
func hideSecretValues(msg string, args map[string]interface{}) string {
	if msg == "" {
		return msg
	}
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch val := v.(type) {
		case map[string]interface{}:
			hide := func(x interface{}) {
				if s, ok := x.(string); ok && len(s) >= 4 {
					msg = strings.ReplaceAll(msg, s, "***")
				}
			}
			if f, ok := val["fieldname"].(string); ok && client.SecretKey(f) {
				hide(val["value"]) // frappe.client.set_value
			}
			for k, x := range val {
				if client.SecretKey(k) {
					hide(x)
				}
				walk(x)
			}
		case []interface{}:
			for _, x := range val {
				walk(x)
			}
		case string:
			if t := strings.TrimSpace(val); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
				var inner interface{}
				if json.Unmarshal([]byte(t), &inner) == nil {
					walk(inner)
				}
			}
		}
	}
	walk(map[string]interface{}(args))
	return text.Sanitize(msg)
}
