package cmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"testing"
)

func TestIsDevBuild(t *testing.T) {
	tests := map[string]bool{
		"v0.1.0":            false,
		"0.1.0":             false,
		"v1.20.3":           false,
		"dev":               true,
		"":                  true,
		"abc1234":           true, // git describe --always
		"v0.1.0-3-gdeadbee": true, // git describe with commits ahead
		"v0.1.0-dirty":      true,
		"v0.1.0-rc1":        true, // a pre-release is not a clean release tag
	}
	for v, want := range tests {
		if got := isDevBuild(v); got != want {
			t.Errorf("isDevBuild(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestNewerThan(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"v1.0.0", "v1.0.1", true},
		{"v1.0.0", "v1.1.0", true},
		{"v1.0.0", "v2.0.0", true},
		{"v1.2.0", "v1.2.0", false},
		{"v2.0.0", "v1.9.9", false},
		{"1.0.0", "1.0.1", true}, // v-prefix optional
		// Pre-release handling (L30): the final release supersedes its rc…
		{"v1.6.0-rc1", "v1.6.0", true},
		// …but we never offer a pre-release as an update over the final.
		{"v1.6.0", "v1.6.0-rc1", false},
	}
	for _, tt := range tests {
		if got := newerThan(tt.current, tt.latest); got != tt.want {
			t.Errorf("newerThan(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestClassifyUpdate(t *testing.T) {
	tests := []struct {
		current, latest string
		want            updateKind
	}{
		{"v1.5.0", "v1.5.0", kindUpToDate},
		{"v1.5.0", "v1.6.0", kindUpdate},
		{"v1.6.0", "v1.5.0", kindDowngrade},
		{"v1.6.0-rc1", "v1.5.0", kindDowngrade}, // rc ahead of latest stable
		{"v1.6.0-rc1", "v1.6.0", kindUpdate},    // final supersedes its rc
		{"v1.5.0-rc1", "v1.6.0", kindUpdate},
		{"v1.2.0-3-gabc1234", "v1.2.0", kindDowngrade}, // ahead of the tag
		{"v1.2.0-3-gabc1234", "v1.3.0", kindUpdate},
		{"v1.2.0-dirty", "v1.2.0", kindDowngrade},
		{"abc1234", "v1.2.0", kindDev},
		{"dev", "v1.2.0", kindDev},
		{"", "v1.2.0", kindDev},
	}
	for _, tt := range tests {
		if got := classifyUpdate(tt.current, tt.latest); got != tt.want {
			t.Errorf("classifyUpdate(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func buildTarGz(t *testing.T, entries []tar.Header, data map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, h := range entries {
		h := h
		body := data[h.Name]
		h.Size = int64(len(body))
		if h.Typeflag != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			tw.Write(body)
		}
	}
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

func TestExtractFromTarGz(t *testing.T) {
	good := buildTarGz(t,
		[]tar.Header{
			{Name: "ffc", Typeflag: tar.TypeSymlink, Linkname: "x"}, // must be skipped
			{Name: "pkg/ffc", Typeflag: tar.TypeReg, Mode: 0o755},
		},
		map[string][]byte{"pkg/ffc": []byte("BIN")})
	got, err := extractFromTarGz(good, "ffc")
	if err != nil || string(got) != "BIN" {
		t.Fatalf("got %q, %v", got, err)
	}

	onlyLink := buildTarGz(t, []tar.Header{{Name: "ffc", Typeflag: tar.TypeSymlink, Linkname: "x"}}, nil)
	if _, err := extractFromTarGz(onlyLink, "ffc"); err == nil {
		t.Error("symlink entry must not be accepted")
	}

	empty := buildTarGz(t, []tar.Header{{Name: "ffc", Typeflag: tar.TypeReg, Mode: 0o755}}, nil)
	if _, err := extractFromTarGz(empty, "ffc"); err == nil {
		t.Error("empty entry must be rejected")
	}
}

func TestExtractFromZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	zw.Create("dir/") // directory entry
	w, _ := zw.Create("ffc.exe")
	w.Write([]byte("EXE"))
	zw.Close()
	got, err := extractFromZip(buf.Bytes(), "ffc.exe")
	if err != nil || string(got) != "EXE" {
		t.Fatalf("got %q, %v", got, err)
	}

	buf.Reset()
	zw = zip.NewWriter(&buf)
	zw.Create("ffc.exe") // zero bytes
	zw.Close()
	if _, err := extractFromZip(buf.Bytes(), "ffc.exe"); err == nil {
		t.Error("empty entry must be rejected")
	}
}
