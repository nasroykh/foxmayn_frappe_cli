package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"github.com/spf13/cobra"
)

// maxBulkWorkers caps --concurrency so a bulk run cannot flood the site.
const maxBulkWorkers = 10

// bulkResult is the per-item outcome of a bulk operation. The CLI (--json) and
// the MCP tools emit the same shape.
type bulkResult struct {
	Index  int    `json:"index"` // 1-based position in the input
	Name   string `json:"name,omitempty"`
	Status string `json:"status"` // <done verb> | error | interrupted | skipped
	Error  string `json:"error,omitempty"`
}

// bulkReport summarises a bulk run.
type bulkReport struct {
	Done     string // "created", "updated" or "deleted"
	Total    int
	Results  []bulkResult
	OK       int
	Failed   int
	Skipped  int
	Canceled bool
}

// runBulk applies op to items 0..n-1 with at most workers requests in flight.
// Once ctx is cancelled (Ctrl+C, MCP client gone) or, with failFast, an item
// has failed, no new item starts and the rest are reported "skipped". An item
// whose request was cut off by the cancellation is "interrupted": the server
// may or may not have applied it, so it must not be counted as either.
func runBulk(ctx context.Context, n, workers int, failFast bool, done string, op func(ctx context.Context, i int) (string, error)) bulkReport {
	if workers < 1 {
		workers = 1
	}
	results := make([]bulkResult, n)
	var (
		mu      sync.Mutex
		stopped bool
		wg      sync.WaitGroup
		next    = make(chan int)
	)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				// The dispatcher may already have handed this item over when a
				// failure or cancellation landed; re-check before starting it.
				mu.Lock()
				halt := stopped
				mu.Unlock()
				if halt || ctx.Err() != nil {
					mu.Lock()
					results[i] = bulkResult{Index: i + 1, Status: "skipped"}
					mu.Unlock()
					continue
				}
				r := bulkResult{Index: i + 1}
				name, err := op(ctx, i)
				r.Name = name
				switch {
				case err == nil:
					r.Status = done
				case ctx.Err() != nil && errors.Is(err, ctx.Err()):
					r.Status = "interrupted"
					r.Error = "cancelled while in flight; the server may or may not have applied it"
				default:
					r.Status = "error"
					r.Error = err.Error()
				}
				mu.Lock()
				results[i] = r
				if r.Status == "error" && failFast {
					stopped = true
				}
				mu.Unlock()
			}
		}()
	}
	for i := 0; i < n; i++ {
		mu.Lock()
		halt := stopped
		mu.Unlock()
		if halt || ctx.Err() != nil {
			for j := i; j < n; j++ {
				results[j] = bulkResult{Index: j + 1, Status: "skipped"}
			}
			break
		}
		next <- i
	}
	close(next)
	wg.Wait()

	rep := bulkReport{Done: done, Total: n, Results: results, Canceled: ctx.Err() != nil}
	for _, r := range results {
		switch r.Status {
		case done:
			rep.OK++
		case "skipped":
			rep.Skipped++
		default:
			rep.Failed++
		}
	}
	return rep
}

// JSON returns the machine-readable form shared by `--json` and MCP.
func (r bulkReport) JSON() map[string]interface{} {
	return map[string]interface{}{
		r.Done:    r.OK,
		"failed":  r.Failed,
		"skipped": r.Skipped,
		"results": r.Results,
	}
}

// err is the command's exit error: non-nil unless every item succeeded.
func (r bulkReport) err() error {
	if r.Failed == 0 && r.Skipped == 0 {
		return nil
	}
	return &partialError{fmt.Sprintf("%d of %d items did not succeed (%d failed or interrupted, %d skipped)", r.Failed+r.Skipped, r.Total, r.Failed, r.Skipped)}
}

