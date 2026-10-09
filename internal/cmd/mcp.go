package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/version"
	"github.com/spf13/cobra"
)

// mcpOptions is everything an MCP server is built from, so no part of the
// build reads a flag variable. mcpOptionsFromFlags is the one place the
// command line becomes options.
type mcpOptions struct {
	configPath string   // config file; "" is the default path
	site       string   // the default site; "" is the config's default_site
	sites      []string // --sites
	allSites   bool     // --all-sites
	// policy narrows every served site's own policy (the flags); ReadOnly is
	// --read-only.
	policy   config.MCPPolicy
	toolsets []string // nil: defaultToolsets
	// api is set by NewMCPServer: errors name option fields, not flags, and
	// are not usage errors.
	api bool
}

// fail returns a usage error for the command line, a plain error for
// NewMCPServer.
func (o mcpOptions) fail(format string, a ...interface{}) error {
	if o.api {
		return fmt.Errorf(format, a...)
	}
	return usageErrorf(format, a...)
}

// optName is the flag or, for NewMCPServer, the option field.
func (o mcpOptions) optName(flag, field string) string {
	if o.api {
		return field
	}
	return flag
}

// mcpOptionsFromFlags reads the global flag variables of `ffc mcp`.
func mcpOptionsFromFlags() mcpOptions {
	o := mcpOptions{
		configPath: configPath,
		site:       siteName,
		sites:      mcpSiteList,
		allSites:   mcpAllSites,
		policy:     mcpFlags,
		toolsets:   mcpToolsets,
	}
	o.policy.ReadOnly = mcpReadOnly
	return o
}

// cfgPath returns the config file path: configPath or the default one.
func (o mcpOptions) cfgPath() (string, error) {
	if o.configPath != "" {
		return o.configPath, nil
	}
	p, err := config.DefaultConfigPath()
	if err != nil {
		return "", fmt.Errorf("resolving config path: %w", err)
	}
	return p, nil
}

// printMCPWarnings writes startMCP's warnings to stderr, one per line.
func printMCPWarnings(warnings []string) {
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, w)
	}
}

// newMCPEnv returns the environment the MCP tools run in for the served
// sites, plus a close func to call on shutdown. A site is read from the
// config on every call, so a policy or credential edit applies to the next
// call. Each site's client is built once and reused (one HTTP transport, one
// login for session-auth sites) while its credentials are unchanged; an
// expired OAuth token is refreshed, and a token refreshed by another ffc
// process is picked up by rebuilding the client.
func newMCPEnv(o mcpOptions, sites []string) (*mcpEnv, func(), error) {
	cfgPath, err := o.cfgPath()
	if err != nil {
		return nil, nil, err
	}
	env := &mcpEnv{cfgPath: cfgPath}
	type siteClient struct {
		mu  sync.Mutex // one build or login per site at a time
		key string
		fc  *client.FrappeClient
	}
	var (
		mu      sync.Mutex
		closed  atomic.Bool // set by closeFn; checked again under sc.mu
		clients = map[string]*siteClient{}
	)
	get := func(ctx context.Context, site *config.SiteConfig) (*client.FrappeClient, error) {
		mu.Lock()
		if closed.Load() {
			mu.Unlock()
			return nil, errors.New("MCP server closed")
		}
		sc := clients[site.Name]
		if sc == nil {
			sc = &siteClient{}
			clients[site.Name] = sc
		}
		mu.Unlock()
		sc.mu.Lock()
		defer sc.mu.Unlock()
		// A call that got sc before Close and its lock after it must not
		// build a client Close will never see.
		if closed.Load() {
			return nil, errors.New("MCP server closed")
		}
		cfg := refreshSite(ctx, env.cfgPath, site)
		k := strings.Join([]string{cfg.URL, cfg.AccessToken, cfg.APIKey, cfg.APISecret, cfg.Username, cfg.Password}, "\x00")
		if sc.fc != nil && k == sc.key {
			return sc.fc, nil
		}
		// A client whose token the site rejects in the middle of a call
		// refreshes it itself (newSiteClient); the next call then finds the
		// new token in the config and builds a new client, as it does after
		// a refresh by another process.
		c, err := newSiteClient(ctx, env.cfgPath, cfg)
		if err != nil {
			return nil, err
		}
		// The old client is dropped without logging out: tool calls that
		// fetched it before the swap may still be using its session.
		sc.fc, sc.key = c, k
		return sc.fc, nil
	}
	closeFn := func() {
		mu.Lock()
		defer mu.Unlock()
		closed.Store(true)
		for _, sc := range clients {
			sc.mu.Lock()
			if sc.fc != nil {
				sc.fc.CloseQuietly()
				sc.fc = nil
			}
			sc.mu.Unlock()
		}
	}
	env.sites = sites
	env.site = func(_ context.Context, name string) (*config.SiteConfig, error) {
		site, err := config.LoadSite(name, o.configPath)
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		// Load falls back to a case-insensitive match: a served site
		// removed or renamed since the start must not resolve to another
		// site ("Prod" gone, "PROD" added).
		if name != "" && site.Name != name {
			return nil, fmt.Errorf("config: site %q not found in config", name)
		}
		return site, nil
	}
	env.client = get
	env.flags = o.policy
	env.audit = newAuditLog(cfgPath)
	env.toolsets = o.toolsets
	env.confirm = newConfirmer()
	return env, closeFn, nil
}

