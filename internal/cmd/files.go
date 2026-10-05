package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"github.com/spf13/cobra"
)

// Files (T2.6): upload, download, attachments. pdf is in pdf.go.

// upload flags
var (
	upDoctype  string
	upName     string
	upField    string
	upFolder   string
	upFilename string
	upPublic   bool
)

// uploadFields are the File fields upload shows.
var uploadFields = []string{"name", "file_name", "file_url", "is_private", "file_size",
	"attached_to_doctype", "attached_to_name", "attached_to_field", "folder"}

// maxUploadWithoutLimit caps an upload when the site does not report its
// limit (frappe.core.api.file.get_max_file_size missing).
const maxUploadWithoutLimit = client.MaxResponseBytes

var uploadCmd = &cobra.Command{
	Use:   "upload FILE",
	Short: "Upload a file and attach it to a document",
	Long: `Upload FILE and attach it to a document, as the desk's Attach button does
(POST /api/method/upload_file). FILE "-" reads stdin; name the file with
--filename then.

The file is private by default (served from /private/files, only to users
who may read the document); --public stores it under /files, readable by
anyone with the URL. It goes in the "Home/Attachments" folder unless
--folder says otherwise. --field also sets that Attach field of the
document to the file's URL (a second request, an update of the document).

The document must exist (upload_file itself would attach the file to a
missing one). Files over the site's limit (System Settings > Max File Size,
25 MiB by default) are refused before anything is sent; the site's allowed
file extensions apply. Uploads are never retried.

--dry-run shows the request with the file's name and size, never its
content.

Examples:
  ffc upload contract.pdf -d Customer -n "ACME Corp"
  ffc upload logo.png -d Item -n ITEM-001 --field image --public
  pg_dump mydb | gzip | ffc upload - --filename db.sql.gz -d ToDo -n TD-0001
  ffc upload notes.txt -d ToDo -n TD-0001 --dry-run
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		src := args[0]
		name := upFilename
		if name == "" {
			if src == "-" {
				return usageErrorf("reading stdin: name the file with --filename")
			}
			name = filepath.Base(src)
		}
		if err := checkFileName(name); err != nil {
			return err
		}
		title := fmt.Sprintf("Uploading %s to %s %s…", name, upDoctype, upName)
		doc, err := callSite(cmd, title, func(ctx context.Context, c *client.FrappeClient) (map[string]interface{}, error) {
			content, err := readUpload(src, name, c.MaxFileSize(ctx))
			if err != nil {
				return nil, err
			}
			doc, err := c.UploadFile(ctx, client.FileUpload{
				Filename: name, Content: content, Doctype: upDoctype, Docname: upName,
				Field: upField, Folder: upFolder, Private: !upPublic,
			})
			if err != nil || upField == "" {
				return doc, err
			}
			fileURL, _ := doc["file_url"].(string)
			if _, err := c.UpdateDoc(ctx, upDoctype, upName, map[string]interface{}{upField: fileURL}); err != nil {
				return nil, fmt.Errorf("uploaded %s (File %v), but setting %s.%s failed: %w", fileURL, doc["name"], upDoctype, upField, err)
			}
			return doc, nil
		})
		if err != nil {
			return err
		}
		return render(doc, uploadFields, func() error {
			output.PrintDocTable(doc, uploadFields)
			return nil
		})
	},
}

// checkFileName refuses a name Frappe would change or that cannot be shown.
func checkFileName(name string) error {
	switch {
	case name == "" || name == "." || name == "..":
		return usageErrorf("invalid file name %q", name)
	case strings.ContainsAny(name, `/\`):
		return usageErrorf("invalid file name %q: no path separators", name)
	case text.Sanitize(name) != name:
		return usageErrorf("invalid file name %q: control or invisible characters", name)
	}
	return nil
}

// readUpload reads the file to upload (src "-" is stdin), refusing one over
// limit (0: the site did not say) before reading more than it.
func readUpload(src, name string, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = maxUploadWithoutLimit
	}
	var r io.Reader = os.Stdin
	if src != "-" {
		f, err := os.Open(src)
		if err != nil {
			return nil, fmt.Errorf("reading file: %w", err)
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return nil, fmt.Errorf("reading file: %w", err)
		}
		if st.IsDir() {
			return nil, usageErrorf("%s is a directory", src)
		}
		if st.Mode().IsRegular() && st.Size() > limit {
			return nil, client.FileTooLarge(name, st.Size(), limit)
		}
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", src, err)
	}
	if int64(len(b)) > limit {
		return nil, client.FileTooLarge(name, int64(len(b)), limit)
	}
	return b, nil
}

// download flags
var (
	dlOut   string
	dlForce bool
)

var downloadCmd = &cobra.Command{
	Use:   "download FILE_URL",
	Short: "Download a file of the site",
	Long: `Download a file of the site: a File's file_url, /files/… (public) or
/private/files/… (sent with the site's credentials). A full URL is accepted
only when it is the site's own (same scheme, host and port): the
credentials never go to another host.

The file is written to --output-file (default: its name, in the current
directory) through a temporary file renamed into place, so an interrupted
download leaves nothing behind. An existing file is not overwritten
without --force. "--output-file -" writes to stdout; on a terminal, binary
content is refused. --timeout bounds the whole download.

Examples:
  ffc download /private/files/contract.pdf
  ffc download /files/logo.png -o logo.png --force
  ffc download "$(ffc attachments -d ToDo -n TD-0001 --jq '.[0].file_url')" -o - | sha256sum
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		site, err := loadSiteConfig()
		if err != nil {
			return err
		}
		p, err := client.FilePath(site.URL, args[0])
		if err != nil {
			return &usageError{err}
		}
		out := dlOut
		if out == "" {
			out, err = defaultFileName(p)
			if err != nil {
				return err
			}
		}
		if err := checkTarget(out, dlForce); err != nil {
			return err
		}
		ctx := cmd.Context()
		c, err := newClient(ctx)
		if err != nil {
			return err
		}
		defer c.CloseQuietly()
		resp, err := fetchStream("Downloading "+p+"…", func() (*client.RawResponse, error) {
			return c.Download(ctx, p, nil)
		})
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.Status >= 400 {
			return fileError(resp, p)
		}
		perm := os.FileMode(0o644)
		if strings.HasPrefix(p, "/private/") {
			perm = 0o600
		}
		return saveDownload(resp, out, dlForce, perm)
	},
}