// printBulkReport renders a report for the CLI and returns the exit error.
func printBulkReport(rep bulkReport, doctype string) error {
	if machineOutput() {
		if err := printResult(rep.JSON()); err != nil {
			return err
		}
		return rep.err()
	}
	rows := make([]map[string]interface{}, len(rep.Results))
	for i, r := range rep.Results {
		rows[i] = map[string]interface{}{"index": r.Index, "name": r.Name, "status": r.Status, "error": r.Error}
	}
	output.PrintTable(rows, []string{"index", "name", "status", "error"})
	if rep.err() == nil {
		output.PrintSuccess(fmt.Sprintf("All %d %s documents %s.", rep.OK, doctype, rep.Done))
	} else {
		output.PrintError(fmt.Sprintf("%d %s, %d failed or interrupted, %d skipped.", rep.OK, rep.Done, rep.Failed, rep.Skipped))
	}
	return rep.err()
}

// ─── Input parsing (shared by the CLI and MCP) ───────────────────────────────

// parseObjects decodes a JSON array whose every element must be an object, so
// a stray null or scalar is reported up front instead of failing (or sending
// "null") halfway through a run.
func parseObjects(raw []byte) ([]map[string]interface{}, error) {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, usageErrorf("expected a JSON array of objects: %w", err)
	}
	if len(elems) == 0 {
		return nil, errors.New("the array is empty")
	}
	items := make([]map[string]interface{}, len(elems))
	for i, e := range elems {
		if err := json.Unmarshal(e, &items[i]); err != nil || items[i] == nil {
			return nil, usageErrorf("item %d is not a JSON object", i+1)
		}
	}
	return items, nil
}

// splitUpdates validates that every update item names its document and
// returns the names plus the payloads with "name" removed (Frappe takes the
// name from the URL).
func splitUpdates(items []map[string]interface{}) ([]string, []map[string]interface{}, error) {
	names := make([]string, len(items))
	payloads := make([]map[string]interface{}, len(items))
	for i, item := range items {
		n, ok := docName(item["name"])
		if !ok {
			return nil, nil, fmt.Errorf("item %d is missing a \"name\" field", i+1)
		}
		names[i] = n
		payloads[i] = withoutName(item)
	}
	return names, payloads, nil
}

// withoutName returns a copy of data without the "name" key.
func withoutName(data map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(data))
	for k, v := range data {
		if k != "name" {
			out[k] = v
		}
	}
	return out
}

// parseNames decodes a JSON array of document names. Numbers are accepted
// (integer-named DocTypes); empty or non-scalar entries are rejected, since
// an empty name would turn DELETE /api/resource/<doctype>/<name> into a
// request on the collection URL.
func parseNames(raw []byte) ([]string, error) {
	var elems []interface{}
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, usageErrorf("expected a JSON array of names: %w", err)
	}
	if len(elems) == 0 {
		return nil, usageErrorf("the names array is empty")
	}
	names := make([]string, len(elems))
	for i, e := range elems {
		n, ok := docName(e)
		if !ok {
			return nil, usageErrorf("name %d is empty or not a string/number", i+1)
		}
		names[i] = n
	}
	return names, nil
}

// bulkFlags are the execution flags shared by the bulk-* commands.
type bulkFlags struct {
	concurrency int
	failFast    bool
}

func (f *bulkFlags) register(cmd *cobra.Command) {
	cmd.Flags().IntVar(&f.concurrency, "concurrency", 1, fmt.Sprintf("Number of requests in flight (1-%d)", maxBulkWorkers))
	cmd.Flags().BoolVar(&f.failFast, "fail-fast", false, "Stop starting new items after the first failure")
}

// run builds the site client once and executes op over n items under one
// spinner.
func (f *bulkFlags) run(cmd *cobra.Command, title string, n int, done string, op func(ctx context.Context, c *client.FrappeClient, i int) (string, error)) (bulkReport, error) {
	if err := f.check(); err != nil {
		return bulkReport{}, err
	}
	c, err := newClient(cmd.Context())
	if err != nil {
		return bulkReport{}, err
	}
	defer c.CloseQuietly()
	return f.runOn(cmd, c, title, n, done, op)
}

