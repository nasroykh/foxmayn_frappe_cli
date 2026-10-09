package prices

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
)

func near(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("cost = %v, want %v", got, want)
	}
}

func TestTableParsesAndEveryEntryIsSourced(t *testing.T) {
	tb, err := Load()
	if err != nil {
		t.Fatalf("prices.json does not parse: %v", err)
	}
	if _, err := time.Parse("2006-01-02", tb.PriceDate); err != nil {
		t.Fatalf("price_date %q: %v", tb.PriceDate, err)
	}
	if len(tb.Models) == 0 {
		t.Fatal("no models")
	}
	seen := map[string]bool{}
	pos := func(e Entry, what string, v float64) {
		if !(v > 0) {
			t.Errorf("%s/%s: %s = %v, want > 0", e.Kind, e.Model, what, v)
		}
	}
	for _, e := range tb.Models {
		if k := key(e.Kind, e.Model); seen[k] {
			t.Errorf("duplicate entry %s/%s", e.Kind, e.Model)
		} else {
			seen[k] = true
		}
		switch e.Kind {
		case "anthropic", "openai", "gemini":
		default:
			t.Errorf("%s/%s: kind %q has no official price page", e.Kind, e.Model, e.Kind)
		}
		if !strings.HasPrefix(e.Source, "https://") {
			t.Errorf("%s/%s: source %q is not an https URL", e.Kind, e.Model, e.Source)
		}
		pos(e, "in", e.In)
		pos(e, "out", e.Out)
		pos(e, "cache_read", e.CacheRead)
		if e.CacheWrite != nil {
			pos(e, "cache_write", *e.CacheWrite)
		}
		if e.ValidUntil != "" {
			if _, err := time.Parse("2006-01-02", e.ValidUntil); err != nil {
				t.Errorf("%s/%s: valid_until %q: %v", e.Kind, e.Model, e.ValidUntil, err)
			}
		}
		if l := e.Long; l != nil {
			if l.Over <= 0 {
				t.Errorf("%s/%s: long.over = %d", e.Kind, e.Model, l.Over)
			}
			pos(e, "long.in", l.In)
			pos(e, "long.out", l.Out)
			pos(e, "long.cache_read", l.CacheRead)
			if l.In < e.In || l.Out < e.Out {
				t.Errorf("%s/%s: a long-context price below the short one", e.Kind, e.Model)
			}
		}
	}
}

func TestAnthropicCacheWriteReadAndInput(t *testing.T) {
	// claude-sonnet-5-5: in 2, out 10, cache read 0.10, 5m write 2.50.
	// In 1000 holds 400 written; 600 fresh; 5000 read; 200 out.
	r := Cost("anthropic", "claude-sonnet-5-5", llm.Usage{In: 1000, CacheWrite: 400, Cached: 5000, Out: 200})
	if !r.OK || r.Source != SourceTable || r.Date != Date() {
		t.Fatalf("result = %+v", r)
	}
	near(t, r.USD, (600*2+400*2.5+5000*0.10+200*10)/1e6)
}

func TestOpenAICachedAndGeminiCached(t *testing.T) {
	// gpt-5.4: in 2.50, cached 0.25, out 15. No write price: writes count as input.
	r := Cost("openai", "gpt-5.4", llm.Usage{In: 2000, Cached: 8000, Out: 500})
	near(t, r.USD, (2000*2.5+8000*0.25+500*15)/1e6)
	// gemini-2.5-flash: in 0.30, cached 0.03, out 2.50 (reasoning is in Out).
	r = Cost("gemini", "gemini-2.5-flash", llm.Usage{In: 1000, Cached: 3000, Out: 700})
	near(t, r.USD, (1000*0.3+3000*0.03+700*2.5)/1e6)
	// A "models/" prefix, as Gemini lists its ids, finds the same entry.
	r2 := Cost("gemini", "models/gemini-2.5-flash", llm.Usage{In: 1000, Cached: 3000, Out: 700})
	near(t, r2.USD, r.USD)
}

// Where the page lists a cache-write price (gpt-6 and gpt-5.6 families) the
// written tokens bill at it, in the short and the long tier; where it lists
// none (gpt-5.5, gpt-5.4, Gemini) they bill as input.
func TestOpenAICacheWritePriceAndItsAbsence(t *testing.T) {
	// gpt-6-sol: in 2, write 2.5, cached 0.2, out 10. 600 fresh + 400 written.
	r := Cost("openai", "gpt-6-sol", llm.Usage{In: 1000, CacheWrite: 400, Cached: 5000, Out: 200})
	near(t, r.USD, (600*2.0+400*2.5+5000*0.2+200*10.0)/1e6)
	// Long tier (prompt over 272000): in 4, write 5, cached 0.4, out 15.
	r = Cost("openai", "gpt-6-sol", llm.Usage{In: 300000, CacheWrite: 1000, Cached: 0, Out: 100})
	near(t, r.USD, (299000*4.0+1000*5.0+100*15.0)/1e6)
	// gpt-6-luna has a fractional write price.
	r = Cost("openai", "gpt-6-luna", llm.Usage{In: 1000, CacheWrite: 1000, Out: 0})
	near(t, r.USD, 1000*0.125/1e6)
	// gpt-5.5 lists "-": written tokens bill as input, in both tiers.
	r = Cost("openai", "gpt-5.5", llm.Usage{In: 1000, CacheWrite: 400, Out: 10})
	near(t, r.USD, (1000*5.0+10*30.0)/1e6)
	r = Cost("openai", "gpt-5.5", llm.Usage{In: 300000, CacheWrite: 400, Out: 10})
	near(t, r.USD, (300000*10.0+10*45.0)/1e6)
	// Gemini has none either.
	r = Cost("gemini", "gemini-2.5-flash", llm.Usage{In: 1000, CacheWrite: 400, Out: 10})
	near(t, r.USD, (1000*0.3+10*2.5)/1e6)
}

