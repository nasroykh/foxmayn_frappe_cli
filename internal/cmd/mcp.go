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
	"github.com/nasroykh/foxmayn_frappe_cli/internal/version"
	"github.com/spf13/cobra"
)

// newMCPClientProvider returns the clientFn the MCP tools use, plus a close
// func to call on shutdown. The client is built once and reused (one HTTP
// transport, one login for session-auth sites) while the site's credentials
// are unchanged; every call still goes through loadSite, so an expired OAuth
// token is refreshed and a config edit (or a token refreshed by another ffc
// process) is picked up by rebuilding the client.
func newMCPClientProvider() (clientFn, func()) {
	var (
		mu  sync.Mutex
		key string
		fc  *client.FrappeClient
	)
	get := func(ctx context.Context) (*client.FrappeClient, error) {
		mu.Lock()
		defer mu.Unlock()
		cfg, err := loadSite(ctx)
		if err != nil {
			return nil, err
		}
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
	return get, closeFn
}

var (
	mcpDetach   bool
	mcpPort     int
	mcpReadOnly bool
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
	provider, closeProvider := newMCPClientProvider()
	defer closeProvider()
	// Validate credentials once up front so misconfiguration fails immediately.
	if _, err := provider(cmd.Context()); err != nil {
		return err
	}

	s := server.NewMCPServer(
		"ffc",
		version.Version,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)
	registerTools(s, provider)

	fmt.Fprintf(os.Stderr, "ffc MCP server running (stdio). Press Ctrl+C to stop.\n")

	if err := server.ServeStdio(s, server.WithStdioContextFunc(func(_ context.Context) context.Context {
		return cmd.Context()
	})); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("mcp server: %w", err)
	}
	return nil
}

func init() {
	mcpCmd.Flags().BoolVarP(&mcpDetach, "detach", "d", false, "Run as a background HTTP server (use 'ffc mcp stop' to stop)")
	mcpCmd.Flags().BoolVar(&mcpReadOnly, "read-only", false, "Expose only read tools (no create, update, delete, bulk or call_method)")
	mcpCmd.Flags().IntVarP(&mcpPort, "port", "p", 0, fmt.Sprintf("Port for HTTP mode (default %d, implies HTTP transport)", defaultMCPPort))
	rootCmd.AddCommand(mcpCmd)
}
