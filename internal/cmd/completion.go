package cmd

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/mcpinstall"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"github.com/spf13/cobra"
)

// Shell completion (`ffc completion bash|zsh|fish|powershell`, served by
// cobra's hidden __complete command) reads only the config file and the
// local cache. It never sends a request, never signs in (a password site
// would log in on every Tab press) and never writes the cache: a missing or
// expired entry completes nothing.

type completeFunc = func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective)

const noFiles = cobra.ShellCompDirectiveNoFileComp

var completionOnce sync.Once

// registerCompletions attaches the completion functions. It runs once, in
// execute, after every init has added its command and flags; a flag of any
// command named doctype or fields completes the same way, so a new command
// gets completion for free.
func registerCompletions(root *cobra.Command) {
	reg := func(c *cobra.Command, flag string, fn completeFunc) {
		if c.Flags().Lookup(flag) != nil || c.PersistentFlags().Lookup(flag) != nil {
			_ = c.RegisterFlagCompletionFunc(flag, fn)
		}
	}
	reg(root, "site", completeSites)
	reg(root, "output", fixedValues(formatNames()...))
	reg(root, "debug", fixedValues("basic", "body"))

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Flags().Lookup("doctype") != nil {
			reg(c, "doctype", completeDoctypes)
			reg(c, "fields", completeFields)
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)

	reg(runReportCmd, "name", completeReports)
	reg(canCmd, "perm", fixedValues(client.PermTypes...))
	reg(configSetCmd, "default-site", completeSites)
	var nf, df []string
	for _, f := range config.AllFormats {
		nf = append(nf, string(f.Key))
	}
	for _, f := range config.AllDateFormats {
		df = append(df, string(f.Key))
	}
	reg(configSetCmd, "number-format", fixedValues(nf...))
	reg(configSetCmd, "date-format", fixedValues(df...))

	reg(mcpCmd, "toolsets", commaValues(func() []string { return knownToolsets }))
	reg(assignCmd, "priority", fixedValues("Low", "Medium", "High"))
	reg(mcpCmd, "confirm", fixedValues("always", "if-supported"))
	reg(mcpInstallCmd, "client", fixedValues(mcpinstall.Clients...))
	reg(mcpUninstallCmd, "client", fixedValues(mcpinstall.Clients...))
	reg(mcpCmd, "sites", commaValues(func() []string { return siteNamesForCompletion() }))
	reg(mcpCmd, "allow-tools", commaValues(func() []string {
		names := make([]string, 0, len(toolActions))
		for n := range toolActions {
			names = append(names, n)
		}
		sort.Strings(names)
		return names
	}))
	reg(mcpCmd, "allow-doctypes", commaValues(cachedDoctypeNames))
	reg(mcpCmd, "deny-doctypes", commaValues(cachedDoctypeNames))
	reg(cacheWarmCmd, "doctypes", commaValues(cachedDoctypeNames))

	firstArgSite := func(c *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, noFiles
		}
		return completeSites(c, args, toComplete)
	}
	for _, c := range []*cobra.Command{siteUseCmd, siteRemoveCmd, siteRenameCmd, siteEditCmd} {
		c.ValidArgsFunction = firstArgSite
	}
}

func formatNames() []string {
	out := make([]string, len(output.Formats))
	for i, f := range output.Formats {
		out[i] = string(f)
	}
	return out
}

// fixedValues completes one of a fixed set of values.
func fixedValues(values ...string) completeFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return matching(values, toComplete), noFiles
	}
}

// commaValues completes the last item of a comma-separated list, keeping the
// items before it and leaving out the ones already given.
func commaValues(values func() []string) completeFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return commaComplete(values(), toComplete), cobra.ShellCompDirectiveNoSpace | noFiles
	}
}

