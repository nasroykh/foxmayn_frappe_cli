package attach

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func refusal(t *testing.T, err error, want string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %v, want a refusal containing %q", err, want)
	}
	if !strings.Contains(e.Msg, want) {
		t.Fatalf("refusal = %q, want it to contain %q", e.Msg, want)
	}
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestParseText(t *testing.T) {
	f, err := Parse("notes.txt", []byte("\xEF\xBB\xBFline one\r\nline\x00 two\x1b[31m\rthree\tend\x7f"))
	if err == nil {
		t.Fatalf("a NUL byte should make the file binary, got %+v", f)
	}
	refusal(t, err, "is not UTF-8 text")

	f, err = Parse("notes.txt", []byte("\xEF\xBB\xBFline one\r\nline two\x1b[31m\rthree\tend\x7f\u0085"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "line one\nline two[31m\nthree\tend"; f.Text != want {
		t.Fatalf("text = %q, want %q", f.Text, want)
	}
	if f.Kind != KindText || f.Mime != "text/plain" || f.Data != nil || len(f.SHA256) != 64 {
		t.Fatalf("file = %+v", f)
	}
}

func TestParseCSVAndJSON(t *testing.T) {
	f, err := Parse("data.csv", []byte("a,b\n1,2\n"))
	if err != nil || f.Mime != "text/csv" || f.Text != "a,b\n1,2\n" {
		t.Fatalf("csv: %+v, %v", f, err)
	}
	f, err = Parse("data.json", []byte(`{"a": [1, 2]}`))
	if err != nil || f.Mime != "application/json" {
		t.Fatalf("json: %+v, %v", f, err)
	}
	_, err = Parse("data.json", []byte(`{"a": `))
	refusal(t, err, "not valid JSON")
}

func TestParseLimits(t *testing.T) {
	_, err := Parse("big.txt", bytes.Repeat([]byte("a"), MaxFileBytes+1))
	refusal(t, err, "larger than 10 MB")

	_, err = Parse("long.txt", []byte(strings.Repeat("é", MaxTextChars+1)))
	refusal(t, err, "more than 200 000 characters")
	if _, err := Parse("long.txt", []byte(strings.Repeat("é", MaxTextChars))); err != nil {
		t.Fatalf("exactly the limit: %v", err)
	}

	_, err = Parse("empty.txt", nil)
	refusal(t, err, "is empty")

	dir := t.TempDir()
	p := filepath.Join(dir, "big.csv")
	if err := os.WriteFile(p, bytes.Repeat([]byte("a"), MaxFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = ReadFile(p)
	refusal(t, err, "larger than 10 MB")
	_, err = ReadFile(dir)
	refusal(t, err, "not a regular file")
}

func TestParseSniffMismatch(t *testing.T) {
	img := pngBytes(t, 2, 2)
	for _, c := range []struct {
		name string
		data []byte
		want string
	}{
		{"photo.jpg", img, "does not match its .jpg extension"},
		{"photo.txt", img, "does not match its .txt extension"},
		{"photo", img, "has no extension"},
		{"fake.png", []byte("just text"), "does not match its .png extension"},
		{"fake.xlsx", []byte("a,b\n"), "does not match its .xlsx extension"},
		{"archive.txt", []byte("PK\x03\x04rest"), "does not match its .txt extension"},
		{"program.exe", []byte{0x4d, 0x5a, 0x90, 0x00, 0xff}, "cannot be attached"},
		{"script.sh", []byte("echo hi"), "cannot be attached"},
		{"report.txt", []byte("%PDF-1.7 ..."), "does not match its .txt extension"},
		{"report.docx", []byte("%PDF-1.7 ..."), "does not match its .docx extension"},
		{"letter.doc", []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1, 0x00}, "old Office format. Save it as .docx"},
		{"secret.docx", []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1, 0x00}, "password protected or not a valid Office file"},
	} {
		_, err := Parse(c.name, c.data)
		refusal(t, err, c.want)
	}
}

func TestParseImages(t *testing.T) {
	f, err := Parse("dot.png", pngBytes(t, 3, 2))
	if err != nil || f.Kind != KindImage || f.Mime != "image/png" || f.Width != 3 || f.Height != 2 || len(f.Data) == 0 || f.Text != "" {
		t.Fatalf("png: %+v, %v", f, err)
	}
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	if f, err := Parse("a.JPEG", jb.Bytes()); err != nil || f.Mime != "image/jpeg" {
		t.Fatalf("jpeg: %+v, %v", f, err)
	}
	var gb bytes.Buffer
	if err := gif.Encode(&gb, image.NewPaletted(image.Rect(0, 0, 5, 1), []color.Color{color.Black}), nil); err != nil {
		t.Fatal(err)
	}
	if f, err := Parse("a.gif", gb.Bytes()); err != nil || f.Mime != "image/gif" || f.Width != 5 {
		t.Fatalf("gif: %+v, %v", f, err)
	}
	// A lossless WebP header: 'RIFF' size 'WEBP' 'VP8L' size, signature 0x2F,
	// then 14 bits width-1 and 14 bits height-1.
	w, h := uint32(640-1), uint32(480-1)
	bits := w | h<<14
	webp := []byte("RIFF\x00\x00\x00\x00WEBPVP8L\x00\x00\x00\x00\x2F")
	webp = append(webp, byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24))
	webp = append(webp, make([]byte, 16)...)
	if f, err := Parse("a.webp", webp); err != nil || f.Mime != "image/webp" || f.Width != 640 || f.Height != 480 {
		t.Fatalf("webp: %+v, %v", f, err)
	}
	// VP8X: 24-bit canvas sizes minus one.
	x := []byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00\x00\x00\x00")
	x = append(x, 0xFF, 0x00, 0x00, 0x0F, 0x27, 0x00) // 256 x 10000
	if _, err := Parse("big.webp", x); err == nil {
		t.Fatal("a 10000 pixel tall WebP should be refused")
	} else {
		refusal(t, err, "too large (256 x 10000 pixels)")
	}

	_, err = Parse("broken.png", []byte("\x89PNG\r\n\x1a\nnot really"))
	refusal(t, err, "not a valid image")
	_, err = Parse("huge.png", pngBytes(t, MaxImageSide+1, 1))
	refusal(t, err, "too large")
}

func TestParseImagePasted(t *testing.T) {
	f, err := ParseImage(pngBytes(t, 2, 2))
	if err != nil || f.Name != "pasted-image.png" || f.Kind != KindImage {
		t.Fatalf("pasted: %+v, %v", f, err)
	}
	_, err = ParseImage([]byte("plain text"))
	refusal(t, err, "Only PNG, JPEG, WebP and GIF")
	big := append(pngBytes(t, 2, 2), make([]byte, MaxPastedBytes)...)
	_, err = ParseImage(big)
	refusal(t, err, "larger than 5 MB")
}

func xlsxBytes(t *testing.T, fill func(f *excelize.File)) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	fill(f)
	b, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestParseXLSX(t *testing.T) {
	data := xlsxBytes(t, func(f *excelize.File) {
		_ = f.SetSheetRow("Sheet1", "A1", &[]any{"name", "qty", "note"})
		_ = f.SetSheetRow("Sheet1", "A2", &[]any{"Bolt", 12, "has, comma"})
		if _, err := f.NewSheet("Prices"); err != nil {
			t.Fatal(err)
		}
		_ = f.SetSheetRow("Prices", "A1", &[]any{"item", "price"})
		_ = f.SetSheetRow("Prices", "A2", &[]any{"Bolt", 1.5})
	})
	f, err := Parse("stock.xlsx", data)
	if err != nil {
		t.Fatal(err)
	}
	want := "Sheet: Sheet1\nname,qty,note\nBolt,12,\"has, comma\"\n\nSheet: Prices\nitem,price\nBolt,1.5\n"
	if f.Text != want || f.Kind != KindText || !strings.Contains(f.Mime, "spreadsheetml") {
		t.Fatalf("xlsx text = %q (%s), want %q", f.Text, f.Mime, want)
	}
	_, err = Parse("stock.zip", data)
	refusal(t, err, "does not match its .zip extension")
}

func TestParseXLSXRowCap(t *testing.T) {
	data := xlsxBytes(t, func(f *excelize.File) {
		sw, err := f.NewStreamWriter("Sheet1")
		if err != nil {
			t.Fatal(err)
		}
		for r := 1; r <= xlsxMaxRows+1; r++ {
			cell, _ := excelize.CoordinatesToCellName(1, r)
			if err := sw.SetRow(cell, []any{1}); err != nil {
				t.Fatal(err)
			}
		}
		if err := sw.Flush(); err != nil {
			t.Fatal(err)
		}
	})
	_, err := Parse("rows.xlsx", data)
	refusal(t, err, "more than 50000 rows")
}

// TestParseXLSXZipBomb crafts a small file whose one entry unpacks to more
// than the unzip limit: refused from the declared size, before unpacking.
func TestParseXLSXZipBomb(t *testing.T) {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for i := 0; i < 65; i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if b.Len() > MaxFileBytes {
		t.Fatalf("the bomb is %d bytes, more than the file limit", b.Len())
	}
	_, err = Parse("bomb.xlsx", b.Bytes())
	refusal(t, err, "unpacks to more than 64 MB")
}

// TestParseXLSXLyingZip crafts an entry that declares a tiny size but holds
// 65 MB: archive/zip stops reading past the declared size.
func TestParseXLSXLyingZip(t *testing.T) {
	var raw bytes.Buffer
	fw, err := flate.NewWriter(&raw, flate.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for i := 0; i < 65; i++ {
		_, _ = fw.Write(chunk)
	}
	_ = fw.Close()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name: "xl/worksheets/sheet1.xml", Method: zip.Deflate,
		CompressedSize64: uint64(raw.Len()), UncompressedSize64: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = Parse("liar.xlsx", b.Bytes())
	refusal(t, err, "not a valid XLSX workbook")
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Users\me\report.csv`: "report.csv",
		"../../etc/passwd.txt":   "passwd.txt",
		"a\x00b\x1b.txt":         "ab.txt",
		"a\nb\rc\td.txt":         "a b c d.txt",
		"":                       "attachment",
	} {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadFileRefusals(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(good, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if f, err := ReadFile(good); err != nil || f.Text != "hello" {
		t.Fatalf("regular file = %+v, %v", f, err)
	}
	refusal(t, readErr(dir), "is not a regular file")
	refusal(t, readErr(filepath.Join(dir, "missing.txt")), "missing.txt could not be read.")
}

func readErr(path string) error {
	_, err := ReadFile(path)
	return err
}

// A UNC path is refused before it is opened (Windows only: elsewhere a
// leading double slash is an ordinary path).
func TestReadFileRefusesUNC(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("UNC volume names exist on Windows only")
	}
	for _, p := range []string{`\\host\share\a.txt`, `\\.\C:\a.txt`, `\\?\C:\a.txt`, `//host/share/a.txt`} {
		refusal(t, readErr(p), "network or device path")
	}
}
