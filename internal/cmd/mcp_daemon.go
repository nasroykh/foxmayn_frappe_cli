package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/version"
	"github.com/spf13/cobra"
)

const defaultMCPPort = 8765

type mcpState struct {
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	Site      string    `json:"site"`
	StartedAt time.Time `json:"started_at"`
	LogPath   string    `json:"log_path"`
}

func mcpStateDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "ffc")
}

func mcpStatePath() string {
	d := mcpStateDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "mcp.json")
}

func mcpLogPath() string {
	d := mcpStateDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "mcp.log")
}

func writeMCPState(state mcpState) error {
	path := mcpStatePath()
	if path == "" {
		return fmt.Errorf("cannot determine config dir")
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
}

func readMCPState() (*mcpState, error) {
	path := mcpStatePath()
	if path == "" {
		return nil, fmt.Errorf("cannot determine config dir")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var state mcpState
	if err := json.Unmarshal(b, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func removeMCPState() {
	if path := mcpStatePath(); path != "" {
		os.Remove(path)
	}
}

func isProcessRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// startDetached re-execs the current binary as a background HTTP MCP server.
func startDetached(port int) error {
	state, err := readMCPState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	if state != nil && isProcessRunning(state.PID) {
		return fmt.Errorf("MCP server already running (PID %d, port %d) — run 'ffc mcp stop' first", state.PID, state.Port)
	}

	logPath := mcpLogPath()

	// Build child args: same site/config flags, explicit port, no --detach.
	args := []string{"mcp", "--port", strconv.Itoa(port)}
	if siteName != "" {
		args = append(args, "--site", siteName)
	}
	if configPath != "" {
		args = append(args, "--config", configPath)
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(os.Args[0], args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = logFile
	setSysProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting background process: %w", err)
	}

	if err := writeMCPState(mcpState{
		PID:       cmd.Process.Pid,
		Port:      port,
		Site:      siteName,
		StartedAt: time.Now().UTC(),
		LogPath:   logPath,
	}); err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("writing state file: %w", err)
	}

	cmd.Process.Release()
	return nil
}

// runHTTPServer starts the MCP server over HTTP on the given port.
func runHTTPServer(port int) error {
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

	addr := fmt.Sprintf(":%d", port)
	fmt.Fprintf(os.Stderr, "ffc MCP HTTP server listening on http://localhost%s/mcp\n", addr)

	httpServer := server.NewStreamableHTTPServer(s)
	if err := httpServer.Start(addr); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}

// mcpStatusCmd reports whether the detached MCP server is running.
var mcpStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of the detached MCP server",
	RunE: func(_ *cobra.Command, _ []string) error {
		state, err := readMCPState()
		if err != nil {
			return err
		}
		if state == nil {
			fmt.Println("MCP server: not running (no state file)")
			return nil
		}
		if isProcessRunning(state.PID) {
			fmt.Printf("MCP server: running\n")
			fmt.Printf("  PID:     %d\n", state.PID)
			fmt.Printf("  URL:     http://localhost:%d/mcp\n", state.Port)
			fmt.Printf("  Site:    %s\n", state.Site)
			fmt.Printf("  Started: %s\n", state.StartedAt.Local().Format("2006-01-02 15:04:05"))
			fmt.Printf("  Log:     %s\n", state.LogPath)
		} else {
			fmt.Printf("MCP server: stopped (stale state file, PID %d no longer alive)\n", state.PID)
			removeMCPState()
		}
		return nil
	},
}

// mcpStopCmd sends SIGTERM to the detached MCP server.
var mcpStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the detached MCP server",
	RunE: func(_ *cobra.Command, _ []string) error {
		state, err := readMCPState()
		if err != nil {
			return err
		}
		if state == nil {
			fmt.Println("MCP server is not running")
			return nil
		}
		if !isProcessRunning(state.PID) {
			fmt.Printf("MCP server (PID %d) is already stopped, cleaning up state file\n", state.PID)
			removeMCPState()
			return nil
		}
		proc, err := os.FindProcess(state.PID)
		if err != nil {
			return fmt.Errorf("finding process: %w", err)
		}
		if err := proc.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("sending SIGTERM to PID %d: %w", state.PID, err)
		}
		removeMCPState()
		fmt.Printf("Stopped MCP server (PID %d)\n", state.PID)
		return nil
	},
}

func init() {
	mcpCmd.AddCommand(mcpStatusCmd)
	mcpCmd.AddCommand(mcpStopCmd)
}
