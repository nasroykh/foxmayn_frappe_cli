// Package prices turns a turn's token usage into dollars. The table is
// embedded, dated and read only: a model that is not in it has an unknown
// cost, never a guessed one.
package prices

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
)

//go:embed prices.json
var raw []byte

// Where a cost came from. SourceLocal means a model on this computer: it has
// tokens and no cost, which is not the same as an unknown cost ("").
const (
	SourceProvider = "provider"
	SourceTable    = "table"
	SourceLocal    = "local"
)

// Price is one set of $/Mtok prices.
type Price struct {
	In         float64  `json:"in"`
	Out        float64  `json:"out"`
	CacheRead  float64  `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write,omitempty"`
}

// Long is the price of a request whose whole prompt is over Over tokens.
type Long struct {
	Over int `json:"over"`
	Price
}

// Entry is the price of one model of one provider kind.
type Entry struct {
	Kind       string `json:"kind"`
	Model      string `json:"model"`
	Source     string `json:"source"`
	ValidUntil string `json:"valid_until,omitempty"`
	Price
	Long *Long `json:"long,omitempty"`
}

// Table is the embedded price table.
type Table struct {
	PriceDate string   `json:"price_date"`
	Unit      string   `json:"unit"`
	Notes     []string `json:"notes"`
	Models    []Entry  `json:"models"`
}

var (
	once  sync.Once
	table Table
	index map[string]Entry
	err   error
)

func load() {
	once.Do(func() {
		if err = json.Unmarshal(raw, &table); err != nil {
			return
		}
		index = make(map[string]Entry, len(table.Models))
		for _, e := range table.Models {
			index[key(e.Kind, e.Model)] = e
		}
	})
}

func key(kind, model string) string { return kind + "\x00" + model }

// Load returns the embedded table. A table that does not parse is a build
// defect (a test covers it); it is returned as an error here and Cost then
// finds no price.
func Load() (Table, error) {
	load()
	return table, err
}

// Date is the day the table was read from the providers' pages.
func Date() string {
	load()
	return table.PriceDate
}

// now is a seam for the valid_until tests.
var now = time.Now

// Result is the cost of one turn. OK is false when no cost is known; then
// USD is 0 and Source is "". Date is the table's price_date for a table cost.
type Result struct {
	USD    float64
	Source string
	Date   string
	OK     bool
}

// Local reports kinds that run on this computer (a custom server is checked by
// the caller, which knows its address).
func Local(kind string) bool { return kind == "ollama" || kind == "lmstudio" }

// Cost prices u for a model of a provider kind. A cost the provider reported
// (OpenRouter's usage.cost) wins; then the table; else the cost is unknown. A
// local kind has none: Source is SourceLocal and OK is false.
//
// u.In holds the cache writes (CacheWrite is a subset): they are priced at the
// write price, the rest of In at the input price, u.Cached at the cache-read
// price. A model with a "long" price uses it when the whole prompt
// (In + Cached) is over its threshold.
func Cost(kind, model string, u llm.Usage) Result {
	if u.Cost != nil && *u.Cost >= 0 {
		return Result{USD: *u.Cost, Source: SourceProvider, OK: true}
	}
	if Local(kind) {
		return Result{Source: SourceLocal}
	}
	load()
	if err != nil {
		return Result{}
	}
	e, ok := index[key(kind, strings.TrimPrefix(model, "models/"))]
	if !ok || expired(e) {
		return Result{}
	}
	p := e.Price
	if e.Long != nil && u.In+u.Cached > e.Long.Over {
		p = e.Long.Price
	}
	in, out, cached, written := max(u.In, 0), max(u.Out, 0), max(u.Cached, 0), min(max(u.CacheWrite, 0), max(u.In, 0))
	writePrice := p.In
	if p.CacheWrite != nil {
		writePrice = *p.CacheWrite
	}
	usd := (float64(in-written)*p.In + float64(written)*writePrice + float64(cached)*p.CacheRead + float64(out)*p.Out) / 1e6
	return Result{USD: usd, Source: SourceTable, Date: table.PriceDate, OK: true}
}

// expired reports whether an entry's valid_until day has passed.
func expired(e Entry) bool {
	if e.ValidUntil == "" {
		return false
	}
	last, perr := time.Parse("2006-01-02", e.ValidUntil)
	if perr != nil {
		return true // an unreadable date is not trusted
	}
	return now().UTC().After(last.AddDate(0, 0, 1).Add(-time.Nanosecond))
}
