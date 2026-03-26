package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/version"
	"github.com/spf13/cobra"
)

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

func runMCP(_ *cobra.Command, _ []string) error {
	if mcpDetach {
		port := mcpPort
		if port == 0 {
			port = defaultMCPPort
		}
		if err := startDetached(port); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "MCP server started in background on http://localhost:%d/mcp\n", port)
		fmt.Fprintf(os.Stderr, "  ffc mcp status   — check status\n")
		fmt.Fprintf(os.Stderr, "  ffc mcp stop     — stop server\n")
		return nil
	}

	if mcpPort != 0 {
		return runHTTPServer(mcpPort)
	}

	// Default: stdio server.
	cfg, err := config.Load(siteName, configPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	fc := client.New(cfg)

	s := server.NewMCPServer(
		"ffc",
		version.Version,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)
	registerTools(s, fc)

	fmt.Fprintf(os.Stderr, "ffc MCP server running (stdio). Press Ctrl+C to stop.\n")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := server.ServeStdio(s, server.WithStdioContextFunc(func(_ context.Context) context.Context {
		return ctx
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
