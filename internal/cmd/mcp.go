package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/version"
	"github.com/spf13/cobra"
)

// newMCPClientProvider returns a clientFn that builds a fresh FrappeClient for
// each tool call: it first refreshes an expired OAuth token (and persists it),
// then reloads config and constructs the client (which re-logs-in session-auth
// sites). This keeps a long-running MCP server's credentials fresh instead of
// pinning one client built at startup (H5). The config refresh/load is
// serialized to avoid concurrent token-file writes.
func newMCPClientProvider() clientFn {
	var mu sync.Mutex
	return func(ctx context.Context) (*client.FrappeClient, error) {
		mu.Lock()
		tryRefreshOAuthToken()
		cfg, err := config.Load(siteName, configPath)
		mu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		return client.New(ctx, cfg)
	}
}

var (
	mcpDetach bool
	mcpPort   int
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

The HTTP endpoint is http://localhost:<port>/mcp (Streamable HTTP transport).

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
	provider := newMCPClientProvider()
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
	mcpCmd.Flags().IntVarP(&mcpPort, "port", "p", 0, fmt.Sprintf("Port for HTTP mode (default %d, implies HTTP transport)", defaultMCPPort))
	rootCmd.AddCommand(mcpCmd)
}
