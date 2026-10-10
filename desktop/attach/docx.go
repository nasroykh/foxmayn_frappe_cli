package attach

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

// docxUnzipLimit caps the unpacked size of a document's text part
// (word/document.xml). The size the zip declares is checked before reading,
// and the read itself stops one byte past the limit, so a zip bomb is never
// unpacked whole. docxMaxDepth and docxMaxTokens bound the XML walk, so a
// small file of deeply nested or countless empty elements cannot cost
// seconds of CPU and gigabytes of memory.
const (
	docxUnzipLimit = 32 << 20
	docxMaxDepth   = 256
	docxMaxTokens  = 2_000_000
)

var blankLines = regexp.MustCompile(`\n{3,}`)

// docxText reads the text of a Word document: its paragraphs one per line,
// table cells separated by tabs and rows by lines. Headers, footers, notes,
// comments, deleted text and field codes are left out. Past MaxTextChars the
// document is refused rather than cut.
func docxText(name string, data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", refuse("%s is not a valid DOCX document.", name)
	}
	var doc *zip.File
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			doc = f
			break
		}
	}
	if doc == nil {
		return "", refuse("%s is not a valid DOCX document.", name)
	}
	if doc.UncompressedSize64 > docxUnzipLimit {
		return "", refuse("%s unpacks to more than 32 MB and was not read.", name)
	}
	rc, err := doc.Open()
	if err != nil {
		return "", refuse("%s is not a valid DOCX document.", name)
	}
	defer rc.Close()
	body, err := io.ReadAll(io.LimitReader(rc, docxUnzipLimit+1))
	if err != nil {
		// archive/zip refuses an entry that holds more than it declares.
		return "", refuse("%s is not a valid DOCX document.", name)
	}
	if len(body) > docxUnzipLimit {
		return "", refuse("%s unpacks to more than 32 MB and was not read.", name)
	}
	text, err := wordText(body)
	if errors.Is(err, errTooMuchText) {
		return "", refuse("%s has more than 200 000 characters of text.", name)
	}
	if errors.Is(err, errTooComplex) {
		return "", refuse("%s is too complex to read.", name)
	}
	if err != nil {
		return "", refuse("%s is not a valid DOCX document.", name)
	}
	if strings.TrimSpace(text) == "" {
		return "", refuse("%s has no text.", name)
	}
	return text, nil
}

var (
	errTooMuchText = errors.New("too much text")
	errTooComplex  = errors.New("too deep or too many elements")
)

// wordText walks WordprocessingML. Only the text of w:t in a run counts,
// and only runs that are not hidden (w:vanish); w:tab and w:br/w:cr in a run
// add a tab or a line (a space inside a table cell, so cells stay columns).
// A paragraph ends a line (a space inside a cell), a cell a tab and a row a
// line. Property subtrees (pPr, rPr, sectPr: tab stops, fonts) and the
// mc:Fallback copy of text boxes are skipped.
func wordText(body []byte) (string, error) {
	d := xml.NewDecoder(bytes.NewReader(body))
	var out strings.Builder
	depth, tokens := 0, 0
	// skip is the depth of a subtree being skipped (0: none).
	skip := 0
	inRun, inText, hidden, cells := false, false, false, 0
	inProps := 0 // inside a run's rPr: where w:vanish is read
	chars := 0
	// A paragraph ending inside a cell becomes a space only when more text
	// follows in that cell.
	space := false
	write := func(s string) error {
		if space && s != "\t" {
			s = " " + s
		}
		space = false
		chars += utf8.RuneCountInString(s)
		if chars > MaxTextChars {
			return errTooMuchText
		}
		out.WriteString(s)
		return nil
	}
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		tokens++
		if tokens > docxMaxTokens {
			return "", errTooComplex
		}
		var s string
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth > docxMaxDepth {
				return "", errTooComplex
			}
			if skip > 0 {
				continue
			}
			switch t.Name.Local {
			case "pPr", "sectPr", "Fallback":
				skip = depth
			case "rPr":
				if inRun {
					inProps = depth
				} else {
					skip = depth
				}
			case "vanish":
				if inProps > 0 {
					hidden = true
				}
			case "r":
				inRun, hidden = true, false
			case "t":
				inText = inRun
			case "tab":
				if inProps == 0 && inRun && !hidden {
					s = "\t"
				}
			case "br", "cr":
				if inProps == 0 && inRun && !hidden {
					s = "\n"
				}
			case "tc":
				cells++
			}
			if cells > 0 && (s == "\t" || s == "\n") {
				s = " "
			}
		case xml.EndElement:
			if skip > 0 {
				if depth == skip {
					skip = 0
				}
				depth--
				continue
			}
			if depth == inProps {
				inProps = 0
			}
			depth--
			switch t.Name.Local {
			case "t":
				inText = false
			case "r":
				inRun, hidden = false, false
			case "p":
				if cells > 0 {
					space = true
				} else {
					s = "\n"
				}
			case "tc":
				cells--
				s = "\t"
			case "tr":
				s = "\n"
			}
		case xml.CharData:
			if inText && !hidden && skip == 0 {
				s = string(t)
			}
		}
		if s != "" {
			if err := write(s); err != nil {
				return "", err
			}
		}
	}
	text := stripControls(out.String())
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(blankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")), nil
}