func commaComplete(values []string, toComplete string) []cobra.Completion {
	prefix, last := "", toComplete
	if i := strings.LastIndex(toComplete, ","); i >= 0 {
		prefix, last = toComplete[:i+1], toComplete[i+1:]
	}
	have := map[string]bool{}
	for _, v := range strings.Split(prefix, ",") {
		have[strings.TrimSpace(v)] = true
	}
	var out []cobra.Completion
	for _, v := range matching(values, last) {
		if !have[v] {
			out = append(out, prefix+v)
		}
	}
	return out
}

// matching returns the values that start with prefix (case-insensitive, so
// zsh and fish with a case-insensitive matcher see them), leaving out any a
// shell could not show safely: a name read from the site may hold control
// or invisible characters.
func matching(values []string, prefix string) []cobra.Completion {
	var out []cobra.Completion
	lp := strings.ToLower(prefix)
	for _, v := range values {
		if v == "" || text.Sanitize(v) != v || strings.ContainsAny(v, "\t\n") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(v), lp) {
			out = append(out, v)
		}
	}
	return out
}

// siteNamesForCompletion lists the sites of the config file, without
// loading one (no OAuth refresh, no network).
func siteNamesForCompletion() []string {
	path, err := resolveCfgPath()
	if err != nil {
		return nil
	}
	cfg, err := config.Read(path)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(cfg.Sites))
	for n := range cfg.Sites {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func completeSites(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return matching(siteNamesForCompletion(), toComplete), noFiles
}

// completionSite is the selected site's config, read without the network;
// nil when the config cannot be loaded.
func completionSite() *config.SiteConfig {
	cfg, err := loadSiteConfig()
	if err != nil {
		return nil
	}
	return cfg
}

func cachedNames(kind string) []string {
	cfg := completionSite()
	if cfg == nil {
		return nil
	}
	items := readListCache(cfg, kind, time.Now())
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
	}
	return names
}

func cachedDoctypeNames() []string { return cachedNames("DocType") }

func completeDoctypes(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return matching(cachedDoctypeNames(), toComplete), noFiles
}

func completeReports(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return matching(cachedNames("Report"), toComplete), noFiles
}

// displayFieldtypes hold no value (frappe.model.display_fieldtypes);
// tableFieldtypes hold child rows, which a list query cannot select.
var (
	displayFieldtypes = map[string]bool{"Section Break": true, "Column Break": true, "Tab Break": true,
		"Attachment Gallery": true, "HTML": true, "Button": true, "Image": true, "Fold": true, "Heading": true}
	tableFieldtypes = map[string]bool{"Table": true, "Table MultiSelect": true}
	// standardFields are on every document (frappe.model.default_fields).
	standardFields = []string{"name", "owner", "creation", "modified", "modified_by", "docstatus", "idx"}
)

// completeFields completes --fields from the cached schema of --doctype:
// the last item of a comma-separated list. A JSON array is not completed.
func completeFields(cmd *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	dt, _ := cmd.Flags().GetString("doctype")
	if strings.TrimSpace(dt) == "" || strings.HasPrefix(strings.TrimSpace(toComplete), "[") {
		return nil, noFiles
	}
	cfg := completionSite()
	if cfg == nil {
		return nil, noFiles
	}
	sc := readSchemaCache(cfg, dt, time.Now())
	if sc == nil {
		return nil, noFiles
	}
	// get-doc filters the document's keys, where a table field is one.
	tables := cmd.Name() == "get-doc"
	var names []string
	seen := map[string]bool{}
	add := func(fn string) {
		if fn != "" && !seen[fn] {
			seen[fn] = true
			names = append(names, fn)
		}
	}
	for _, r := range sc.Rows {
		fn, _ := r["fieldname"].(string)
		ft, _ := r["fieldtype"].(string)
		if displayFieldtypes[ft] || (tableFieldtypes[ft] && !tables) {
			continue
		}
		add(fn)
	}
	for _, fn := range standardFields {
		add(fn)
	}
	return commaComplete(names, toComplete), cobra.ShellCompDirectiveNoSpace | noFiles
}
