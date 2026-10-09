package services

import (
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm/prices"
	"github.com/nasroykh/foxmayn_frappe_cli/desktop/store"
)

// priced is the cost of one model call: USD is nil when it is unknown or the
// model is local; Source says which ("provider", "table", "local" or "").
type priced struct {
	USD    *float64
	Source string
	Date   string
}

// costOf prices a call of conv's provider and model. The provider's own price
// (OpenRouter's usage.cost) wins; a model on this computer has tokens and no
// cost; else the embedded table decides; a model it does not list has an
// unknown cost, never a guessed one. A model that a local server passes on to
// a hosted service (isCloudModel) is not local: its cost is unknown.
func costOf(st *store.Store, conv store.Conversation, model string, u llm.Usage) priced {
	var p store.Provider
	if rows, err := st.ListProviders(); err == nil {
		for _, row := range rows {
			if row.ID == conv.ProviderID {
				p = row
			}
		}
	}
	if u.Cost == nil && (prices.Local(p.Kind) || isLocalProvider(p)) {
		if isCloudModel(model) {
			return priced{}
		}
		return priced{Source: prices.SourceLocal}
	}
	r := prices.Cost(p.Kind, model, u)
	if !r.OK {
		return priced{Source: r.Source}
	}
	usd := r.USD
	return priced{USD: &usd, Source: r.Source, Date: r.Date}
}

// usageRow builds the stored row of one call.
func usageRow(runID string, turn int, kind string, u llm.Usage, c priced) store.Usage {
	return store.Usage{
		RunID: runID, Turn: turn, Kind: kind,
		Input: u.In, Output: u.Out, Cached: u.Cached, CacheWrite: u.CacheWrite,
		CostUSD: c.USD, CostSource: c.Source, PriceDate: c.Date,
	}
}

// UsageTotals adds up the token counts and costs of some calls. When Unknown
// is set the cost of some call is not known: CostUSD is then "at least". A
// call on a local model counts tokens only and never makes a total unknown.
type UsageTotals struct {
	Input      int `json:"input"`
	Output     int `json:"output"`
	Cached     int `json:"cached"`
	CacheWrite int `json:"cacheWrite"`
	// CostUSD sums the calls that have a cost; HasCost says any has.
	CostUSD float64 `json:"costUSD"`
	HasCost bool    `json:"hasCost"`
	Unknown bool    `json:"unknown"`
}

func (t *UsageTotals) add(u store.Usage) {
	t.Input += u.Input
	t.Output += u.Output
	t.Cached += u.Cached
	t.CacheWrite += u.CacheWrite
	switch {
	case u.CostUSD != nil:
		t.CostUSD += *u.CostUSD
		t.HasCost = true
	case u.CostSource != prices.SourceLocal:
		t.Unknown = true
	}
}

// RunUsage is what one run (a question and its answer) used. MsgID is the
// last assistant message of the run, where the chat shows the line; "" when
// the run left none.
type RunUsage struct {
	RunID string      `json:"runID"`
	MsgID string      `json:"msgID"`
	Usage UsageTotals `json:"usage"`
}

// conversationUsage returns the per-run usage (model turns only) and the
// total of the whole conversation, title calls included. A run's message is
// the last assistant one created between its start and the next run's.
func conversationUsage(st *store.Store, runs []store.Run, msgs []ChatMessage, convID string) ([]RunUsage, UsageTotals, error) {
	rows, err := st.ListConversationUsage(convID)
	if err != nil {
		return nil, UsageTotals{}, err
	}
	var total UsageTotals
	byRun := map[string]*UsageTotals{}
	for _, u := range rows {
		total.add(u)
		if u.Kind != store.UsageTurn {
			continue
		}
		t := byRun[u.RunID]
		if t == nil {
			t = &UsageTotals{}
			byRun[u.RunID] = t
		}
		t.add(u)
	}
	out := []RunUsage{}
	for i, r := range runs {
		t := byRun[r.ID]
		if t == nil {
			continue
		}
		ru := RunUsage{RunID: r.ID, Usage: *t}
		for _, m := range msgs {
			if m.Role == string(llm.RoleAssistant) && !m.Created.Before(r.Started) && (i+1 == len(runs) || m.Created.Before(runs[i+1].Started)) {
				ru.MsgID = m.ID
			}
		}
		out = append(out, ru)
	}
	return out, total, nil
}
