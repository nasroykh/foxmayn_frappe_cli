package cmd

import (
	"context"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// search flags
var (
	seDoctype string
	seLimit   int
)

// defaultSearchLimit is the page length of ffc search and the MCP search
// tool; maxMCPSearchLimit caps what a model may ask for (global search loads
// every hit's document on the server).
const (
	defaultSearchLimit = 20
	maxMCPSearchLimit  = 100
)

var searchCmd = &cobra.Command{
	Use:   "search TEXT...",
	Short: "Search documents by text, or look a name up in one DocType",
	Long: `Search a Frappe site.

With --doctype, ffc runs the search a Link field runs (search_link): it
matches the DocType's search fields and title, applies its link query and the
user's permissions, and returns value (the name), label and description. It
is the way to resolve "Acme" to the document name "CUST-0042". An empty TEXT
lists the first documents. Frappe marks the answer cacheable for 60 seconds;
ffc keeps no cache, but a proxy in front of the site may serve it, so a
document created a moment ago may not show up yet.

Without --doctype, ffc runs Frappe's global search over every DocType the user
may read, ranked by relevance. Only DocTypes listed in Global Search Settings
are searched, and only fields flagged "In Global Search" are indexed; a
DocType missing from there never returns a hit. "a & b" searches the phrases
a and b separately and combines the hits.

TEXT may be several words; they are joined with spaces. Put "--" before a
text that starts with a dash.

Examples:
  ffc search acme -d Customer
  ffc search "" -d Item --limit 5
  ffc search "jetons de presence"
  ffc search overdue invoice --limit 10 --json
`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		text := strings.Join(args, " ")
		if err := validateSearch(text, seDoctype, seLimit); err != nil {
			return err
		}
		rows, err := callSite(cmd, "Searching…", func(ctx context.Context, c *client.FrappeClient) ([]map[string]interface{}, error) {
			return runSearch(ctx, c, text, seDoctype, seLimit)
		})
		if err != nil {
			return err
		}
		return render(rows, nil, func() error {
			output.PrintTable(searchTableRows(rows, seDoctype != ""))
			return nil
		})
	},
}

func init() {
	searchCmd.Flags().StringVarP(&seDoctype, "doctype", "d", "", "Resolve names in this DocType (search_link); omit to search every DocType")
	searchCmd.Flags().IntVarP(&seLimit, "limit", "l", defaultSearchLimit, "Maximum results (at least 1)")
	rootCmd.AddCommand(searchCmd)
}

// validateSearch checks the arguments shared by the CLI and the MCP tool. An
// empty text is meaningful only for a DocType search, where it lists the
// first documents; the global index has nothing to match it against.
func validateSearch(text, doctype string, limit int) error {
	switch {
	case limit < 1:
		return usageErrorf("--limit must be at least 1")
	case doctype == "" && strings.TrimSpace(text) == "":
		return usageErrorf("search text is empty: give some text, or --doctype to list a DocType")
	}
	return nil
}

// runSearch runs search_link for a DocType and the global search without one.
func runSearch(ctx context.Context, c *client.FrappeClient, text, doctype string, limit int) ([]map[string]interface{}, error) {
	if doctype != "" {
		return c.SearchLink(ctx, doctype, text, limit)
	}
	return c.GlobalSearch(ctx, text, limit)
}

// searchTableRows shapes search rows for the table and returns the columns.
// search_link has a label only for some DocTypes and it usually repeats the
// value, so the column appears when some row has a different one. Global
// search content is a long "Field : value" text: it is cut to one line.
func searchTableRows(rows []map[string]interface{}, link bool) ([]map[string]interface{}, []string) {
	out := make([]map[string]interface{}, len(rows))
	hasLabel, hasTitle := false, false
	for i, r := range rows {
		row := make(map[string]interface{}, len(r))
		for k, v := range r {
			row[k] = v
		}
		if s, ok := row["content"].(string); ok {
			row["content"] = oneLine(s, 100)
		}
		if l, ok := row["label"].(string); ok && l != "" && l != row["value"] {
			hasLabel = true
		}
		if t, ok := row["title"].(string); ok && t != "" && t != row["name"] {
			hasTitle = true
		}
		out[i] = row
	}
	var cols []string
	if link {
		cols = []string{"value"}
		if hasLabel {
			cols = append(cols, "label")
		}
		cols = append(cols, "description")
		return out, cols
	}
	cols = []string{"doctype", "name"}
	if hasTitle {
		cols = append(cols, "title")
	}
	return out, append(cols, "content")
}

// oneLine collapses whitespace and cuts s to at most n runes.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
