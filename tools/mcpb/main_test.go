package main

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// layout builds a fake dist with a darwin_all binary and the given windows dirs.
func layout(t *testing.T, winDirs ...string) (dist, darwin string) {
	t.Helper()
	dist = filepath.Join(t.TempDir(), "dist")
	darwin = filepath.Join(dist, "ffc_darwin_all", "ffc")
	put(t, darwin, "MACHO")
	for _, d := range winDirs {
		put(t, filepath.Join(dist, d, "ffc.exe"), "PE-"+d)
	}
	icon := filepath.Join(t.TempDir(), "icon.png")
	put(t, icon, "\x89PNG\r\n\x1a\nxxxx")
	old := iconPath
	iconPath = icon
	t.Cleanup(func() { iconPath = old })
	return dist, darwin
}

func put(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunWritesBundle(t *testing.T) {
	dist, darwin := layout(t, "ffc_windows_amd64_v1", "ffc_windows_arm64_v8.0")
	if err := run([]string{darwin, "v1.2.3"}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(filepath.Join(dist, "ffc_1.2.3.mcpb"))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	var names []string
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		names = append(names, f.Name)
		files[f.Name] = f
	}
	want := []string{"manifest.json", "icon.png", "server/ffc", "server/ffc.exe"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	for _, n := range []string{"server/ffc", "server/ffc.exe"} {
		if m := files[n].Mode().Perm(); m != 0o755 {
			t.Errorf("%s mode = %o", n, m)
		}
	}
	read := func(n string) string {
		rc, err := files[n].Open()
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		return string(b)
	}
	if got := read("server/ffc.exe"); got != "PE-ffc_windows_amd64_v1" {
		t.Errorf("exe = %q", got)
	}
	if got := read("server/ffc"); got != "MACHO" {
		t.Errorf("ffc = %q", got)
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(read("manifest.json")), &m); err != nil {
		t.Fatal(err)
	}
	if m["version"] != "1.2.3" || m["manifest_version"] != "0.3" || m["name"] != "ffc" {
		t.Errorf("manifest = %v", m)
	}
	srv := m["server"].(map[string]any)
	if srv["type"] != "binary" || srv["entry_point"] != "server/ffc" {
		t.Errorf("server = %v", srv)
	}
	cfg := srv["mcp_config"].(map[string]any)
	ov := cfg["platform_overrides"].(map[string]any)["win32"].(map[string]any)
	if ov["command"] != "${__dirname}/server/ffc.exe" {
		t.Errorf("win32 override = %v", ov)
	}
	uc := m["user_config"].(map[string]any)
	for _, k := range []string{"site", "config", "read_only"} {
		o := uc[k].(map[string]any)
		if _, ok := o["default"]; !ok {
			t.Errorf("user_config.%s has no default", k)
		}
		if o["required"] != false {
			t.Errorf("user_config.%s required", k)
		}
	}
	if m["tools_generated"] != true || len(m["tools"].([]any)) == 0 {
		t.Errorf("tools = %v", m["tools"])
	}
}

func TestDeterministic(t *testing.T) {
	dist, darwin := layout(t, "ffc_windows_amd64_v1")
	out := filepath.Join(dist, "ffc_1.0.0.mcpb")
	if err := run([]string{darwin, "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(out)
	if err := run([]string{darwin, "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	if string(a) != string(b) {
		t.Error("bundle differs between runs")
	}
}

func TestWindowsBinaryMissingOrAmbiguous(t *testing.T) {
	_, darwin := layout(t)
	if err := run([]string{darwin, "1.0.0"}); err == nil {
		t.Error("no windows binary: want error")
	}
	_, darwin = layout(t, "ffc_windows_amd64_v1", "ffc_windows_amd64_v3")
	if err := run([]string{darwin, "1.0.0"}); err == nil {
		t.Error("two windows binaries: want error")
	}
}

func TestBadArgsAndIcon(t *testing.T) {
	if err := run(nil); err == nil {
		t.Error("no args: want error")
	}
	_, darwin := layout(t, "ffc_windows_amd64_v1")
	put(t, iconPath, "not a png")
	if err := run([]string{darwin, "1.0.0"}); err == nil {
		t.Error("non-PNG icon: want error")
	}
}