// cleanMCPFlags validates the flags of `ffc mcp` with cleanMCPOptions, the
// same code NewMCPServer validates its options with, and stores the cleaned
// values back in the flag variables (daemonArgs reads them from there).
func cleanMCPFlags(cmd *cobra.Command) error {
	o := mcpOptionsFromFlags()
	if err := cleanMCPOptions(&o, true, func(flag string) bool { return cmd.Flags().Changed(flag) }); err != nil {
		return err
	}
	mcpFlags, mcpToolsets = o.policy, o.toolsets
	mcpFlags.ReadOnly = false // --read-only has its own variable
	return nil
}

// startMCP builds the environment and the server with the tools the served
// sites' policies allow. It checks the default site's credentials up front,
// so a misconfiguration fails at start rather than on the first tool call.
// The other sites are only read (config and policy): signing in to each
// could take longer than a client or the detached start waits, and a site
// that is down must not stop the others. Their first call signs in. The
// warnings are returned for the caller to print.
func startMCP(ctx context.Context, o mcpOptions) (*server.MCPServer, []string, func(), error) {
	s, _, warnings, closeEnv, err := buildMCP(ctx, o)
	return s, warnings, closeEnv, err
}

// buildMCP is startMCP that also returns the served sites, the default one
// first.
func buildMCP(ctx context.Context, o mcpOptions) (*server.MCPServer, []string, []string, func(), error) {
	sites, err := mcpSites(o)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	env, closeEnv, err := newMCPEnv(o, sites)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var warnings []string
	var policies []mcpPolicy
	for i, name := range sites {
		site, err := env.site(ctx, name)
		if err == nil {
			policies = append(policies, newMCPPolicy(site, env.flags))
			if i == 0 {
				_, err = env.client(ctx, site)
			}
		}
		switch {
		case err != nil && i == 0:
			closeEnv()
			return nil, nil, nil, nil, err
		case err != nil:
			warnings = append(warnings, fmt.Sprintf("warning: site %q: %v", name, err))
		}
	}
	s := server.NewMCPServer("ffc", version.Version, mcpServerOptions()...)
	registerTools(s, env, policies)
	if len(sites) > 1 {
		warnings = append(warnings, fmt.Sprintf("Serving %d sites: %s. Every tool call must name its site.", len(sites), strings.Join(sites, ", ")))
	}
	return s, sites, warnings, closeEnv, nil
}

var (
	mcpDetach   bool
	mcpPort     int
	mcpReadOnly bool
	mcpAllSites bool
	mcpSiteList []string // --sites
	// mcpFlags is the policy given on the command line. It can only narrow
	// the site's config: a project's MCP client config must not be able to
	// widen what the site owner allowed.
	mcpFlags config.MCPPolicy
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start an MCP (Model Context Protocol) server",
	Long: `Start an MCP server for AI agent integration.

By default, runs a stdio MCP server — use this mode in your MCP client config:
  ffc mcp --site mysite

To run in the background as an HTTP server:
  ffc mcp --detach [--port 8765] [--site mysite]
  ffc mcp status
  ffc mcp stop

The HTTP endpoint is http://127.0.0.1:<port>/mcp (Streamable HTTP transport, loopback only).

To add ffc to Claude Code, Claude Desktop, Cursor, VS Code or Codex:
  ffc mcp install --client <client> [--site mysite] [--read-only]
  ffc mcp uninstall --client <client>   (removes the entry again)

All tools use the same authentication and site config as other ffc commands.
`,
	RunE: runMCP,
}

