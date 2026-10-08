// Command mcpb packs the ffc MCP Bundle (.mcpb), the one-click extension for
// Claude Desktop. It is not shipped; GoReleaser runs it as a post hook of the
// universal_binaries entry, which runs after every build, so the Windows
// binary already exists.
//
//	go run ./tools/mcpb <darwin-universal-binary> <version>
//
// It writes dist/ffc_<version>.mcpb next to the darwin_all directory: a zip
// with manifest.json, icon.png, server/ffc (macOS universal) and
// server/ffc.exe (Windows amd64, found by globbing dist). The zip is
// deterministic: fixed entry order and times (SOURCE_DATE_EPOCH, else
// 1980-01-01 UTC).
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// iconPath is relative to the repository root, where GoReleaser runs hooks.
var iconPath = filepath.Join("desktop", "build", "appicon.png")

var pngMagic = []byte("\x89PNG\r\n\x1a\n")

const repoURL = "https://github.com/nasroykh/foxmayn_frappe_cli"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "mcpb:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: mcpb <darwin-universal-binary> <version>")
	}
	darwin, version := args[0], strings.TrimPrefix(args[1], "v")
	if version == "" {
		return errors.New("empty version")
	}
	// <dist>/ffc_darwin_all/ffc
	dist := filepath.Dir(filepath.Dir(darwin))

	matches, err := filepath.Glob(filepath.Join(dist, "*windows_amd64*", "ffc.exe"))
	if err != nil {
		return err
	}
	if len(matches) != 1 {
		return fmt.Errorf("want exactly one windows amd64 ffc.exe in %s, found %d: %v", dist, len(matches), matches)
	}

	mf, err := manifestJSON(version)
	if err != nil {
		return err
	}
	icon, err := os.ReadFile(iconPath)
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(icon, pngMagic) {
		return fmt.Errorf("%s is not a PNG", iconPath)
	}
	mac, err := os.ReadFile(darwin)
	if err != nil {
		return err
	}
	win, err := os.ReadFile(matches[0])
	if err != nil {
		return err
	}

	mod, err := modTime()
	if err != nil {
		return err
	}
	out := filepath.Join(dist, "ffc_"+version+".mcpb")
	return writeZip(out, mod, []entry{
		{"manifest.json", mf, 0o644},
		{"icon.png", icon, 0o644},
		{"server/ffc", mac, 0o755},
		{"server/ffc.exe", win, 0o755},
	})
}

func modTime() (time.Time, error) {
	if s := os.Getenv("SOURCE_DATE_EPOCH"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("SOURCE_DATE_EPOCH: %w", err)
		}
		return time.Unix(n, 0).UTC(), nil
	}
	return time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC), nil
}

type entry struct {
	name string
	data []byte
	mode os.FileMode
}

func writeZip(path string, mod time.Time, entries []entry) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcpb-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	zw := zip.NewWriter(tmp)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate, Modified: mod}
		h.SetMode(e.mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		if _, err := w.Write(e.data); err != nil {
			return err
		}
	}
	if err = zw.Close(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// The manifest types keep field order fixed, so the output is stable.
type manifest struct {
	ManifestVersion string        `json:"manifest_version"`
	Name            string        `json:"name"`
	DisplayName     string        `json:"display_name"`
	Version         string        `json:"version"`
	Description     string        `json:"description"`
	LongDescription string        `json:"long_description"`
	Author          author        `json:"author"`
	Repository      repository    `json:"repository"`
	Homepage        string        `json:"homepage"`
	Documentation   string        `json:"documentation"`
	License         string        `json:"license"`
	Keywords        []string      `json:"keywords"`
	Icon            string        `json:"icon"`
	Compatibility   compatibility `json:"compatibility"`
	Server          server        `json:"server"`
	ToolsGenerated  bool          `json:"tools_generated"`
	Tools           []tool        `json:"tools"`
	UserConfig      userConfig    `json:"user_config"`
}

type author struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type repository struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type compatibility struct {
	Platforms []string `json:"platforms"`
}

type server struct {
	Type       string    `json:"type"`
	EntryPoint string    `json:"entry_point"`
	MCPConfig  mcpConfig `json:"mcp_config"`
}

type commandOverride struct {
	Command string `json:"command"`
}

type mcpConfig struct {
	Command           string                     `json:"command"`
	Args              []string                   `json:"args"`
	Env               map[string]string          `json:"env"`
	PlatformOverrides map[string]commandOverride `json:"platform_overrides"`
}

type tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type userConfig struct {
	Site     stringOpt `json:"site"`
	Config   stringOpt `json:"config"`
	ReadOnly boolOpt   `json:"read_only"`
}

type stringOpt struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Default     string `json:"default"`
}

type boolOpt struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Default     bool   `json:"default"`
}

func manifestJSON(version string) ([]byte, error) {
	m := manifest{
		ManifestVersion: "0.3",
		Name:            "ffc",
		DisplayName:     "Foxmayn Frappe CLI",
		Version:         version,
		Description:     "Read and change documents, reports and workflows on your Frappe and ERPNext sites.",
		LongDescription: "Connects Claude to your Frappe and ERPNext sites through ffc. Set up your sites with the ffc command-line tool first (ffc init); this extension uses the same sites and credentials. See " + repoURL + "/blob/main/docs/mcp/bundle.md for details.",
		Author:          author{Name: "Foxmayn", URL: "https://github.com/nasroykh"},
		Repository:      repository{Type: "git", URL: repoURL},
		Homepage:        repoURL,
		Documentation:   repoURL + "/blob/main/docs/mcp/bundle.md",
		License:         "MIT",
		Keywords:        []string{"frappe", "erpnext", "erp"},
		Icon:            "icon.png",
		Compatibility:   compatibility{Platforms: []string{"darwin", "win32"}},
		Server: server{
			Type:       "binary",
			EntryPoint: "server/ffc",
			MCPConfig: mcpConfig{
				Command: "${__dirname}/server/ffc",
				Args:    []string{"mcp", "--read-only=${user_config.read_only}"},
				Env: map[string]string{
					"FFC_SITE":   "${user_config.site}",
					"FFC_CONFIG": "${user_config.config}",
				},
				PlatformOverrides: map[string]commandOverride{
					"win32": {Command: "${__dirname}/server/ffc.exe"},
				},
			},
		},
		ToolsGenerated: true,
		Tools: []tool{
			{"list_docs", "List documents from a DocType with filters, fields, ordering and paging."},
			{"get_doc", "Retrieve a single document by DocType and name."},
			{"run_report", "Run a query report and return its columns and rows."},
			{"create_doc", "Create a document in a DocType."},
			{"update_doc", "Update fields of an existing document."},
		},
		UserConfig: userConfig{
			Site:     stringOpt{"string", "Site", "Site name from your ffc config (empty: the default site)", false, ""},
			Config:   stringOpt{"file", "Config file", "ffc config file (empty: the default ~/.config/ffc/config.yaml)", false, ""},
			ReadOnly: boolOpt{"boolean", "Read only", "Expose only read tools", false, false},
		},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
