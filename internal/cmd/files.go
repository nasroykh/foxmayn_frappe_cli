package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
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

// maxUploadInMemory caps an upload read from stdin or a pipe, which is held
// in memory; a regular file is streamed from disk. A var for tests.
var maxUploadInMemory int64 = client.MaxResponseBytes

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
file extensions apply. Uploads are never retried. A file is streamed from
disk; stdin is read into memory first, up to 128 MiB.

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
			u := client.FileUpload{
				Filename: name, Doctype: upDoctype, Docname: upName,
				Field: upField, Folder: upFolder, Private: !upPublic,
			}
			done, err := openUpload(&u, src, c.MaxFileSize(ctx))
			defer done()
			if err != nil {
				return nil, err
			}
			doc, err := c.UploadFile(ctx, u)
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
	case text.Sanitize(name) != name || strings.ContainsAny(name, "\n\t"):
		return usageErrorf("invalid file name %q: control or invisible characters", name)
	}
	return nil
}

// openUpload opens the file to upload (src "-" is stdin) into u, refusing
// one over limit (0: the site did not say) before reading more than it. A
// regular file is streamed from disk as it is sent; stdin or a pipe is read
// into memory first, so it is also held to maxUploadInMemory. The caller
// runs the returned func when the upload is done.
func openUpload(u *client.FileUpload, src string, limit int64) (func(), error) {
	done := func() {}
	var r io.Reader = os.Stdin
	if src != "-" {
		f, err := os.Open(src)
		if err != nil {
			return done, fmt.Errorf("reading file: %w", err)
		}
		st, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return done, fmt.Errorf("reading file: %w", err)
		}
		switch {
		case st.IsDir():
			_ = f.Close()
			return done, usageErrorf("%s is a directory", src)
		case st.Mode().IsRegular():
			if limit > 0 && st.Size() > limit {
				_ = f.Close()
				return done, client.FileTooLarge(u.Filename, st.Size(), limit)
			}
			u.File, u.Size = f, st.Size()
			return func() { _ = f.Close() }, nil
		}
		defer f.Close() // a FIFO or device: read like stdin
		r = f
	}
	most := limit
	if most <= 0 || most > maxUploadInMemory {
		most = maxUploadInMemory
	}
	b, err := io.ReadAll(io.LimitReader(r, most+1))
	if err != nil {
		return done, fmt.Errorf("reading %s: %w", src, err)
	}
	if int64(len(b)) > most {
		if most == limit {
			return done, client.FileTooLarge(u.Filename, int64(len(b)), limit)
		}
		return done, &client.StateError{Message: fmt.Sprintf("%s is over %d MiB, the most ffc reads from stdin or a pipe: save it to a file and upload that",
			u.Filename, most>>20)}
	}
	u.Content = b
	return done, nil
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
		perm := os.FileMode(0o666) // less the umask, like any new file
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
	if err == nil && base == "-" {
		err = errors.New(`"-" means stdout`) // only when the user asks for it
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
// into place (see writeAtomic).
func saveAtomic(body io.Reader, dst string, force bool, perm os.FileMode) (int64, error) {
	var n int64
	err := writeAtomic(dst, force, perm, func(w io.Writer) error {
		var err error
		if n, err = io.Copy(w, body); err != nil {
			return fmt.Errorf("saving to %s: %w", dst, err)
		}
		return nil
	})
	return n, err
}

// writeAtomic lets write fill a temporary file next to dst and moves it
// into place: an interrupted or failed write never leaves a partial file
// at dst. write's error is returned as is. Without force an existing dst is
// never replaced, even one created while write ran (the move is a hard
// link, which fails when dst exists). The file is created with perm less
// the umask (os.CreateTemp would force 0600), so it is never more open than
// the user's other files.
func writeAtomic(dst string, force bool, perm os.FileMode, write func(io.Writer) error) error {
	tmp, err := createTemp(filepath.Dir(dst), perm)
	if err != nil {
		return fmt.Errorf("creating the output file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a rename
	if err := write(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	err = tmp.Sync()
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("saving to %s: %w", dst, err)
	}
	if force {
		if err := os.Rename(tmpName, dst); err != nil {
			return fmt.Errorf("saving to %s: %w", dst, err)
		}
		return nil
	}
	switch err := os.Link(tmpName, dst); {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrExist):
		return usageErrorf("%s exists: pass --force to replace it", dst)
	}
	// No hard links on this file system: check, then rename.
	if _, err := os.Lstat(dst); err == nil {
		return usageErrorf("%s exists: pass --force to replace it", dst)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("saving to %s: %w", dst, err)
	}
	return nil
}

// createTemp is os.CreateTemp with a mode: a new hidden file in dir, created
// exclusively with perm (less the umask).
func createTemp(dir string, perm os.FileMode) (*os.File, error) {
	for range 100 {
		name := filepath.Join(dir, ".ffc-download-"+strconv.FormatUint(rand.Uint64(), 36))
		f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, perm)
		if !errors.Is(err, fs.ErrExist) {
			return f, err
		}
	}
	return nil, errors.New("no free temporary file name")
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