// defaultFileName is the last segment of a file path, as a local name.
func defaultFileName(p string) (string, error) {
	base, err := url.PathUnescape(path.Base(p))
	if err == nil {
		err = checkFileName(base)
	}
	if err != nil {
		return "", usageErrorf("cannot name the file after %q: pass --output-file", p)
	}
	return base, nil
}

// checkTarget refuses an existing output file without --force, before the
// request is sent.
func checkTarget(out string, force bool) error {
	if out == "-" || force {
		return nil
	}
	if _, err := os.Lstat(out); err == nil {
		return usageErrorf("%s exists: pass --force to replace it", out)
	}
	return nil
}

// fetchStream runs fn under the spinner until the response headers arrive.
func fetchStream(title string, fn func() (*client.RawResponse, error)) (*client.RawResponse, error) {
	var resp *client.RawResponse
	var reqErr error
	spinErr := runSpinner(title, func() { resp, reqErr = fn() })
	if reqErr != nil {
		return nil, reqErr
	}
	if spinErr != nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return nil, errAborted
	}
	return resp, nil
}

// fileError is the error for a file request answered with status >= 400.
// Frappe answers a private file that is missing or not readable with the
// same 403 HTML page, so the message says both.
func fileError(resp *client.RawResponse, p string) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if err != nil {
		return fmt.Errorf("reading the error response: %w", err)
	}
	apiErr := client.ResponseError(resp.Status, body)
	var e *client.APIError
	if !errors.As(apiErr, &e) || e.ExcType != "" {
		return apiErr
	}
	switch resp.Status {
	case http.StatusForbidden:
		e.Message = fmt.Sprintf("%s does not exist or your user may not read it (403)", p)
	case http.StatusNotFound:
		e.Message = fmt.Sprintf("%s not found (404)", p)
	}
	return e
}

// saveDownload writes a 2xx body to out ("-": stdout) and reports it.
func saveDownload(resp *client.RawResponse, out string, force bool, perm os.FileMode) error {
	ct := resp.Header.Get("Content-Type")
	if out == "-" {
		return writeBody(resp.Body, ct)
	}
	n, err := saveAtomic(resp.Body, out, force, perm)
	if err != nil {
		return err
	}
	if machineOutput() {
		return printResult(map[string]interface{}{"path": out, "bytes": n, "content_type": ct})
	}
	if !quiet {
		output.PrintSuccess(fmt.Sprintf("Saved %d bytes to %s", n, out))
	}
	return nil
}

