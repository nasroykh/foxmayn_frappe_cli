package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/itchyny/gojq"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
	"github.com/spf13/cobra"
)

// Output flags, resolved by resolveOutput before any command runs.
var (
	outputFlag string
	jqFlag     string

	outFormat = output.FormatTable
	jqCode    *gojq.Code
)

// resolveOutput turns --output, --json, --jq and FFC_OUTPUT into outFormat
// and jqCode. An invalid choice is a usage error, reported before the
// command sends anything.
func resolveOutput() error {
	outFormat, jqCode = output.FormatTable, nil
	flags := rootCmd.PersistentFlags()
	name := outputFlag
	if !flags.Changed("output") {
		name = os.Getenv("FFC_OUTPUT")
		if jsonOutput {
			name = string(output.FormatJSON)
		}
	}
	if name != "" {
		f, err := output.ParseFormat(name)
		if err != nil {
			if !flags.Changed("output") {
				err = fmt.Errorf("FFC_OUTPUT: %w", err)
			}
			return &usageError{err}
		}
		if jsonOutput && f != output.FormatJSON {
			return usageErrorf("--json conflicts with --output %s", f)
		}
		outFormat = f
	}
	// Machine output means machine errors: JSON on stderr.
	jsonOutput = outFormat == output.FormatJSON || outFormat == output.FormatNDJSON

	if jqFlag != "" {
		q, err := gojq.Parse(jqFlag)
		if err != nil {
			return usageErrorf("--jq: %v", err)
		}
		if jqCode, err = gojq.Compile(q); err != nil {
			return usageErrorf("--jq: %v", err)
		}
	}
	return nil
}

// render prints a command's result. The table format calls table, the
// command's own view; the others render v (after --jq). fields orders the
// CSV/TSV columns.
func render(v interface{}, fields []string, table func() error) error {
	if jqCode != nil {
		return renderJQ(v, fields)
	}
	if outFormat == output.FormatTable {
		return table()
	}
	return output.Write(os.Stdout, outFormat, v, fields, stdoutIsTerminal())
}

// renderJQ runs --jq on v. With the default format each result prints like
// jq -r: strings as raw lines, other values as JSON. With --output the
// results are rendered in that format: one result as itself, several as a
// list.
func renderJQ(v interface{}, fields []string) error {
	in, err := output.Normalize(v)
	if err != nil {
		return err
	}
	var results []interface{}
	iter := jqCode.Run(in)
	for {
		r, ok := iter.Next()
		if !ok {
			break
		}
		if err, isErr := r.(error); isErr {
			var halt *gojq.HaltError
			if errors.As(err, &halt) && halt.Value() == nil {
				break // halt: a normal stop
			}
			return fmt.Errorf("--jq: %w", err)
		}
		results = append(results, r)
	}
	if outFormat != output.FormatTable {
		var out interface{} = results
		if len(results) == 1 {
			out = results[0]
		}
		return output.Write(os.Stdout, outFormat, out, fields, stdoutIsTerminal())
	}
	for _, r := range results {
		line, isString := r.(string)
		if !isString {
			b, err := json.MarshalIndent(r, "", "  ")
			if err != nil {
				return fmt.Errorf("--jq: encoding a result: %w", err)
			}
			line = string(b)
		}
		if stdoutIsTerminal() {
			line = text.Sanitize(line)
		}
		if _, err := fmt.Fprintln(os.Stdout, line); err != nil {
			return err
		}
	}
	return nil
}

// listPager fetches one page of a list: rows from start, at most size.
type listPager func(ctx context.Context, start, size int) ([]map[string]interface{}, error)

// renderAll pages through a whole list. JSON, NDJSON, CSV and TSV are
// written as each page arrives; the table, YAML and --jq need the whole
// list and render it at the end. title labels the spinner.
func renderAll(ctx context.Context, title string, pageSize int, fetch listPager, fields []string, table func([]map[string]interface{}) error) error {
	var stream *output.ListStream
	if jqCode == nil && output.Streams(outFormat) {
		stream = output.NewListStream(os.Stdout, outFormat, fields, stdoutIsTerminal())
	}
	var all []map[string]interface{}
	for start := 0; ; {
		var rows []map[string]interface{}
		var fetchErr error
		if err := runSpinner(fmt.Sprintf("%s (%d so far)…", title, start), func() {
			rows, fetchErr = fetch(ctx, start, pageSize)
		}); err != nil {
			return errAborted
		}
		if fetchErr != nil {
			return fetchErr
		}
		if stream != nil {
			if err := stream.Rows(rows); err != nil {
				return writeErr(err)
			}
		} else {
			all = append(all, rows...)
		}
		start += len(rows)
		if len(rows) < pageSize {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if stream != nil {
		return writeErr(stream.Close())
	}
	if all == nil {
		all = []map[string]interface{}{}
	}
	return render(all, fields, func() error { return table(all) })
}

// writeErr labels a failed write to stdout (a closed pipe, a full disk).
func writeErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("writing output: %w", err)
}

func init() {
	rootCmd.PersistentFlags().StringVar(&outputFlag, "output", "", "Output format: table, json, ndjson, csv, tsv or yaml (default table, or $FFC_OUTPUT)")
	rootCmd.PersistentFlags().StringVar(&jqFlag, "jq", "", "Filter the JSON result with a jq expression (strings print raw)")
}

// pageFlags are the --all and --page-size flags of the list commands.
type pageFlags struct {
	all      bool
	pageSize int
}

func (p *pageFlags) register(cmd *cobra.Command, exclusive ...string) {
	cmd.Flags().BoolVar(&p.all, "all", false, "Fetch every row, page by page (streamed for json, ndjson, csv and tsv)")
	cmd.Flags().IntVar(&p.pageSize, "page-size", 500, "Rows per request with --all")
	for _, f := range exclusive {
		cmd.MarkFlagsMutuallyExclusive("all", f)
	}
}

// listDocs runs a list command: one request, or with --all every page.
// Without --order-by, --all sorts by creation and name, because Frappe's
// default (modified desc) moves rows between pages while documents change.
func listDocs(cmd *cobra.Command, p pageFlags, title, doctype string, opts client.ListOptions, fields []string, table func([]map[string]interface{}) error) error {
	if !p.all {
		rows, err := callSite(cmd, title, func(ctx context.Context, c *client.FrappeClient) ([]map[string]interface{}, error) {
			return c.GetList(ctx, doctype, opts)
		})
		if err != nil {
			return err
		}
		return render(rows, fields, func() error { return table(rows) })
	}
	if p.pageSize < 1 {
		return usageErrorf("--page-size must be at least 1")
	}
	if opts.OrderBy == "" {
		opts.OrderBy = "creation asc, name asc"
	}
	ctx := cmd.Context()
	c, err := newClient(ctx)
	if err != nil {
		return err
	}
	defer c.CloseQuietly()
	return renderAll(ctx, title, p.pageSize, func(ctx context.Context, start, size int) ([]map[string]interface{}, error) {
		o := opts
		o.Start, o.Limit = start, size
		return c.GetList(ctx, doctype, o)
	}, fields, table)
}

// machineOutput reports whether the result is printed as data (a machine
// format or --jq) instead of the command's table.
func machineOutput() bool {
	return outFormat != output.FormatTable || jqCode != nil
}

// printResult prints v as data; call it only when machineOutput is true.
func printResult(v interface{}, fields ...string) error {
	return render(v, fields, func() error { return output.PrintJSON(v) })
}
