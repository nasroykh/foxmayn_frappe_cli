package store

import (
	"errors"
	"testing"
)

func TestUsageKindsShareARunWithoutCollision(t *testing.T) {
	s, _ := openTemp(t)
	c := mustConv(t, s, "t")
	r, err := s.CreateRun(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	cost := 0.0125
	if err := s.AddUsage(Usage{RunID: r.ID, Turn: 1, Input: 100, Output: 20, Cached: 5, CacheWrite: 40, CostUSD: &cost, CostSource: "table", PriceDate: "2026-10-09"}); err != nil {
		t.Fatal(err)
	}
	// The title call is turn 0 of its own kind; a second turn row is unaffected.
	if err := s.AddUsage(Usage{RunID: r.ID, Turn: 0, Kind: UsageTitle, Input: 7, Output: 3}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddUsage(Usage{RunID: r.ID, Turn: 1, Kind: UsageTitle, Input: 8, Output: 3}); err != nil {
		t.Fatalf("the same turn number under another kind: %v", err)
	}
	// Replacing a row keeps one row per (run, turn, kind).
	if err := s.AddUsage(Usage{RunID: r.ID, Turn: 1, Input: 101, Output: 20, CostUSD: &cost, CostSource: "table"}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListUsage(r.ID)
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	var turn, title Usage
	for _, u := range rows {
		switch {
		case u.Kind == UsageTurn:
			turn = u
		case u.Kind == UsageTitle && u.Turn == 0:
			title = u
		}
	}
	if turn.Input != 101 || turn.CacheWrite != 0 || turn.CostUSD == nil || *turn.CostUSD != cost || turn.CostSource != "table" {
		t.Errorf("turn row = %+v", turn)
	}
	// A row with no cost reads back as nil, not 0 ("cost unknown").
	if title.Kind != UsageTitle || title.CostUSD != nil || title.CostSource != "" {
		t.Errorf("title row = %+v", title)
	}
	all, err := s.ListConversationUsage(c.ID)
	if err != nil || len(all) != 3 {
		t.Fatalf("conversation usage = %+v, %v", all, err)
	}
}

func TestTitleSources(t *testing.T) {
	s, _ := openTemp(t)
	c := mustConv(t, s, "")
	ts, err := s.GetTitleState(c.ID)
	if err != nil || ts.Source != TitleFallback || ts.Ephemeral {
		t.Fatalf("new conversation = %+v, %v", ts, err)
	}
	if err := s.SetFallbackTitle(c.ID, "first words"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetAutoTitle(c.ID, "Model title"); err != nil || !ok {
		t.Fatalf("SetAutoTitle = %v, %v", ok, err)
	}
	if ts, _ := s.GetTitleState(c.ID); ts.Source != TitleAuto {
		t.Fatalf("source = %q", ts.Source)
	}
	if err := s.RenameConversation(c.ID, "Mine"); err != nil {
		t.Fatal(err)
	}
	if ts, _ := s.GetTitleState(c.ID); ts.Source != TitleUser {
		t.Fatalf("source = %q", ts.Source)
	}
	if ok, err := s.SetAutoTitle(c.ID, "Later"); err != nil || ok {
		t.Fatalf("SetAutoTitle over a user title = %v, %v", ok, err)
	}
	if got, _ := s.GetConversation(c.ID); got.Title != "Mine" {
		t.Fatalf("title = %q", got.Title)
	}
	if _, err := s.GetTitleState("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id = %v", err)
	}
}