// saveAtomic streams body into a temporary file next to path and moves it
// into place: an interrupted download never leaves a partial file at path.
// Without force an existing path is never replaced, even one created while
// the download ran (the move is a hard link, which fails when path exists).
func saveAtomic(body io.Reader, dst string, force bool, perm os.FileMode) (int64, error) {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".ffc-download-*")
	if err != nil {
		return 0, fmt.Errorf("creating the output file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a rename
	n, err := io.Copy(tmp, body)
	if err == nil {
		err = tmp.Chmod(perm)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, fmt.Errorf("saving to %s: %w", dst, err)
	}
	if force {
		if err := os.Rename(tmpName, dst); err != nil {
			return 0, fmt.Errorf("saving to %s: %w", dst, err)
		}
		return n, nil
	}
	switch err := os.Link(tmpName, dst); {
	case err == nil:
		return n, nil
	case errors.Is(err, fs.ErrExist):
		return 0, usageErrorf("%s exists: pass --force to replace it", dst)
	}
	// No hard links on this file system: check, then rename.
	if _, err := os.Lstat(dst); err == nil {
		return 0, usageErrorf("%s exists: pass --force to replace it", dst)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return 0, fmt.Errorf("saving to %s: %w", dst, err)
	}
	return n, nil
}

// attachments flags
var (
	atDoctype string
	atName    string
	atLimit   int
	atPages   pageFlags
)

var attachmentsCmd = &cobra.Command{
	Use:   "attachments",
	Short: "List the files attached to a document",
	Long: `List the File documents attached to a document (attached_to_doctype and
attached_to_name), oldest first: name, file_name, file_url, is_private,
file_size, the Attach field they fill (attached_to_field), folder and
creation. Download one with 'ffc download <file_url>'.

Examples:
  ffc attachments -d ToDo -n TD-0001
  ffc attachments -d "Sales Invoice" -n SINV-0001 --jq '.[].file_url'
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		limit, err := listLimit("--limit", atLimit)
		if err != nil {
			return err
		}
		opts := client.ListOptions{
			Fields:  client.AttachmentFields,
			Filters: client.AttachmentFilters(atDoctype, atName),
			Limit:   limit,
			OrderBy: "creation asc",
		}
		return listDocs(cmd, atPages, fmt.Sprintf("Fetching attachments of %s %s…", atDoctype, atName), "File", opts,
			client.AttachmentFields, func(rows []map[string]interface{}) error {
				output.PrintTable(rows, []string{"name", "file_name", "file_url", "is_private", "file_size"})
				return nil
			})
	},
}

func init() {
	uploadCmd.Flags().StringVarP(&upDoctype, "doctype", "d", "", "DocType of the document to attach to (required)")
	uploadCmd.Flags().StringVarP(&upName, "name", "n", "", "Name of the document to attach to (required)")
	uploadCmd.Flags().StringVar(&upField, "field", "", "Attach field of the document to set to the file's URL")
	uploadCmd.Flags().StringVar(&upFolder, "folder", "", `File folder (default "Home/Attachments")`)
	uploadCmd.Flags().StringVar(&upFilename, "filename", "", "File name on the site (default: FILE's name; required with -)")
	uploadCmd.Flags().BoolVar(&upPublic, "public", false, "Store the file under /files, readable by anyone with the URL (default private)")
	_ = uploadCmd.MarkFlagRequired("doctype")
	_ = uploadCmd.MarkFlagRequired("name")
	addDryRun(uploadCmd, false)
	rootCmd.AddCommand(uploadCmd)

	downloadCmd.Flags().StringVarP(&dlOut, "output-file", "o", "", `Where to save the file ("-" for stdout; default: its name in the current directory)`)
	downloadCmd.Flags().BoolVar(&dlForce, "force", false, "Replace an existing output file")
	rootCmd.AddCommand(downloadCmd)

	attachmentsCmd.Flags().StringVarP(&atDoctype, "doctype", "d", "", "DocType of the document (required)")
	attachmentsCmd.Flags().StringVarP(&atName, "name", "n", "", "Name of the document (required)")
	attachmentsCmd.Flags().IntVarP(&atLimit, "limit", "l", 100, "Maximum files to return (0 = no limit)")
	atPages.register(attachmentsCmd, "limit")
	_ = attachmentsCmd.MarkFlagRequired("doctype")
	_ = attachmentsCmd.MarkFlagRequired("name")
	rootCmd.AddCommand(attachmentsCmd)
}
