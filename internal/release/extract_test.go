package release

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"testing"
)

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
	got, err := ExtractFromTarGz(good, "ffc")
	if err != nil || string(got) != "BIN" {
		t.Fatalf("got %q, %v", got, err)
	}

	onlyLink := buildTarGz(t, []tar.Header{{Name: "ffc", Typeflag: tar.TypeSymlink, Linkname: "x"}}, nil)
	if _, err := ExtractFromTarGz(onlyLink, "ffc"); err == nil {
		t.Error("symlink entry must not be accepted")
	}

	empty := buildTarGz(t, []tar.Header{{Name: "ffc", Typeflag: tar.TypeReg, Mode: 0o755}}, nil)
	if _, err := ExtractFromTarGz(empty, "ffc"); err == nil {
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
	got, err := ExtractFromZip(buf.Bytes(), "ffc.exe")
	if err != nil || string(got) != "EXE" {
		t.Fatalf("got %q, %v", got, err)
	}

	buf.Reset()
	zw = zip.NewWriter(&buf)
	zw.Create("ffc.exe") // zero bytes
	zw.Close()
	if _, err := ExtractFromZip(buf.Bytes(), "ffc.exe"); err == nil {
		t.Error("empty entry must be rejected")
	}
}
