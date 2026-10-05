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

// newMCPEnv returns the environment the MCP tools run in, plus a close func
// to call on shutdown. The site is read from the config on every call, so a
// policy or credential edit applies to the next call. The client is built
// once and reused (one HTTP transport, one login for session-auth sites)
// while the site's credentials are unchanged; an expired OAuth token is
// refreshed, and a token refreshed by another ffc process is picked up by
// rebuilding the client.
func newMCPEnv() (*mcpEnv, func(), error) {
	audit, err := newAuditLog()
	if err != nil {
		return nil, nil, err
	}
	var (
		mu  sync.Mutex
		key string
		fc  *client.FrappeClient
	)
	get := func(ctx context.Context, site *config.SiteConfig) (*client.FrappeClient, error) {
		mu.Lock()
		defer mu.Unlock()
		cfg := refreshSite(ctx, site)
		k := strings.Join([]string{cfg.URL, cfg.AccessToken, cfg.APIKey, cfg.APISecret, cfg.Username, cfg.Password}, "\x00")
		if fc != nil && k == key {
			return fc, nil
		}
		c, err := client.New(ctx, cfg)
		if err != nil {
			return nil, err
		}
		// The old client is dropped without logging out: tool calls that
		// fetched it before the swap may still be using its session.
		fc, key = c, k
		return fc, nil
	}
	closeFn := func() {
		mu.Lock()
		defer mu.Unlock()
		if fc != nil {
			fc.CloseQuietly()
			fc = nil
		}
	}
	env := &mcpEnv{
		site:   func(context.Context) (*config.SiteConfig, error) { return loadSiteConfig() },
		client: get,
		flags:  mcpFlags,
		audit:  audit,
	}
	env.flags.ReadOnly = mcpReadOnly
	return env, closeFn, nil
}

// startMCP builds the environment and the server with the tools the site's
// policy allows. It checks the credentials once up front, so a
// misconfiguration fails at start rather than on the first tool call.
func startMCP(ctx context.Context) (*server.MCPServer, func(), error) {
	env, closeEnv, err := newMCPEnv()
	if err != nil {
		return nil, nil, err
	}
	site, err := env.site(ctx)
	if err == nil {
		_, err = env.client(ctx, site)
	}
	if err != nil {
		closeEnv()
		return nil, nil, err
	}
	s := server.NewMCPServer(
		"ffc",
		version.Version,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)
	registerTools(s, env, newMCPPolicy(site, env.flags))
	return s, closeEnv, nil
}

var (
	mcpDetach   bool
	mcpPort     int
	mcpReadOnly bool
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
	mcpCmd.Flags().BoolVar(&mcpReadOnly, "read-only", false, "Expose only read tools (no create, update, delete, bulk or call_method)")
	mcpCmd.Flags().StringSliceVar(&mcpFlags.AllowTools, "allow-tools", nil, "Expose only these tools (narrows sites.<site>.mcp.allow_tools)")
	mcpCmd.Flags().StringSliceVar(&mcpFlags.AllowDoctypes, "allow-doctypes", nil, "Allow only these DocTypes (narrows the config; never unlocks a sensitive DocType)")
	mcpCmd.Flags().StringSliceVar(&mcpFlags.DenyDoctypes, "deny-doctypes", nil, "Refuse these DocTypes, in addition to the config")
	mcpCmd.Flags().StringSliceVar(&mcpFlags.AllowMethods, "allow-methods", nil, "call_method may call only these methods (narrows the config; a trailing * is a prefix)")
	mcpCmd.Flags().StringSliceVar(&mcpFlags.DenyMethods, "deny-methods", nil, "Refuse these methods in call_method, in addition to the config")
	mcpCmd.Flags().IntVarP(&mcpPort, "port", "p", 0, fmt.Sprintf("Port for HTTP mode (default %d, implies HTTP transport)", defaultMCPPort))
	rootCmd.AddCommand(mcpCmd)
}
