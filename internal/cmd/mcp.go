package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/version"
	"github.com/spf13/cobra"
)

// newMCPEnv returns the environment the MCP tools run in for the served
// sites, plus a close func to call on shutdown. A site is read from the
// config on every call, so a policy or credential edit applies to the next
// call. Each site's client is built once and reused (one HTTP transport, one
// login for session-auth sites) while its credentials are unchanged; an
// expired OAuth token is refreshed, and a token refreshed by another ffc
// process is picked up by rebuilding the client.
func newMCPEnv(sites []string) (*mcpEnv, func(), error) {
	audit, err := newAuditLog()
	if err != nil {
		return nil, nil, err
	}
	type siteClient struct {
		mu  sync.Mutex // one build or login per site at a time
		key string
		fc  *client.FrappeClient
	}
	var (
		mu      sync.Mutex
		clients = map[string]*siteClient{}
	)
	get := func(ctx context.Context, site *config.SiteConfig) (*client.FrappeClient, error) {
		mu.Lock()
		sc := clients[site.Name]
		if sc == nil {
			sc = &siteClient{}
			clients[site.Name] = sc
		}
		mu.Unlock()
		sc.mu.Lock()
		defer sc.mu.Unlock()
		cfg := refreshSite(ctx, site)
		k := strings.Join([]string{cfg.URL, cfg.AccessToken, cfg.APIKey, cfg.APISecret, cfg.Username, cfg.Password}, "\x00")
		if sc.fc != nil && k == sc.key {
			return sc.fc, nil
		}
		// A client whose token the site rejects in the middle of a call
		// refreshes it itself (newSiteClient); the next call then finds the
		// new token in the config and builds a new client, as it does after
		// a refresh by another process.
		c, err := newSiteClient(ctx, cfg)
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
		for _, sc := range clients {
			sc.mu.Lock()
			if sc.fc != nil {
				sc.fc.CloseQuietly()
				sc.fc = nil
			}
			sc.mu.Unlock()
		}
	}
	env := &mcpEnv{
		sites: sites,
		site: func(_ context.Context, name string) (*config.SiteConfig, error) {
			site, err := config.Load(name, configPath)
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
		},
		client:   get,
		flags:    mcpFlags,
		audit:    audit,
		toolsets: mcpToolsets,
	}
	env.flags.ReadOnly = mcpReadOnly
	return env, closeFn, nil
}

// cleanMCPFlags trims the policy flag values and refuses one given with no
// value: "--allow-doctypes=" reads as "none" but would mean "no limit".
func cleanMCPFlags(cmd *cobra.Command) error {
	for name, list := range map[string]*[]string{
		"allow-tools": &mcpFlags.AllowTools, "allow-doctypes": &mcpFlags.AllowDoctypes,
		"deny-doctypes": &mcpFlags.DenyDoctypes, "allow-methods": &mcpFlags.AllowMethods,
		"deny-methods": &mcpFlags.DenyMethods,
	} {
		var out []string
		for _, v := range *list {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
		if cmd.Flags().Changed(name) && len(out) == 0 {
			return usageErrorf("--%s needs at least one value", name)
		}
		*list = out
	}
	if err := cleanToolsets(cmd); err != nil {
		return err
	}
	switch mcpFlags.Confirm = strings.TrimSpace(mcpFlags.Confirm); mcpFlags.Confirm {
	case "", config.ConfirmIfSupported, config.ConfirmAlways:
	case config.ConfirmNever:
		return usageErrorf("--confirm can only tighten the site's setting; to turn confirmation off, set sites.<site>.mcp.confirm: never in the config")
	default:
		return usageErrorf("--confirm must be always or if-supported, not %q", mcpFlags.Confirm)
	}
	return nil
}

// startMCP builds the environment and the server with the tools the served
// sites' policies allow. It checks the default site's credentials up front,
// so a misconfiguration fails at start rather than on the first tool call.
// The other sites are only read (config and policy): signing in to each
// could take longer than a client or the detached start waits, and a site
// that is down must not stop the others. Their first call signs in.
func startMCP(ctx context.Context) (*server.MCPServer, func(), error) {
	sites, err := mcpSites()
	if err != nil {
		return nil, nil, err
	}
	env, closeEnv, err := newMCPEnv(sites)
	if err != nil {
		return nil, nil, err
	}
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
			return nil, nil, err
		case err != nil:
			fmt.Fprintf(os.Stderr, "warning: site %q: %v\n", name, err)
		}
	}
	s := server.NewMCPServer("ffc", version.Version, mcpServerOptions()...)
	registerTools(s, env, policies)
	if len(sites) > 1 {
		fmt.Fprintf(os.Stderr, "Serving %d sites: %s. Every tool call must name its site.\n", len(sites), strings.Join(sites, ", "))
	}
	return s, closeEnv, nil
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
		if err := startDetached(cmd.Context(), port); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "MCP server started in background on http://127.0.0.1:%d/mcp\n", port)
		fmt.Fprintf(os.Stderr, "  ffc mcp status   — check status (shows the bearer token)\n")
		fmt.Fprintf(os.Stderr, "  ffc mcp stop     — stop server\n")
		return nil
	}

	if mcpPort != 0 {
		return runHTTPServer(cmd.Context(), mcpPort)
	}

	// Default: stdio server.
	s, closeEnv, err := startMCP(cmd.Context())
	if err != nil {
		return err
	}
	defer closeEnv()

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
	mcpCmd.Flags().StringSliceVar(&mcpToolsets, "toolsets", nil, "Expose only these tool sets: core (documents, reports, bulk, call_method, whoami, check_permission), lifecycle (submit, cancel, amend, copy, rename, workflow), collab (comments, assignments, tags), admin (share, unshare) and files (list_attachments, attach_file, get_print_html); default core,lifecycle")
	mcpCmd.Flags().StringVar(&mcpFlags.Confirm, "confirm", "", "Ask the user before deleting, cancelling or merging: always (refuse when the client cannot ask) or if-supported (tightens the config)")
	mcpCmd.Flags().IntVarP(&mcpPort, "port", "p", 0, fmt.Sprintf("Port for HTTP mode (default %d, implies HTTP transport)", defaultMCPPort))
	rootCmd.AddCommand(mcpCmd)
}
