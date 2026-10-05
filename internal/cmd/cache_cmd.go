package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

var (
	ccAllSites bool
	cwDoctypes []string
)

var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Show, fill or clear the local cache of site metadata",
	Long: `ffc keeps a small cache per site in the user cache directory
(~/.cache/ffc/<site> on Linux): the installed apps (24 h), the DocType and
report lists (24 h) and compact DocType schemas (1 h, at most 100). Shell
completion reads it and never fills it or sends a request. Documents are
never cached.

list-doctypes and list-reports fill the lists when they return the whole list
(no --module; --all, --limit 0 or fewer rows than the limit), get-schema
caches the schema it fetched and answers from the cache until it expires
(--refresh fetches it again). "ffc cache warm" fills it in one go.`,
}

var cacheStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "List the cached entries of the selected site, their age and size",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadSiteConfig()
		if err != nil {
			return err
		}
		dir, err := serverCacheDir(cfg)
		if err != nil {
			return err
		}
		entries := cacheEntries(cfg, time.Now())
		var total int64
		for _, e := range entries {
			total += e.Bytes
		}
		if entries == nil {
			entries = []cacheEntry{}
		}
		res := map[string]interface{}{"site": cfg.Name, "url": redactedURL(cfg.URL), "dir": dir, "entries": entries, "bytes": total}
		return render(res, nil, func() error {
			fmt.Printf("Cache of %s: %s\n", cacheSiteLabel(cfg), dir)
			if len(entries) == 0 {
				fmt.Println("Empty.")
				return nil
			}
			rows := make([]map[string]interface{}, len(entries))
			for i, e := range entries {
				age := "?"
				if !e.FetchedAt.IsZero() {
					age = (time.Duration(e.AgeSec) * time.Second).String()
				}
				state := "fresh"
				if !e.Fresh {
					state = "stale"
				}
				rows[i] = map[string]interface{}{"kind": e.Kind, "name": e.Name, "age": age, "size": byteSize(e.Bytes), "state": state}
			}
			output.PrintTable(rows, []string{"kind", "name", "age", "size", "state"})
			fmt.Printf("Total: %s\n", byteSize(total))
			return nil
		})
	},
}

var cacheClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Delete the cache of the selected site (or of every site with --all-sites)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var dir, label string
		var cfg *config.SiteConfig
		if ccAllSites {
			base, err := userCacheDir()
			if err != nil {
				return fmt.Errorf("cache: %w", err)
			}
			if !filepath.IsAbs(base) {
				return fmt.Errorf("cache: the user cache directory %q is not absolute", base)
			}
			dir = filepath.Join(base, "ffc")
		} else {
			var err error
			if cfg, err = loadSiteConfig(); err != nil {
				return err
			}
			// Every credential's cache of the site.
			if dir, err = siteCacheRoot(cfg); err != nil {
				return fmt.Errorf("cache: %w", err)
			}
			label = cacheSiteLabel(cfg)
		}
		n := countFiles(dir)
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("cache: %w", err)
		}
		res := map[string]interface{}{"dir": dir, "removed": n}
		if cfg != nil {
			res["site"], res["url"] = cfg.Name, redactedURL(cfg.URL)
		}
		return render(res, nil, func() error {
			what := "every site"
			if cfg != nil {
				what = label
			}
			fmt.Printf("Removed %d cached files of %s (%s).\n", n, what, dir)
			return nil
		})
	},
}