func runMCP(cmd *cobra.Command, _ []string) error {
	if err := cleanMCPFlags(cmd); err != nil {
		return err
	}
	if mcpDetach {
		port := mcpPort
		if port == 0 {
			port = defaultMCPPort
		}
		if err := startDetached(cmd.Context(), mcpOptionsFromFlags(), port); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "MCP server started in background on http://127.0.0.1:%d/mcp\n", port)
		fmt.Fprintf(os.Stderr, "  ffc mcp status   — check status (shows the bearer token)\n")
		fmt.Fprintf(os.Stderr, "  ffc mcp stop     — stop server\n")
		return nil
	}

	if mcpPort != 0 {
		return runHTTPServer(cmd.Context(), mcpOptionsFromFlags(), mcpPort)
	}

	// Default: stdio server.
	s, warnings, closeEnv, err := startMCP(cmd.Context(), mcpOptionsFromFlags())
	if err != nil {
		return err
	}
	defer closeEnv()
	printMCPWarnings(warnings)

	fmt.Fprintf(os.Stderr, "ffc MCP server running (stdio). Press Ctrl+C to stop.\n")

	// Listen, not ServeStdio with a context func: the context must keep the
	// session mcp-go adds (client info and capabilities, elicitation), and
	// cmd.Context() is already cancelled on SIGINT/SIGTERM.
	if err := server.NewStdioServer(s).Listen(cmd.Context(), os.Stdin, os.Stdout); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("mcp server: %w", err)
	}
	return nil
}

func init() {
	mcpCmd.Flags().BoolVarP(&mcpDetach, "detach", "d", false, "Run as a background HTTP server (use 'ffc mcp stop' to stop)")
	mcpCmd.Flags().StringSliceVar(&mcpSiteList, "sites", nil, "Serve these sites; every tool call then names its site (default: the selected site only)")
	mcpCmd.Flags().BoolVar(&mcpAllSites, "all-sites", false, "Serve every site in the config; every tool call then names its site")
	mcpCmd.Flags().BoolVar(&mcpReadOnly, "read-only", false, "Expose only read tools (no create, update, delete, bulk or call_method)")
	mcpCmd.Flags().StringSliceVar(&mcpFlags.AllowTools, "allow-tools", nil, "Expose only these tools (narrows sites.<site>.mcp.allow_tools)")
	mcpCmd.Flags().StringSliceVar(&mcpFlags.AllowDoctypes, "allow-doctypes", nil, "Allow only these DocTypes (narrows the config; never unlocks a sensitive DocType)")
	mcpCmd.Flags().StringSliceVar(&mcpFlags.DenyDoctypes, "deny-doctypes", nil, "Refuse these DocTypes, in addition to the config")
	mcpCmd.Flags().StringSliceVar(&mcpFlags.AllowMethods, "allow-methods", nil, "call_method may call only these methods (narrows the config; a trailing * is a prefix)")
	mcpCmd.Flags().StringSliceVar(&mcpFlags.DenyMethods, "deny-methods", nil, "Refuse these methods in call_method, in addition to the config")
	mcpCmd.Flags().StringSliceVar(&mcpToolsets, "toolsets", nil, "Expose only these tool sets: core (documents, reports, bulk, call_method, whoami, check_permission), lifecycle (submit, cancel, amend, copy, rename, workflow), collab (comments, assignments, tags), admin (share, unshare) and files (list_attachments, attach_file, get_print_html) and erp (erp_map, erp_payment, erp_item, erp_stock, erp_party: ERPNext drafts and lookups); default core,lifecycle")
	mcpCmd.Flags().StringVar(&mcpFlags.Confirm, "confirm", "", "Ask the user before deleting, cancelling or merging: always (refuse when the client cannot ask) or if-supported (tightens the config)")
	mcpCmd.Flags().IntVarP(&mcpPort, "port", "p", 0, fmt.Sprintf("Port for HTTP mode (default %d, implies HTTP transport)", defaultMCPPort))
	rootCmd.AddCommand(mcpCmd)
}
