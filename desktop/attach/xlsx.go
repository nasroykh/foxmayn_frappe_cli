package attach

import (
	"bytes"
	"encoding/csv"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// xlsxText turns every sheet of a workbook into CSV, each under a
// "Sheet: <name>" line. The workbook is unzipped in memory under
// xlsxUnzipLimit; a workbook past that, or past the sheet, row, column, cell
// or text caps, is refused rather than cut, so the model never takes part of
// a sheet for all of it.
func xlsxText(name string, data []byte) (string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{
		UnzipSizeLimit:    xlsxUnzipLimit,
		UnzipXMLSizeLimit: xlsxUnzipLimit,
	})
	if err != nil {
		if strings.Contains(err.Error(), "unzip size") {
			return "", refuse("%s unpacks to more than 64 MB and was not read.", name)
		}
		if errors.Is(err, excelize.ErrWorkbookFileFormat) || errors.Is(err, excelize.ErrWorkbookPassword) {
			return "", refuse("%s is password protected or not an XLSX workbook.", name)
		}
		return "", refuse("%s is not a valid XLSX workbook.", name)
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return "", refuse("%s has no sheets.", name)
	}
	if len(sheets) > xlsxMaxSheets {
		return "", refuse("%s has more than %d sheets.", name, xlsxMaxSheets)
	}
	var out strings.Builder
	chars, cells := 0, 0
	tooBig := func() error { return refuse("%s has more than 200 000 characters of data.", name) }
	for i, sheet := range sheets {
		head := "Sheet: " + stripControls(sheet) + "\n"
		if i > 0 {
			head = "\n" + head
		}
		chars += utf8.RuneCountInString(head)
		out.WriteString(head)
		rows, err := f.Rows(sheet)
		if err != nil {
			return "", refuse("%s: the sheet %q could not be read.", name, sheet)
		}
		n := 0
		for rows.Next() {
			n++
			if n > xlsxMaxRows {
				_ = rows.Close()
				return "", refuse("%s: the sheet %q has more than %d rows.", name, sheet, xlsxMaxRows)
			}
			cols, err := rows.Columns()
			if err != nil {
				_ = rows.Close()
				return "", refuse("%s: the sheet %q could not be read.", name, sheet)
			}
			if len(cols) > xlsxMaxColumns {
				_ = rows.Close()
				return "", refuse("%s: the sheet %q has more than %d columns.", name, sheet, xlsxMaxColumns)
			}
			cells += len(cols)
			if cells > xlsxMaxCells {
				_ = rows.Close()
				return "", refuse("%s has more than %d cells.", name, xlsxMaxCells)
			}
			for j, c := range cols {
				c = strings.ReplaceAll(c, "\r\n", "\n")
				cols[j] = stripControls(strings.ReplaceAll(c, "\r", "\n"))
			}
			line, err := csvLine(cols)
			if err != nil {
				_ = rows.Close()
				return "", refuse("%s: the sheet %q could not be read.", name, sheet)
			}
			chars += utf8.RuneCountInString(line)
			if chars > MaxTextChars {
				_ = rows.Close()
				return "", tooBig()
			}
			out.WriteString(line)
		}
		err = rows.Error()
		_ = rows.Close()
		if err != nil {
			return "", refuse("%s: the sheet %q could not be read.", name, sheet)
		}
	}
	text := out.String()
	if chars > MaxTextChars {
		return "", tooBig()
	}
	return text, nil
}

// csvLine writes one CSV record, LF-terminated.
func csvLine(cols []string) (string, error) {
	var b strings.Builder
	w := csv.NewWriter(&b)
	if err := w.Write(cols); err != nil {
		return "", err
	}
	w.Flush()
	return b.String(), w.Error()
}
