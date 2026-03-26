package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-resty/resty/v2"
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

func updateCheckPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "ffc", ".update_check.json")
}

// runUpdateCheck reads the cached update state file and prints a one-line
// notice to stderr if a newer version is available. If the cache is stale
// (or missing) it starts a background goroutine to refresh it; Execute()
// waits for that goroutine before the process exits.
func runUpdateCheck() {
	path := updateCheckPath()
	if path == "" {
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		// State file missing — start background fetch; no notification yet.
		startBackgroundFetch(path)
		return
	}

	var state updateCheckState
	if err := json.Unmarshal(data, &state); err != nil {
		startBackgroundFetch(path)
		return
	}

	// Notify if the cached latest is newer than the running binary.
	cur := version.Version
	if cur != "dev" && cur != "" && newerThan(cur, state.Latest) {
		fmt.Fprintf(os.Stderr, "Update available: %s → %s  (run: ffc update)\n", cur, state.Latest)
	}

	// Refresh in background if the cache is older than the check interval.
	if time.Since(state.CheckedAt) > updateCheckInterval {
		startBackgroundFetch(path)
	}
}

func startBackgroundFetch(path string) {
	updateCheckDone = make(chan struct{})
	go func() {
		defer close(updateCheckDone)
		fetchAndStoreLatestRelease(path)
	}()
}

// fetchAndStoreLatestRelease queries the GitHub releases API and writes the
// result to path. Always called inside a goroutine via startBackgroundFetch.
func fetchAndStoreLatestRelease(path string) {
	var release githubRelease
	resp, err := resty.New().R().
		SetResult(&release).
		SetHeader("Accept", "application/vnd.github+json").
		Get(githubReleasesAPI)
	if err != nil || resp.StatusCode() != 200 || release.TagName == "" {
		return
	}

	state := updateCheckState{
		CheckedAt: time.Now().UTC(),
		Latest:    release.TagName,
	}
	out, err := json.Marshal(state)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	_ = os.WriteFile(path, out, 0644)
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

func init() {
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		// Skip the check when the user is already running `ffc update` or `ffc mcp`
		// (stderr output from the update notice would corrupt the MCP JSON-RPC stream).
		if cmd.Name() != "update" && cmd.Name() != "mcp" {
			runUpdateCheck()
		}
		return nil
	}
}