func TestEveryGPT6AndGPT56EntryHasShortAndLongCacheWrite(t *testing.T) {
	tb, _ := Load()
	n := 0
	for _, e := range tb.Models {
		if e.Kind == "openai" && (strings.HasPrefix(e.Model, "gpt-6") || strings.HasPrefix(e.Model, "gpt-5.6")) {
			n++
			if e.CacheWrite == nil || e.Long == nil || e.Long.CacheWrite == nil {
				t.Errorf("%s: cache_write missing (short or long)", e.Model)
			}
		}
	}
	if n != 7 {
		t.Errorf("found %d gpt-6/gpt-5.6 entries, want 7", n)
	}
}

func TestLongContextTier(t *testing.T) {
	// Haiku 5.5: over 100000 prompt tokens (cache reads count) the long price applies.
	short := Cost("anthropic", "claude-haiku-5-5", llm.Usage{In: 50000, Cached: 50000, Out: 1000})
	near(t, short.USD, (50000*0.10+50000*0.01+1000*0.5)/1e6)
	long := Cost("anthropic", "claude-haiku-5-5", llm.Usage{In: 50000, Cached: 50001, Out: 1000})
	near(t, long.USD, (50000*0.5+50001*0.05+1000*2.5)/1e6)
	// Gemini 2.5 Pro over 200000; OpenAI gpt-5.5 over 272000.
	g := Cost("gemini", "gemini-2.5-pro", llm.Usage{In: 300000, Out: 10})
	near(t, g.USD, (300000*2.5+10*15)/1e6)
	o := Cost("openai", "gpt-5.5", llm.Usage{In: 272000, Out: 10})
	near(t, o.USD, (272000*5.0+10*30)/1e6)
	o = Cost("openai", "gpt-5.5", llm.Usage{In: 272001, Out: 10})
	near(t, o.USD, (272001*10.0+10*45)/1e6)
}

func TestProviderCostWins(t *testing.T) {
	c := 0.0123
	r := Cost("openrouter", "anthropic/claude-sonnet-5-5", llm.Usage{In: 10, Out: 10, Cost: &c})
	if !r.OK || r.Source != SourceProvider || r.USD != c {
		t.Fatalf("result = %+v", r)
	}
	// It wins over the table too.
	r = Cost("anthropic", "claude-sonnet-5-5", llm.Usage{In: 1000000, Cost: &c})
	if r.Source != SourceProvider || r.USD != c {
		t.Fatalf("result = %+v", r)
	}
}

func TestUnknownModelIsNotGuessed(t *testing.T) {
	for _, tc := range []struct{ kind, model string }{
		{"anthropic", "claude-opus-9"},
		{"anthropic", "claude-sonnet-5-5-20260101"}, // a snapshot id the page does not list
		{"openai", "gpt-5.4-pro"},
		{"openrouter", "anthropic/claude-sonnet-5-5"}, // no table for OpenRouter
		{"custom", "gpt-5.4"},
		{"", ""},
	} {
		r := Cost(tc.kind, tc.model, llm.Usage{In: 100, Out: 100})
		if r.OK || r.Source != "" || r.USD != 0 {
			t.Errorf("%s/%s: result = %+v, want unknown", tc.kind, tc.model, r)
		}
	}
}

// Cost is the table only: the caller decides what is local.
func TestKindsWithoutATableEntryAreUnknown(t *testing.T) {
	for _, kind := range []string{"ollama", "lmstudio"} {
		r := Cost(kind, "gpt-5.4", llm.Usage{In: 100, Out: 100})
		if r.OK || r.Source != "" {
			t.Errorf("%s: result = %+v", kind, r)
		}
	}
}

func TestValidUntil(t *testing.T) {
	defer func() { now = time.Now }()
	u := llm.Usage{In: 1000, Out: 100}
	now = func() time.Time { return time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC) }
	if r := Cost("gemini", "gemini-3.8-flash", u); !r.OK {
		t.Fatal("unknown on the last valid day")
	}
	now = func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) }
	if r := Cost("gemini", "gemini-3.8-flash", u); r.OK {
		t.Fatalf("priced after valid_until: %+v", r)
	}
	// A model with no end date is still priced.
	if r := Cost("gemini", "gemini-3.5-flash", u); !r.OK {
		t.Fatal("unknown")
	}
}
