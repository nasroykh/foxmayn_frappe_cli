package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/spf13/cobra"
)

// import-template flags
var (
	itDoctype string
	itOut     string
	itXLSX    bool
	itForce   bool
	itSelect  exportSelect
)

var importTemplateCmd = &cobra.Command{
	Use:   "import-template",
	Short: "Write an empty Data Import template for a DocType",
	Long: `Write the header row of a Data Import CSV for a DocType: the same columns
as 'ffc export' (name, the DocType's readable data fields, then
items.name, items.<field> for each table), chosen with the same --fields,
--tables and --no-tables. Fill one row per document, and one more row per
extra table row with the document's columns left blank.

Like the desk's template, it includes read-only and hidden fields; Frappe
ignores what it cannot set. Password and virtual fields are left out.

--xlsx asks the site for Frappe's own blank Excel template with these
columns (Data Import's download_template). A workbook is never printed to
a terminal.

Examples:
  ffc import-template -d "Sales Invoice" -o invoices.csv
  ffc import-template -d Customer --fields customer_name,customer_group --no-tables
  ffc import-template -d Item --xlsx -o items.xlsx
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if itXLSX {
			if err := refuseFormat("--xlsx"); err != nil {
				return err
			}
		} else {
			// FFC_OUTPUT is a default for other commands: only an explicit
			// format other than CSV is refused.
			flags := rootCmd.PersistentFlags()
			explicit := flags.Changed("output") || flags.Changed("json")
			if jqCode != nil || explicit && outFormat != output.FormatTable && outFormat != output.FormatCSV {
				return usageErrorf("import-template writes CSV only: drop --output, --json and --jq (or use --xlsx)")
			}
		}
		out, err := exportTarget(itOut, itForce, itXLSX)
		if err != nil {
			return err
		}
		ctx := cmd.Context()
		c, err := newClient(ctx)
		if err != nil {
			return err
		}
		defer c.CloseQuietly()
		layout, err := loadExportLayout(ctx, c, itDoctype, itSelect)
		if err != nil {
			return err
		}
		if itXLSX {
			return saveTemplate(ctx, c, layout, client.TemplateOptions{Records: "blank_template", FileType: "Excel"}, out, itForce)
		}
		write := func(w io.Writer, clean bool) error {
			buf := bufio.NewWriter(w)
			if err := output.NewListStream(buf, output.FormatCSV, nil, clean).Record(layout.header()); err != nil {
				return writeErr(err)
			}
			return writeErr(buf.Flush())
		}
		if out == "-" {
			return write(os.Stdout, stdoutIsTerminal())
		}
		if err := writeAtomic(out, itForce, 0o666, func(w io.Writer) error { return write(w, false) }); err != nil {
			return err
		}
		if !quiet {
			output.PrintSuccess(fmt.Sprintf("Wrote the %s template (%d columns) to %s", itDoctype, len(layout.header()), out))
		}
		return nil
	},
}

func init() {
	importTemplateCmd.Flags().StringVarP(&itDoctype, "doctype", "d", "", "DocType of the template (required)")
	importTemplateCmd.Flags().StringVarP(&itOut, "output-file", "o", "", `File to write ("-" or none: stdout)`)
	importTemplateCmd.Flags().BoolVar(&itXLSX, "xlsx", false, "Ask the site for Frappe's blank Excel template")
	importTemplateCmd.Flags().BoolVar(&itForce, "force", false, "Replace an existing output file")
	itSelect.register(importTemplateCmd)
	_ = importTemplateCmd.MarkFlagRequired("doctype")
	rootCmd.AddCommand(importTemplateCmd)
}
