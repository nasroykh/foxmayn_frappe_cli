package cmd

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/version"
	"github.com/spf13/cobra"
)

const (
	defaultMCPPort  = 8765
	mcpTokenEnv     = "FFC_MCP_TOKEN"
	mcpInstanceEnv  = "FFC_MCP_INSTANCE"
	mcpHealthPath   = "/healthz"
	mcpServiceName  = "ffc-mcp"
	mcpProbeTimeout = 500 * time.Millisecond
)

type mcpState struct {
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	Site      string    `json:"site"`
	StartedAt time.Time `json:"started_at"`
	LogPath   string    `json:"log_path"`
	Token     string    `json:"token"`    // bearer token required by the HTTP transport (C2)
	Instance  string    `json:"instance"` // non-secret id used to verify process identity (M7/M15)
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { // L25
		return err
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600) // 0600: the file holds the bearer token (L3)
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

// isProcessRunning is a cheap liveness check (signal 0 on Unix). It cannot tell
// whether the PID still belongs to our server after PID reuse — mcpHealth does
// that. On Windows, Signal is unsupported, so this reports false; callers rely
// on mcpHealth there.
func isProcessRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// mcpHealth probes the daemon's unauthenticated health endpoint and returns the
// instance id it reports. A match against the recorded instance uniquely
// identifies our server on that port, independent of PID reuse (M7/M15).
func mcpHealth(port int, timeout time.Duration) (instance string, ok bool) {
	cl := &http.Client{Timeout: timeout}
	resp, err := cl.Get(fmt.Sprintf("http://127.0.0.1:%d%s", port, mcpHealthPath))
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	var h struct {
		Service  string `json:"service"`
		Instance string `json:"instance"`
	}
	if json.NewDecoder(resp.Body).Decode(&h) != nil || h.Service != mcpServiceName {
		return "", false
	}
	return h.Instance, true
}

// randomToken returns nBytes of crypto-random data as a URL-safe string.
func randomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func lastLogLines(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "(no log available)"
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// startDetached re-execs the current binary as a background HTTP MCP server.
func startDetached(ctx context.Context, port int) error {
	// Already running? Verify via the health endpoint so a stale/reused PID
	// doesn't block a fresh start (M7).
	if state, _ := readMCPState(); state != nil {
		if inst, ok := mcpHealth(state.Port, mcpProbeTimeout); ok && inst == state.Instance {
			return fmt.Errorf("MCP server already running (PID %d, port %d) — run 'ffc mcp stop' first", state.PID, state.Port)
		}
	}

	if dir := mcpStateDir(); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil { // L25
			return fmt.Errorf("creating state dir: %w", err)
		}
	}

	token, err := randomToken(24)
	if err != nil {
		return fmt.Errorf("generating token: %w", err)
	}
	instance, err := randomToken(16)
	if err != nil {
		return fmt.Errorf("generating instance id: %w", err)
	}

	logPath := mcpLogPath()
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // L3
	if err != nil {
		return fmt.Errorf("opening log file: %w", err)
	}
	defer logFile.Close()

	// Build child args: same site/config flags, explicit port, no --detach.
	args := []string{"mcp", "--port", strconv.Itoa(port)}
	if siteName != "" {
		args = append(args, "--site", siteName)
	}
	if configPath != "" {
		args = append(args, "--config", configPath)
	}

	cmd := exec.Command(os.Args[0], args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = logFile
	// Hand the token+instance to the child via the environment (never argv).
	cmd.Env = append(os.Environ(), mcpTokenEnv+"="+token, mcpInstanceEnv+"="+instance)
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
		Token:     token,
		Instance:  instance,
	}); err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("writing state file: %w", err)
	}
	cmd.Process.Release()

	// Poll the health endpoint to confirm the child actually started before
	// reporting success — a port conflict or bad config kills it seconds in (M15).
	for i := 0; i < 30; i++ {
		if inst, ok := mcpHealth(port, 300*time.Millisecond); ok && inst == instance {
			fmt.Fprintf(os.Stderr, "Authorization: Bearer %s\n", token)
			return nil
		}
		if ctx.Err() != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Never became healthy — stop the child and surface the log tail.
	if proc, e := os.FindProcess(cmd.Process.Pid); e == nil {
		_ = terminateProcess(proc)
	}
	removeMCPState()
	return fmt.Errorf("MCP server did not become healthy on port %d within 3s — last log lines:\n%s", port, lastLogLines(logPath, 15))
}

