package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunBulkOrderAndCounts(t *testing.T) {
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			rep := runBulk(context.Background(), 10, workers, false, "created", func(_ context.Context, i int) (string, error) {
				if i%3 == 0 {
					return "", errors.New("boom")
				}
				return fmt.Sprintf("N-%d", i), nil
			})
			if len(rep.Results) != 10 {
				t.Fatalf("results = %d", len(rep.Results))
			}
			for i, r := range rep.Results {
				if r.Index != i+1 {
					t.Errorf("results[%d].Index = %d", i, r.Index)
				}
				wantStatus := "created"
				if i%3 == 0 {
					wantStatus = "error"
				}
				if r.Status != wantStatus {
					t.Errorf("results[%d].Status = %q, want %q", i, r.Status, wantStatus)
				}
			}
			if rep.OK != 6 || rep.Failed != 4 || rep.Skipped != 0 {
				t.Errorf("counts ok=%d failed=%d skipped=%d", rep.OK, rep.Failed, rep.Skipped)
			}
			if rep.err() == nil {
				t.Error("err() = nil with failures")
			}
		})
	}
}

func TestRunBulkAllSucceed(t *testing.T) {
	rep := runBulk(context.Background(), 5, 3, false, "deleted", func(context.Context, int) (string, error) { return "x", nil })
	if rep.OK != 5 || rep.err() != nil || rep.Canceled {
		t.Errorf("rep = %+v, err = %v", rep, rep.err())
	}
	if rep.JSON()["deleted"] != 5 {
		t.Errorf("JSON = %v", rep.JSON())
	}
}

func TestRunBulkFailFast(t *testing.T) {
	var started atomic.Int32
	rep := runBulk(context.Background(), 6, 1, true, "created", func(_ context.Context, i int) (string, error) {
		started.Add(1)
		if i == 1 {
			return "", errors.New("boom")
		}
		return "ok", nil
	})
	if started.Load() != 2 {
		t.Errorf("started = %d, want 2", started.Load())
	}
	got := []string{}
	for _, r := range rep.Results {
		got = append(got, r.Status)
	}
	want := []string{"created", "error", "skipped", "skipped", "skipped", "skipped"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("statuses = %v, want %v", got, want)
	}
	if rep.Skipped != 4 || rep.Failed != 1 || rep.OK != 1 {
		t.Errorf("counts = %+v", rep)
	}
}

func TestRunBulkCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rep := runBulk(ctx, 5, 1, false, "created", func(ctx context.Context, i int) (string, error) {
		if i == 1 {
			cancel()
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
			return "", ctx.Err()
		}
		return "ok", nil
	})
	got := []string{}
	for _, r := range rep.Results {
		got = append(got, r.Status)
	}
	want := []string{"created", "interrupted", "skipped", "skipped", "skipped"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("statuses = %v, want %v", got, want)
	}
	if !rep.Canceled || rep.err() == nil {
		t.Errorf("Canceled=%v err=%v", rep.Canceled, rep.err())
	}
}

func TestParseObjects(t *testing.T) {
	bad := []string{`[]`, `[null]`, `[1]`, `{"a":1}`, `"x"`, `[{"a":1}, null]`, `nope`}
	for _, in := range bad {
		if _, err := parseObjects([]byte(in)); err == nil {
			t.Errorf("parseObjects(%s): want error", in)
		}
	}
	items, err := parseObjects([]byte(`[{"a":1},{"b":"x"}]`))
	if err != nil || len(items) != 2 {
		t.Errorf("valid: %v %v", items, err)
	}
}

func TestParseNames(t *testing.T) {
	got, err := parseNames([]byte(`["A-1", 42, 7.5]`))
	if err != nil || !reflect.DeepEqual(got, []string{"A-1", "42", "7.5"}) {
		t.Errorf("got %v, %v", got, err)
	}
	for _, in := range []string{`[""]`, `[{"name":"a"}]`, `[]`, `[null]`, `"a"`, `["a", ""]`} {
		if _, err := parseNames([]byte(in)); err == nil {
			t.Errorf("parseNames(%s): want error", in)
		}
	}
}

func TestSplitUpdates(t *testing.T) {
	items := []map[string]interface{}{
		{"name": "A", "status": "Closed"},
		{"name": float64(5), "x": 1},
	}
	names, payloads, err := splitUpdates(items)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, []string{"A", "5"}) {
		t.Errorf("names = %v", names)
	}
	for i, p := range payloads {
		if _, ok := p["name"]; ok {
			t.Errorf("payload %d still has name", i)
		}
	}
	if payloads[0]["status"] != "Closed" {
		t.Errorf("payload = %v", payloads[0])
	}
	if _, ok := items[0]["name"]; !ok {
		t.Error("input was mutated")
	}
	for _, bad := range [][]map[string]interface{}{
		{{"status": "x"}}, {{"name": ""}}, {{"name": nil}}, {{"name": map[string]interface{}{}}},
	} {
		if _, _, err := splitUpdates(bad); err == nil {
			t.Errorf("splitUpdates(%v): want error", bad)
		}
	}
}

func TestReadInputStdin(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`[{"a":1}]`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = old; f.Close() }()

	b, err := readInput("", "-")
	if err != nil || string(b) != `[{"a":1}]` {
		t.Errorf("readInput = %q, %v", b, err)
	}
}

func TestReadInputOther(t *testing.T) {
	if b, err := readInput(`{"x":1}`, ""); err != nil || string(b) != `{"x":1}` {
		t.Errorf("inline: %q %v", b, err)
	}
	if _, err := readInput("", ""); err == nil {
		t.Error("neither: want error")
	}
	p := t.TempDir() + "/in.json"
	if err := os.WriteFile(p, []byte("[1]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := readInput("", p); err != nil || string(b) != "[1]" {
		t.Errorf("file: %q %v", b, err)
	}
	if _, err := readInput("", p+".missing"); err == nil {
		t.Error("missing file: want error")
	}
}
