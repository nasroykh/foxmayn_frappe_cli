// Package attach reads the files a person attaches to a chat message: it
// tells their type from their content (the extension must agree), checks the
// size limits, and turns them into what the model gets: text for text, CSV
// and JSON files, CSV per sheet for XLSX workbooks, the bytes for PNG, JPEG,
// WebP and GIF images. Everything else, PDF included, is refused.
package attach

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // DecodeConfig of attached images
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxFileBytes is the largest file that can be attached.
	MaxFileBytes = 10 << 20
	// MaxPastedBytes is the largest pasted image, decoded.
	MaxPastedBytes = 5 << 20
	// MaxPerMessage is how many attachments one message may carry.
	MaxPerMessage = 5
	// MaxTextChars is the most characters the text of one attachment may
	// have, after cleaning (XLSX: all its sheets as CSV).
	MaxTextChars = 200_000
	// MaxMessageChars is the most attachment text one message may carry.
	MaxMessageChars = 400_000
	// MaxImageSide and MaxImagePixels cap the size of an image.
	MaxImageSide   = 8000
	MaxImagePixels = 40_000_000

	// The XLSX caps. A workbook is unzipped in memory only up to
	// xlsxUnzipLimit bytes in all (a zip bomb is refused before it is
	// unzipped, from the sizes its entries declare; archive/zip refuses an
	// entry that holds more than it declares).
	xlsxUnzipLimit = 64 << 20
	xlsxMaxSheets  = 50
	xlsxMaxRows    = 50_000
	xlsxMaxCells   = 500_000
	xlsxMaxColumns = 1_000
)

// Kinds of attachment.
const (
	KindText  = "text"
	KindImage = "image"
)

// File is an attachment read and checked.
type File struct {
	Name string
	// Mime is the type found from the content: text/plain, text/csv,
	// application/json, the XLSX type, or one of the image types.
	Mime string
	Kind string
	// Size is the byte size of the file as given.
	Size   int64
	SHA256 string
	// Text is what the model gets for a text attachment.
	Text string
	// Data is the bytes of an image attachment.
	Data []byte
	// Width and Height are an image's size in pixels.
	Width, Height int
}

// Error is a refusal the person can act on; Error() is shown as is.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func refuse(format string, args ...any) error {
	return &Error{Msg: fmt.Sprintf(format, args...)}
}

// Mime types.
const (
	mimeText = "text/plain"
	mimeCSV  = "text/csv"
	mimeJSON = "application/json"
	mimeXLSX = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	mimePNG  = "image/png"
	mimeJPEG = "image/jpeg"
	mimeWebP = "image/webp"
	mimeGIF  = "image/gif"
)

// textExts are the extensions read as text, with the type they are given.
var textExts = map[string]string{
	".txt": mimeText, ".text": mimeText, ".md": mimeText, ".log": mimeText,
	".csv": mimeCSV, ".tsv": mimeCSV,
	".json": mimeJSON,
}

// imageExts are the extensions of each image type.
var imageExts = map[string][]string{
	mimePNG:  {".png"},
	mimeJPEG: {".jpg", ".jpeg"},
	mimeWebP: {".webp"},
	mimeGIF:  {".gif"},
}

// IsImageMime reports whether mime is one of the image types attach takes.
func IsImageMime(mime string) bool {
	_, ok := imageExts[mime]
	return ok
}

// ReadFile reads the file at path and checks it (see Parse). Only a regular
// file of at most MaxFileBytes is read.
func ReadFile(path string) (File, error) {
	name := filepath.Base(path)
	f, err := os.Open(path)
	if err != nil {
		return File{}, refuse("%s could not be opened.", name)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return File{}, refuse("%s is not a file that can be attached.", name)
	}
	if st.Size() > MaxFileBytes {
		return File{}, refuse("%s is larger than 10 MB.", name)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return File{}, refuse("%s could not be read.", name)
	}
	if len(data) > MaxFileBytes {
		return File{}, refuse("%s is larger than 10 MB.", name)
	}
	return Parse(name, data)
}

// Parse checks a file given by name and content. The type comes from the
// content; the extension must name the same type, or the file is refused.
func Parse(name string, data []byte) (File, error) {
	name = cleanName(name)
	if len(data) > MaxFileBytes {
		return File{}, refuse("%s is larger than 10 MB.", name)
	}
	if len(data) == 0 {
		return File{}, refuse("%s is empty.", name)
	}
	ext := strings.ToLower(filepath.Ext(name))
	out := File{Name: name, Size: int64(len(data)), SHA256: sum(data)}
	switch sniffed := sniff(data); sniffed {
	case "pdf":
		return File{}, refuse("%s is a PDF. PDF files cannot be attached yet (planned for V1.x).", name)
	case "zip":
		if ext != ".xlsx" {
			return File{}, mismatch(name, ext)
		}
		text, err := xlsxText(name, data)
		if err != nil {
			return File{}, err
		}
		out.Mime, out.Kind, out.Text = mimeXLSX, KindText, text
		return out, nil
	case mimePNG, mimeJPEG, mimeWebP, mimeGIF:
		if !hasExt(imageExts[sniffed], ext) {
			return File{}, mismatch(name, ext)
		}
		w, h, err := imageSize(sniffed, data)
		if err != nil {
			return File{}, refuse("%s is not a valid image.", name)
		}
		if err := checkPixels(name, w, h); err != nil {
			return File{}, err
		}
		out.Mime, out.Kind, out.Data, out.Width, out.Height = sniffed, KindImage, data, w, h
		return out, nil
	case "binary":
		if _, ok := textExts[ext]; ok {
			return File{}, refuse("%s is not UTF-8 text.", name)
		}
		return File{}, refuse("%s: this type of file cannot be attached. Attach text, CSV, JSON, XLSX, PNG, JPEG, WebP or GIF files.", name)
	default: // text
		mime, ok := textExts[ext]
		if !ok {
			if ext == ".xlsx" || hasAnyImageExt(ext) || ext == ".pdf" {
				return File{}, mismatch(name, ext)
			}
			return File{}, refuse("%s: this type of file cannot be attached. Attach text, CSV, JSON, XLSX, PNG, JPEG, WebP or GIF files.", name)
		}
		text, err := cleanText(name, data)
		if err != nil {
			return File{}, err
		}
		if mime == mimeJSON && !json.Valid([]byte(text)) {
			return File{}, refuse("%s is not valid JSON.", name)
		}
		out.Mime, out.Kind, out.Text = mime, KindText, text
		return out, nil
	}
}