// mcpAuthMiddleware enforces the bearer token and rejects cross-origin requests,
// so only local clients holding the token can reach the tools (C2).
func mcpAuthMiddleware(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reject a browser-issued cross-origin request (DNS rebinding). A native
		// MCP client sends no Origin; a localhost Origin is also allowed.
		if origin := r.Header.Get("Origin"); origin != "" && !isLocalhostOrigin(origin) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLocalhostOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// runHTTPServer starts the MCP server over HTTP on the given port.
func runHTTPServer(ctx context.Context, port int) error {
	provider := newMCPClientProvider()
	// Validate credentials up front so misconfiguration fails immediately.
	if _, err := provider(ctx); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	s := server.NewMCPServer(
		"ffc",
		version.Version,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)
	registerTools(s, provider)

	// The token/instance are supplied by the parent when detached; generate
	// them for a foreground `ffc mcp --port N` run.
	token := os.Getenv(mcpTokenEnv)
	if token == "" {
		t, err := randomToken(24)
		if err != nil {
			return err
		}
		token = t
	}
	instance := os.Getenv(mcpInstanceEnv)
	if instance == "" {
		inst, err := randomToken(16)
		if err != nil {
			return err
		}
		instance = inst
	}

	streamable := server.NewStreamableHTTPServer(s)
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpAuthMiddleware(token, streamable))
	mux.HandleFunc(mcpHealthPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"service": mcpServiceName, "instance": instance})
	})

	addr := fmt.Sprintf("127.0.0.1:%d", port) // C1: bind localhost only
	httpServer := &http.Server{Addr: addr, Handler: mux}

	// Graceful shutdown on Ctrl+C / SIGTERM (L14).
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutCtx)
	}()

	fmt.Fprintf(os.Stderr, "ffc MCP HTTP server listening on http://%s/mcp\n", addr)
	fmt.Fprintf(os.Stderr, "  Authorization: Bearer %s\n", token)

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
		if inst, ok := mcpHealth(state.Port, mcpProbeTimeout); ok && inst == state.Instance {
			fmt.Printf("MCP server: running\n")
			fmt.Printf("  PID:     %d\n", state.PID)
			fmt.Printf("  URL:     http://127.0.0.1:%d/mcp\n", state.Port)
			fmt.Printf("  Auth:    Bearer %s\n", state.Token)
			fmt.Printf("  Site:    %s\n", state.Site)
			fmt.Printf("  Started: %s\n", state.StartedAt.Local().Format("2006-01-02 15:04:05"))
			fmt.Printf("  Log:     %s\n", state.LogPath)
		} else if isProcessRunning(state.PID) {
			fmt.Printf("MCP server: PID %d is alive but not responding on port %d (starting up or wedged)\n", state.PID, state.Port)
		} else {
			fmt.Printf("MCP server: stopped (stale state file, PID %d no longer alive)\n", state.PID)
			removeMCPState()
		}
		return nil
	},
}

var mcpStopForce bool

// mcpStopCmd stops the detached MCP server.
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

		inst, healthy := mcpHealth(state.Port, mcpProbeTimeout)
		confirmed := healthy && inst == state.Instance

		// By default only signal a PID the health endpoint confirms is *our*
		// server, so we never SIGTERM a process that reused the PID after a
		// reboot (M7). But don't orphan a wedged-but-alive server either: keep
		// the state file and let the user force-stop it.
		if !confirmed {
			if !isProcessRunning(state.PID) {
				fmt.Printf("MCP server (PID %d) is not running; cleaning up state file\n", state.PID)
				removeMCPState()
				return nil
			}
			if !mcpStopForce {
				fmt.Printf("PID %d is alive but not responding on http://127.0.0.1:%d%s.\n", state.PID, state.Port, mcpHealthPath)
				fmt.Println("It may be starting up, wedged, or a reused PID. To stop it anyway: ffc mcp stop --force")
				return nil
			}
			fmt.Printf("--force: stopping unconfirmed PID %d\n", state.PID)
		}

		proc, err := os.FindProcess(state.PID)
		if err != nil {
			return fmt.Errorf("finding process: %w", err)
		}
		if err := terminateProcess(proc); err != nil {
			return fmt.Errorf("stopping PID %d: %w", state.PID, err)
		}

		// Wait briefly for it to drain and exit before removing state (L14).
		for i := 0; i < 20; i++ {
			if _, ok := mcpHealth(state.Port, 200*time.Millisecond); !ok {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		removeMCPState()
		fmt.Printf("Stopped MCP server (PID %d)\n", state.PID)
		return nil
	},
}

func init() {
	mcpStopCmd.Flags().BoolVar(&mcpStopForce, "force", false, "Stop the recorded PID even if the server can't be health-confirmed")
	mcpCmd.AddCommand(mcpStatusCmd)
	mcpCmd.AddCommand(mcpStopCmd)
}
