package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/version"
	"github.com/spf13/cobra"
)

const updateCheckInterval = 24 * time.Hour

// updateCheckDone is set when a background release-fetch goroutine is in
// flight. Execute() waits on it (up to 2 s) so the goroutine can finish
// writing the state file before the process exits.
var updateCheckDone chan struct{}

type updateCheckState struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

// updateCheckPath returns ~/.config/ffc/.update_check.json — see the comment
// on mcpStateDir for why this isn't os.UserConfigDir().
func updateCheckPath() string {
	dir, err := config.DefaultConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, ".update_check.json")
}

// runUpdateCheck reads the cached update state file and prints a one-line
// notice to stderr if a newer version is available. If the cache is stale
// (or missing) it starts a background goroutine to refresh it; Execute()
// waits for that goroutine (at most 2 s) before the process exits.
func runUpdateCheck() {
	if os.Getenv("FFC_NO_UPDATE_CHECK") != "" {
		return
	}
	path := updateCheckPath()
	if path == "" {
		return
	}

	var state updateCheckState
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &state)
	}

	// Notify if the cached latest is newer than the running binary.
	cur := version.Version
	if state.Latest != "" && !isDevBuild(cur) && newerThan(cur, state.Latest) {
		fmt.Fprintf(os.Stderr, "Update available: %s → %s  (run: ffc update)\n", cur, state.Latest)
	}

	if time.Since(state.CheckedAt) > updateCheckInterval {
		// Record the attempt before fetching: a fetch that fails, is
		// rate-limited or is cut off by the 2 s exit wait must not make every
		// following command pay for another attempt.
		state.CheckedAt = time.Now().UTC()
		writeUpdateState(path, state)
		startBackgroundFetch(path, state)
	}
}

func startBackgroundFetch(path string, state updateCheckState) {
	updateCheckDone = make(chan struct{})
	go func() {
		defer close(updateCheckDone)
		var release githubRelease
		resp, err := client.NewHTTPClient(10*time.Second).R().
			SetResult(&release).
			SetHeader("Accept", "application/vnd.github+json").
			Get(githubReleasesAPI)
		if err != nil || resp.StatusCode() != 200 || release.TagName == "" {
			return
		}
		state.Latest = release.TagName
		writeUpdateState(path, state)
	}()
}

func writeUpdateState(path string, state updateCheckState) {
	if out, err := json.Marshal(state); err == nil {
		_ = config.WriteFileAtomic(path, out, 0o600)
	}
}

// waitForUpdateCheck blocks until any in-flight background fetch completes
// or 2 seconds have elapsed. Called from Execute() so the goroutine always
// gets a chance to write the state file before the process exits.
func waitForUpdateCheck() {
	if updateCheckDone == nil {
		return
	}
	select {
	case <-updateCheckDone:
	case <-time.After(2 * time.Second):
	}
}

// skipUpdateCheck reports commands that must not print the update notice or
// touch the network: `update` itself, the MCP server (long-running; its stdout
// is the JSON-RPC channel), and shell completion/help, which run on every TAB.
func skipUpdateCheck(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "update", "mcp", "completion", "help", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
			return true
		}
	}
	return false
}

func init() {
	// The only PersistentPreRunE in the tree — see CLAUDE.md. OAuth token
	// refresh is not done here: loadSite() refreshes lazily, only for the
	// commands that actually call a site.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if !skipUpdateCheck(cmd) {
			runUpdateCheck()
		}
		return nil
	}
}