// ParseImage checks a pasted image: one of the image types, at most
// MaxPastedBytes. It gets a name from its type.
func ParseImage(data []byte) (File, error) {
	if len(data) > MaxPastedBytes {
		return File{}, refuse("The pasted image is larger than 5 MB.")
	}
	mime := sniff(data)
	exts, ok := imageExts[mime]
	if !ok {
		return File{}, refuse("Only PNG, JPEG, WebP and GIF images can be pasted.")
	}
	return Parse("pasted-image"+exts[0], data)
}

// sniff names the type of data from its first bytes: an image type, "pdf",
// "zip", "binary", or "" for text (valid UTF-8 without NUL bytes, a UTF-8
// byte order mark allowed).
func sniff(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return mimePNG
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return mimeJPEG
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return mimeGIF
	case len(data) >= 12 && bytes.HasPrefix(data, []byte("RIFF")) && string(data[8:12]) == "WEBP":
		return mimeWebP
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return "pdf"
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		return "zip"
	}
	body := bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	if bytes.IndexByte(body, 0) >= 0 || !utf8.Valid(body) {
		return "binary"
	}
	return ""
}

// cleanText strips a byte order mark, turns CR LF and lone CR into LF, and
// drops every control character but tab and newline.
func cleanText(name string, data []byte) (string, error) {
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	if !utf8.Valid(data) {
		return "", refuse("%s is not UTF-8 text.", name)
	}
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = stripControls(s)
	if utf8.RuneCountInString(s) > MaxTextChars {
		return "", refuse("%s has more than 200 000 characters of text.", name)
	}
	if strings.TrimSpace(s) == "" {
		return "", refuse("%s has no text.", name)
	}
	return s, nil
}

func stripControls(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, s)
}

// imageSize reads an image's width and height without decoding it.
func imageSize(mime string, data []byte) (int, int, error) {
	if mime == mimeWebP {
		return webpSize(data)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

// webpSize reads the canvas size of a WebP file from its first chunk
// (VP8X, VP8L or VP8), as the WebP container format describes it.
func webpSize(b []byte) (int, int, error) {
	bad := errors.New("not a valid WebP image")
	if len(b) < 30 {
		return 0, 0, bad
	}
	switch string(b[12:16]) {
	case "VP8X":
		w := 1 + (int(b[24]) | int(b[25])<<8 | int(b[26])<<16)
		h := 1 + (int(b[27]) | int(b[28])<<8 | int(b[29])<<16)
		return w, h, nil
	case "VP8L":
		if b[20] != 0x2F {
			return 0, 0, bad
		}
		bits := uint32(b[21]) | uint32(b[22])<<8 | uint32(b[23])<<16 | uint32(b[24])<<24
		return int(bits&0x3FFF) + 1, int(bits>>14&0x3FFF) + 1, nil
	case "VP8 ":
		if b[23] != 0x9D || b[24] != 0x01 || b[25] != 0x2A {
			return 0, 0, bad
		}
		w := int(uint16(b[26])|uint16(b[27])<<8) & 0x3FFF
		h := int(uint16(b[28])|uint16(b[29])<<8) & 0x3FFF
		return w, h, nil
	}
	return 0, 0, bad
}

func checkPixels(name string, w, h int) error {
	if w <= 0 || h <= 0 {
		return refuse("%s is not a valid image.", name)
	}
	if w > MaxImageSide || h > MaxImageSide || int64(w)*int64(h) > MaxImagePixels {
		return refuse("%s is too large (%d x %d pixels). Images can be at most %d pixels on a side.", name, w, h, MaxImageSide)
	}
	return nil
}

func mismatch(name, ext string) error {
	if ext == "" {
		return refuse("%s has no extension; its type cannot be checked.", name)
	}
	return refuse("The content of %s does not match its %s extension.", name, ext)
}

func hasExt(list []string, ext string) bool {
	for _, e := range list {
		if e == ext {
			return true
		}
	}
	return false
}

func hasAnyImageExt(ext string) bool {
	for _, l := range imageExts {
		if hasExt(l, ext) {
			return true
		}
	}
	return false
}

// cleanName keeps the base name, without control characters, at most 200
// characters.
func cleanName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSpace(stripControls(name))
	if r := []rune(name); len(r) > 200 {
		name = string(r[:200])
	}
	if name == "" || name == "." || name == "/" {
		name = "attachment"
	}
	return name
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
