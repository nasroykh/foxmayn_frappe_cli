package cmd

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"github.com/spf13/cobra"
)

// pdf flags
var (
	pdfDoctype      string
	pdfName         string
	pdfFormat       string
	pdfLetterhead   string
	pdfNoLetterhead bool
	pdfLang         string
	pdfOut          string
	pdfForce        bool
)

var pdfCmd = &cobra.Command{
	Use:   "pdf",
	Short: "Save a document's print as PDF",
	Long: `Render a document with a print format and save the PDF
(frappe.utils.print_format.download_pdf, the desk's PDF button). Your user
needs read or print access to the document.

--format picks the Print Format (default: the DocType's default),
--letterhead a Letter Head (default: the default one) or --no-letterhead
none, --lang the language (default: your user's).

The PDF is written to --output-file (default <name>.pdf in the current
directory) through a temporary file, and an existing file is not replaced
without --force. "--output-file -" writes to stdout, except on a terminal.
A response that is not a PDF (an error page) is never saved.

Frappe v16 lets only a few PDFs render at a time; a request that waits
more than 10 s gets 503 with Retry-After, and ffc retries it twice. Raise
--timeout for long documents.

Examples:
  ffc pdf -d "Sales Invoice" -n SINV-0001
  ffc pdf -d "Sales Invoice" -n SINV-0001 --format "Detailed Invoice" --no-letterhead -o inv.pdf
  ffc pdf -d Quotation -n QTN-0001 --lang fr -o - | lpr
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := pdfOut
		if out == "" {
			out = pdfFileName(pdfName)
		}
		if err := checkTarget(out, pdfForce); err != nil {
			return err
		}
		ctx := cmd.Context()
		c, err := newClient(ctx)
		if err != nil {
			return err
		}
		defer c.CloseQuietly()
		opts := client.PrintOptions{Format: pdfFormat, Letterhead: pdfLetterhead, NoLetterhead: pdfNoLetterhead, Language: pdfLang}
		resp, err := fetchStream(fmt.Sprintf("Rendering %s %s…", pdfDoctype, pdfName), func() (*client.RawResponse, error) {
			return c.PDF(ctx, pdfDoctype, pdfName, opts)
		})
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.Status >= 400 {
			body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
			if err != nil {
				return fmt.Errorf("reading the error response: %w", err)
			}
			err = client.ResponseError(resp.Status, body)
			var e *client.APIError
			if errors.As(err, &e) && e.ExcType == "OSError" {
				e.Message += ": the site could not render the PDF (wkhtmltopdf failed; the site's Error Log has the cause)"
			}
			return err
		}
		if err := checkPDF(resp); err != nil {
			return err
		}
		return saveDownload(resp, out, pdfForce, 0o600)
	},
}

// pdfFileName is the default file for a document's PDF, named like
// Frappe's own (spaces and slashes become dashes).
func pdfFileName(name string) string {
	n := strings.NewReplacer(" ", "-", "/", "-", `\`, "-").Replace(text0(name))
	if n == "" || n == "." || n == ".." {
		n = "document"
	}
	return n + ".pdf"
}

// text0 drops control characters from a name used as a file name.
func text0(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// checkPDF refuses a 2xx response that is not a PDF: by its Content-Type,
// then by its first bytes. It keeps the bytes it peeked in resp.Body.
func checkPDF(resp *client.RawResponse) error {
	ct := resp.Header.Get("Content-Type")
	if !client.IsPDF(resp.Header) {
		head, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("the site answered %q, not a PDF; nothing was saved: %s", ct, text.Sanitize(string(bytes.TrimSpace(head))))
	}
	br := bufio.NewReader(resp.Body)
	head, _ := br.Peek(5)
	if string(head) != "%PDF-" {
		return fmt.Errorf("the site's response is labelled %s but does not start like a PDF; nothing was saved", ct)
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{br, resp.Body}
	return nil
}

func init() {
	pdfCmd.Flags().StringVarP(&pdfDoctype, "doctype", "d", "", "DocType of the document (required)")
	pdfCmd.Flags().StringVarP(&pdfName, "name", "n", "", "Name of the document (required)")
	pdfCmd.Flags().StringVar(&pdfFormat, "format", "", "Print Format (default: the DocType's default)")
	pdfCmd.Flags().StringVar(&pdfLetterhead, "letterhead", "", "Letter Head (default: the default Letter Head)")
	pdfCmd.Flags().BoolVar(&pdfNoLetterhead, "no-letterhead", false, "Print without a letter head")
	pdfCmd.Flags().StringVar(&pdfLang, "lang", "", "Language code, e.g. fr (default: your user's)")
	pdfCmd.Flags().StringVarP(&pdfOut, "output-file", "o", "", `Where to save the PDF ("-" for stdout; default <name>.pdf)`)
	pdfCmd.Flags().BoolVar(&pdfForce, "force", false, "Replace an existing output file")
	pdfCmd.MarkFlagsMutuallyExclusive("letterhead", "no-letterhead")
	_ = pdfCmd.MarkFlagRequired("doctype")
	_ = pdfCmd.MarkFlagRequired("name")
	rootCmd.AddCommand(pdfCmd)
}