// check validates the flags before any request.
func (f *bulkFlags) check() error {
	if f.concurrency < 1 || f.concurrency > maxBulkWorkers {
		return usageErrorf("--concurrency must be between 1 and %d", maxBulkWorkers)
	}
	return nil
}

// runOn is run with a client the caller built (and closes), for commands
// that read the site before the bulk run, such as --filters.
func (f *bulkFlags) runOn(cmd *cobra.Command, c *client.FrappeClient, title string, n int, done string, op func(ctx context.Context, c *client.FrappeClient, i int) (string, error)) (bulkReport, error) {
	if client.IsDryRun(cmd.Context()) {
		return bulkReport{}, planAll(cmd.Context(), c, n, op)
	}
	var rep bulkReport
	_ = runSpinner(title, func() {
		rep = runBulk(cmd.Context(), n, f.concurrency, f.failFast, done, func(ctx context.Context, i int) (string, error) {
			return op(ctx, c, i)
		})
	})
	return rep, nil
}

// planAll runs op on every item under a dry run, one at a time, and returns
// all the requests it would have sent as one *client.DryRunError.
func planAll(ctx context.Context, c *client.FrappeClient, n int, op func(ctx context.Context, c *client.FrappeClient, i int) (string, error)) error {
	all := &client.DryRunError{}
	for i := 0; i < n; i++ {
		_, err := op(ctx, c, i)
		var plan *client.DryRunError
		switch {
		case err == nil:
			return fmt.Errorf("item %d: the dry run held nothing back", i+1)
		case !errors.As(err, &plan):
			return fmt.Errorf("item %d: %w", i+1, err)
		}
		all.Requests = append(all.Requests, plan.Requests...)
	}
	return all
}

// bulkFilters reads --filters for a bulk command. Filters that select
// nothing ({} or []) match every document, so they are refused: a bulk write
// to a whole DocType must be asked for with a filter that says so.
func bulkFilters(raw string) (string, error) {
	filters, err := filtersFlag(raw)
	if err != nil {
		return "", err
	}
	var v interface{}
	_ = json.Unmarshal([]byte(filters), &v)
	switch f := v.(type) {
	case map[string]interface{}:
		if len(f) > 0 {
			return filters, nil
		}
	case []interface{}:
		if len(f) > 0 {
			return filters, nil
		}
	}
	return "", usageErrorf(`--filters: empty filters match every document; to act on all of them, say so with '[["name","is","set"]]'`)
}

// filterNames returns the names of every doctype document matching filters
// (one unpaged list call for the names only), for --filters on bulk
// commands. The run then works on this list, so documents that start or
// stop matching during the run are not affected.
func filterNames(cmd *cobra.Command, c *client.FrappeClient, doctype, filters string) ([]string, error) {
	var rows []map[string]interface{}
	var listErr error
	spinErr := runSpinner(fmt.Sprintf("Finding matching %s documents…", doctype), func() {
		rows, listErr = c.GetList(cmd.Context(), doctype, client.ListOptions{
			Fields: []string{"name"}, Filters: filters, Limit: -1, OrderBy: "name asc",
		})
	})
	if listErr != nil {
		return nil, listErr
	}
	if spinErr != nil {
		return nil, spinErr
	}
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		n, ok := docName(r["name"])
		if !ok {
			return nil, fmt.Errorf("the list returned a row without a name: %v", r)
		}
		names = append(names, n)
	}
	return names, nil
}

// namePreview lists up to 10 names, then how many more there are.
func namePreview(names []string) string {
	preview := names
	if len(preview) > 10 {
		preview = append(append([]string{}, names[:10]...), fmt.Sprintf("… and %d more", len(names)-10))
	}
	quoted := make([]string, len(preview))
	for i, n := range preview {
		quoted[i] = text.Sanitize(n)
	}
	return strings.Join(quoted, ", ")
}
