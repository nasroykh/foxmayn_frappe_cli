package attach

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"strings"
	"testing"
	"time"
)

const wordNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`

// docxOf builds a minimal DOCX with body as the content of w:body.
func docxOf(t *testing.T, body string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><w:document ` + wordNS + `><w:body>` + body + `</w:body></w:document>`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestParsePDF(t *testing.T) {
	data := []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n%%EOF\n")
	f, err := Parse("Facture 12.pdf", data)
	if err != nil || f.Kind != KindDocument || f.Mime != MimePDF || !bytes.Equal(f.Data, data) || f.Text != "" {
		t.Fatalf("pdf: %+v, %v", f, err)
	}
	_, err = Parse("report", data)
	refusal(t, err, "has no extension")
}

func TestParseDOCX(t *testing.T) {
	body := `<w:p><w:r><w:t>Invoice SINV-0001</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t xml:space="preserve">Total: </w:t><w:tab/><w:t>300 DZD</w:t></w:r><w:r><w:br/><w:t>Due Friday</w:t></w:r></w:p>` +
		`<w:p><w:del><w:r><w:delText>old text</w:delText></w:r></w:del><w:r><w:instrText>PAGE</w:instrText><w:t>kept</w:t></w:r></w:p>` +
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Item</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Qty</w:t></w:r></w:p></w:tc></w:tr>` +
		`<w:tr><w:tc><w:p><w:r><w:t>Paper &amp; ink</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>2</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` +
		`<w:p/><w:p/><w:p/><w:p><w:r><w:t>العربية</w:t></w:r></w:p>`
	f, err := Parse("facture.docx", docxOf(t, body))
	if err != nil {
		t.Fatal(err)
	}
	want := "Invoice SINV-0001\nTotal: \t300 DZD\nDue Friday\nkept\nItem\tQty\nPaper & ink\t2\n\nالعربية"
	if f.Kind != KindText || f.Mime != mimeDOCX || f.Text != want {
		t.Fatalf("docx text:\n%q\nwant\n%q", f.Text, want)
	}
}

func TestParseDOCXRefusals(t *testing.T) {
	refusal(t, parseErr("empty.docx", docxOf(t, `<w:p/>`)), "has no text")
	refusal(t, parseErr("bad.docx", []byte("PK\x03\x04garbage")), "not a valid DOCX document")

	// A workbook named .docx has no word/document.xml.
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, _ := zw.Create("xl/workbook.xml")
	_, _ = w.Write([]byte("<workbook/>"))
	_ = zw.Close()
	refusal(t, parseErr("sheet.docx", b.Bytes()), "not a valid DOCX document")

	// Unbalanced XML.
	refusal(t, parseErr("broken.docx", docxOf(t, `<w:p><w:r><w:t>x</w:r>`)), "not a valid DOCX document")

	// More than MaxTextChars of text is refused, not cut.
	long := strings.Repeat(`<w:p><w:r><w:t>`+strings.Repeat("a", 1000)+`</w:t></w:r></w:p>`, MaxTextChars/1000+1)
	refusal(t, parseErr("long.docx", docxOf(t, long)), "more than 200 000 characters")
}

func parseErr(name string, data []byte) error {
	_, err := Parse(name, data)
	return err
}

// A document part that declares more than 64 MB is refused before it is
// read; one that declares little but holds 65 MB stops at its declared size.
func TestParseDOCXZipBombs(t *testing.T) {
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, _ := zw.Create("word/document.xml")
	chunk := make([]byte, 1<<20)
	for i := 0; i < 65; i++ {
		_, _ = w.Write(chunk)
	}
	_ = zw.Close()
	refusal(t, parseErr("bomb.docx", b.Bytes()), "unpacks to more than 32 MB")

	var raw bytes.Buffer
	fw, _ := flate.NewWriter(&raw, flate.BestCompression)
	for i := 0; i < 65; i++ {
		_, _ = fw.Write(chunk)
	}
	_ = fw.Close()
	b.Reset()
	zw = zip.NewWriter(&b)
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name: "word/document.xml", Method: zip.Deflate,
		CompressedSize64: uint64(raw.Len()), UncompressedSize64: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(raw.Bytes())
	_ = zw.Close()
	refusal(t, parseErr("liar.docx", b.Bytes()), "not a valid DOCX document")
}

// Tab stops, cell tabs and breaks, text box fallbacks and hidden text.
func TestParseDOCXLayout(t *testing.T) {
	body := `<w:p><w:pPr><w:tabs><w:tab w:val="left" w:pos="720"/></w:tabs></w:pPr><w:r><w:t>Invoice</w:t></w:r></w:p>` +
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>A</w:t></w:r></w:p><w:p><w:r><w:tab/><w:t>B</w:t><w:br/><w:t>C</w:t></w:r></w:p></w:tc>` +
		`<w:tc><w:p><w:r><w:t>2</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` +
		`<w:p><w:r><mc:AlternateContent xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006">` +
		`<mc:Choice Requires="wps"><w:t>BOX</w:t></mc:Choice><mc:Fallback><w:t>BOX</w:t></mc:Fallback></mc:AlternateContent></w:r></w:p>` +
		`<w:p><w:r><w:rPr><w:vanish/></w:rPr><w:t>hidden instructions</w:t></w:r><w:r><w:t>shown</w:t></w:r></w:p>`
	f, err := Parse("layout.docx", docxOf(t, body))
	if err != nil {
		t.Fatal(err)
	}
	want := "Invoice\nA  B C\t2\nBOX\nshown"
	if f.Text != want {
		t.Fatalf("text:\n%q\nwant\n%q", f.Text, want)
	}
}

// Deep nesting and countless elements are refused quickly, without the
// memory a full walk would take.
func TestParseDOCXComplexity(t *testing.T) {
	deep := strings.Repeat("<a>", docxMaxDepth+1) + strings.Repeat("</a>", docxMaxDepth+1)
	refusal(t, parseErr("deep.docx", docxOf(t, deep)), "too complex")
	start := time.Now()
	many := strings.Repeat("<a/>", docxMaxTokens/2+10)
	refusal(t, parseErr("many.docx", docxOf(t, many)), "too complex")
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("refusing took %v", d)
	}
}

func TestParsePDFEncrypted(t *testing.T) {
	refusal(t, parseErr("locked.pdf", []byte("%PDF-1.7\ntrailer\n<< /Encrypt 5 0 R >>\n%%EOF")), "password protected or encrypted")
}