var cacheWarmCmd = &cobra.Command{
	Use:   "warm",
	Short: "Fetch the DocType and report lists, and the schemas of --doctypes, into the cache",
	Long: `Fetch the DocType list and the report list of the selected site (two
requests) and the schema of each DocType given with --doctypes (three requests
each) into the local cache, for shell completion and get-schema.

Examples:
  ffc cache warm
  ffc cache warm --doctypes "Sales Invoice,Customer"`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		type failure struct {
			Item  string `json:"item"` // "reports" or "schema:<DocType>"
			Error string `json:"error"`
		}
		type warmed struct {
			Site     string    `json:"site"`
			URL      string    `json:"url"`
			Doctypes int       `json:"doctypes"`
			Reports  int       `json:"reports"`
			Schemas  []string  `json:"schemas"`
			Errors   []failure `json:"errors"`
			label    string
			items    int
		}
		res, err := callSiteCfg(cmd, "Warming the cache…", func(ctx context.Context, c *client.FrappeClient, cfg *config.SiteConfig) (warmed, error) {
			w := warmed{Site: cfg.Name, URL: redactedURL(cfg.URL), Schemas: []string{}, Errors: []failure{}, label: cacheSiteLabel(cfg), items: 1}
			dts, err := c.GetList(ctx, "DocType", client.ListOptions{Fields: []string{"name"}, Limit: -1, OrderBy: "name asc"})
			if err != nil {
				return w, fmt.Errorf("listing DocTypes: %w", err)
			}
			if err := writeListCache(cfg, "DocType", dts); err != nil {
				return w, fmt.Errorf("writing the cache: %w", err)
			}
			w.Doctypes = len(dts)
			reps, err := c.GetList(ctx, "Report", client.ListOptions{Fields: []string{"name", "ref_doctype"}, Limit: -1, OrderBy: "name asc"})
			if err != nil {
				w.Errors = append(w.Errors, failure{"reports", err.Error()})
			} else if err := writeListCache(cfg, "Report", reps); err != nil {
				return w, fmt.Errorf("writing the cache: %w", err)
			} else {
				w.Reports = len(reps)
			}
			for _, dt := range cwDoctypes {
				if dt = strings.TrimSpace(dt); dt == "" {
					continue
				}
				w.items++
				if err := ctx.Err(); err != nil {
					return w, err
				}
				doc, warnings, err := fetchSchema(ctx, c, dt)
				if err != nil {
					w.Errors = append(w.Errors, failure{"schema:" + dt, err.Error()})
					continue
				}
				if err := writeSchemaCache(cfg, dt, compactSchema(doc), schemaTableRows(doc), warnings); err != nil {
					return w, fmt.Errorf("writing the cache: %w", err)
				}
				w.Schemas = append(w.Schemas, dt)
			}
			return w, nil
		})
		if err != nil {
			return err
		}
		if rerr := render(res, nil, func() error {
			fmt.Printf("Cached %d DocTypes, %d reports and %d schemas of %s.\n", res.Doctypes, res.Reports, len(res.Schemas), res.label)
			for _, f := range res.Errors {
				fmt.Fprintf(os.Stderr, "failed: %s: %s\n", f.Item, f.Error)
			}
			return nil
		}); rerr != nil {
			return rerr
		}
		if len(res.Errors) > 0 {
			// The lists and the schemas that did come back are cached.
			return &partialError{fmt.Sprintf("cache warm: %d of %d items failed (the rest is cached)", len(res.Errors), res.items)}
		}
		return nil
	},
}

// cacheSiteLabel names a site in messages: "site <name>", or its URL (password
// hidden) for a site defined only by FFC_* variables.
func cacheSiteLabel(cfg *config.SiteConfig) string {
	if cfg.Name != "" {
		return "site " + cfg.Name
	}
	return redactedURL(cfg.URL)
}

// countFiles counts the regular files under dir (0 when it does not exist).
func countFiles(dir string) int {
	n := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			n++
		}
		return nil
	})
	return n
}

// byteSize formats n bytes for people: 512 B, 3.4 KiB, 1.2 MiB.
func byteSize(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

func init() {
	cacheClearCmd.Flags().BoolVar(&ccAllSites, "all-sites", false, "Delete the cache of every site, not only the selected one")
	cacheWarmCmd.Flags().StringSliceVar(&cwDoctypes, "doctypes", nil, "Also cache the schemas of these DocTypes (comma-separated)")
	cacheCmd.AddCommand(cacheStatusCmd, cacheClearCmd, cacheWarmCmd)
	rootCmd.AddCommand(cacheCmd)
}
