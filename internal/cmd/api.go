package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"github.com/spf13/cobra"
)

// api flags
var (
	apiRawFields []string
	apiFields    []string
	apiInput     string
	apiHeaders   []string
	apiInclude   bool
	apiPaginate  bool
	apiOutFile   string
	apiSilent    bool
)

// stdoutIsTerminal is a variable so tests can exercise the terminal rules.
var stdoutIsTerminal = func() bool { return isTerminal(os.Stdout) }

// maxErrorBody caps how much of an error response is read.
const maxErrorBody = 1 << 20

var apiCmd = &cobra.Command{
	Use:   "api [METHOD] PATH",
	Short: "Send an authenticated request to any path of the site",
	Long: `Send a request to any path of the site with the site's credentials and
print the response body: every endpoint ffc has no command for, desk
methods that return more than "message", and file downloads.

PATH is relative to the site URL (/api/resource/..., /api/method/...,
/api/v2/..., /private/files/...); URLs are refused, so the credentials
never go to another host. METHOD defaults to GET, or POST with --input.
Unlike gh, fields alone do not switch to POST: on /api/resource that would
create a document. Name the method to write.

Fields:
  -f key=value   a string
  -F key=value   typed: true, false, null, a number, a JSON object or array,
                 or @FILE / @- for the content of a file / stdin
For GET and HEAD the fields are sent as query parameters, otherwise as a
JSON body. With --input the body is the file and the fields go in the query.

Output: the body is written to stdout as it arrives. --jq and --output
render a JSON body instead (it is then read into memory first). On a terminal, JSON is
indented, text is cleaned of control characters, and binary bodies are
refused: use --output-file or redirect stdout. A response of 400 or more
prints its body (with --json, only the error JSON on stderr) and exits
with the matching exit code. The request is not
retried, and --timeout bounds the whole download.

--paginate fetches every page of a /api/resource/<DocType> or
/api/v2/document/<DocType> list and prints {"data": [...all rows]}. Pages
are requested by offset: pass an order_by when the list may change during
the run.

Examples:
  ffc api /api/method/frappe.desk.form.load.getdoc -f doctype=ToDo -f name=TD-0001
  ffc api /api/resource/Currency --paginate -f 'fields=["name","enabled"]'
  ffc api POST /api/method/frappe.client.set_value -f doctype=ToDo -f name=TD-0001 -f fieldname=status -f value=Closed
  ffc api /api/method/frappe.utils.print_format.download_pdf -f doctype="Sales Invoice" -f name=SINV-0001 --output-file inv.pdf
  ffc api /private/files/contract.pdf > contract.pdf
  ffc api DELETE "/api/resource/ToDo/TD-0001"
`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		req, err := buildAPIRequest(args)
		if err != nil {
			return err
		}
		if apiPaginate {
			return apiPaginated(cmd.Context(), req)
		}
		return apiOnce(cmd.Context(), req)
	},
}

// buildAPIRequest turns the arguments and flags into a request.
func buildAPIRequest(args []string) (client.RawRequest, error) {
	var req client.RawRequest
	rawPath := args[len(args)-1]
	if len(args) == 2 {
		req.Method = strings.ToUpper(args[0])
		if !httpMethod.MatchString(req.Method) {
			return req, usageErrorf("invalid method %q", args[0])
		}
	}
	path, query, err := client.SitePath(rawPath)
	if err != nil {
		return req, &usageError{err}
	}
	req.Path, req.Query = path, query

	stdinReaders := 0
	if apiInput == "-" {
		stdinReaders++
	}
	for _, f := range apiFields {
		if _, v, _ := strings.Cut(f, "="); v == "@-" {
			stdinReaders++
		}
	}
	if stdinReaders > 1 {
		return req, usageErrorf("stdin can be read only once: use one of --input - and -F key=@-")
	}
	fields, err := apiFieldValues()
	if err != nil {
		return req, err
	}
	if req.Method == "" {
		req.Method = http.MethodGet
		if apiInput != "" && !apiPaginate {
			req.Method = http.MethodPost
		}
	}

	req.Header = http.Header{}
	for _, h := range apiHeaders {
		k, v, ok := strings.Cut(h, ":")
		if !ok || strings.TrimSpace(k) == "" {
			return req, usageErrorf("invalid header %q: use \"Name: value\"", h)
		}
		req.Header.Add(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	if err := client.CheckHeaders(req.Header); err != nil {
		return req, &usageError{err}
	}

	inQuery := apiInput != "" || req.Method == http.MethodGet || req.Method == http.MethodHead
	switch {
	case apiInput != "":
		if req.Body, err = readInput("", apiInput); err != nil {
			return req, err
		}
	case len(fields) > 0 && !inQuery:
		if req.Body, err = json.Marshal(fields); err != nil {
			return req, fmt.Errorf("encoding fields: %w", err)
		}
	}
	if inQuery && len(fields) > 0 {
		qp, err := client.QueryArgs(fields)
		if err != nil {
			return req, err
		}
		if req.Query == nil {
			req.Query = url.Values{}
		}
		for k, v := range qp {
			req.Query.Set(k, v)
		}
	}
	if req.Body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// apiFieldValues parses -f (string) and -F (typed) fields; a later field
// with the same key wins.
func apiFieldValues() (map[string]interface{}, error) {
	fields := map[string]interface{}{}
	for _, f := range apiRawFields {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return nil, usageErrorf("invalid field %q: use key=value", f)
		}
		fields[k] = v
	}
	for _, f := range apiFields {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return nil, usageErrorf("invalid field %q: use key=value", f)
		}
		val, err := typedField(v)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", k, err)
		}
		fields[k] = val
	}
	return fields, nil
}

// typedField converts a -F value: literals, numbers and JSON become typed
// values, @FILE the file's content, anything else stays a string.
func typedField(v string) (interface{}, error) {
	switch {
	case v == "true":
		return true, nil
	case v == "false":
		return false, nil
	case v == "null":
		return nil, nil
	case strings.HasPrefix(v, "@"):
		if v == "@" {
			return nil, usageErrorf("@ needs a file name, or - for stdin")
		}
		b, err := readInput("", strings.TrimPrefix(v, "@"))
		if err != nil {
			return nil, err
		}
		return string(b), nil
	case strings.HasPrefix(v, "{"), strings.HasPrefix(v, "["):
		dec := json.NewDecoder(strings.NewReader(v))
		dec.UseNumber()
		var out interface{}
		if err := dec.Decode(&out); err != nil {
			return nil, usageErrorf("invalid JSON value %q", v)
		}
		if _, err := dec.Token(); err != io.EOF {
			return nil, usageErrorf("invalid JSON value %q: unexpected data after it", v)
		}
		return out, nil
	}
	if _, err := strconv.ParseFloat(v, 64); err == nil && json.Valid([]byte(v)) {
		return json.Number(v), nil
	}
	return v, nil
}

// apiSend sends req and stops the spinner when the response headers arrive.
func apiSend(ctx context.Context, c *client.FrappeClient, req client.RawRequest) (*client.RawResponse, error) {
	var resp *client.RawResponse
	var reqErr error
	spinErr := runSpinner(fmt.Sprintf("%s %s…", req.Method, req.Path), func() {
		resp, reqErr = c.Raw(ctx, req)
	})
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

func apiOnce(ctx context.Context, req client.RawRequest) error {
	c, err := newClient(ctx)
	if err != nil {
		return err
	}
	defer c.CloseQuietly()
	resp, err := apiSend(ctx, c, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if apiInclude {
		printResponseHead(resp)
	}

	if resp.Status >= 400 {
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		if err != nil {
			return fmt.Errorf("reading the error response: %w", err)
		}
		// The error body is the response, as for any status; with --json
		// stdout stays empty and the error goes to stderr as JSON.
		if !apiSilent && !jsonOutput {
			// A body that cannot be shown must not hide the HTTP error.
			_ = writeBody(bytes.NewReader(body), resp.Header.Get("Content-Type"))
		}
		return client.ResponseError(resp.Status, body)
	}
	switch {
	case apiOutFile != "":
		return saveBody(resp.Body, apiOutFile)
	case apiSilent:
		return nil
	case machineOutput():
		return printBody(resp.Body)
	}
	return writeBody(resp.Body, resp.Header.Get("Content-Type"))
}

// printBody renders a JSON response with --output or --jq.
func printBody(body io.Reader) error {
	b, err := io.ReadAll(io.LimitReader(body, client.MaxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("reading the response: %w", err)
	}
	if len(b) > client.MaxResponseBytes {
		return fmt.Errorf("response is larger than %d MiB: --output and --jq need it in memory", client.MaxResponseBytes>>20)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("--output and --jq need a JSON response: %w", err)
	}
	return printResult(v)
}

// writeBody copies a response body to stdout. A pipe or file gets the bytes
// unchanged; a terminal gets indented JSON or cleaned text, never binary.
func writeBody(body io.Reader, contentType string) error {
	if !stdoutIsTerminal() {
		if _, err := io.Copy(os.Stdout, body); err != nil {
			return fmt.Errorf("writing the response: %w", err)
		}
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(body, client.MaxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("reading the response: %w", err)
	}
	if len(b) > client.MaxResponseBytes {
		return usageErrorf("response is larger than %d MiB: pass --output-file or redirect stdout", client.MaxResponseBytes>>20)
	}
	if len(b) == 0 {
		return nil
	}
	var indented bytes.Buffer
	switch {
	case json.Indent(&indented, b, "", "  ") == nil:
		b = indented.Bytes()
	case !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0:
		if contentType == "" {
			contentType = "unknown type"
		}
		return usageErrorf("binary response (%s, %d bytes) not printed to a terminal: pass --output-file or redirect stdout", contentType, len(b))
	}
	s := text.Sanitize(string(b))
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	_, err = io.WriteString(os.Stdout, s)
	return err
}

// saveBody streams a body into path, removing the file when the copy fails.
func saveBody(body io.Reader, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}
	n, err := io.Copy(f, body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("saving the response to %s: %w", path, err)
	}
	if !quiet {
		fmt.Fprintf(os.Stderr, "✓ Saved %d bytes to %s\n", n, path)
	}
	return nil
}

// printResponseHead writes the status line and headers to stderr. Cookie
// values are left out: they carry session ids.
func printResponseHead(resp *client.RawResponse) {
	fmt.Fprintf(os.Stderr, "%s %d %s\n", resp.Proto, resp.Status, http.StatusText(resp.Status))
	keys := make([]string, 0, len(resp.Header))
	for k := range resp.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range resp.Header[k] {
			if k == "Set-Cookie" {
				name, _, _ := strings.Cut(v, "=")
				v = name + "=(hidden)"
			}
			fmt.Fprintf(os.Stderr, "%s: %s\n", text.Sanitize(k), text.Sanitize(v))
		}
	}
	fmt.Fprintln(os.Stderr)
}

var (
	httpMethod       = regexp.MustCompile(`^[A-Z]+$`)
	resourceListPath = regexp.MustCompile(`^/api/resource/[^/]+$`)
	v2ListPath       = regexp.MustCompile(`^/api/v2/document/[^/]+$`)
)

// apiPaginated fetches every page of a list and prints the rows as one
// {"data": [...]} object.
func apiPaginated(ctx context.Context, req client.RawRequest) error {
	v1, v2 := resourceListPath.MatchString(req.Path), v2ListPath.MatchString(req.Path)
	switch {
	case req.Method != http.MethodGet:
		return usageErrorf("--paginate works only with GET")
	case !v1 && !v2:
		return usageErrorf("--paginate works only for /api/resource/<DocType> and /api/v2/document/<DocType> lists")
	case apiOutFile != "":
		return usageErrorf("--paginate cannot be combined with --output-file")
	}
	startKey, sizeKeys := "limit_start", []string{"limit_page_length", "limit"}
	if v2 {
		startKey, sizeKeys = "start", []string{"limit"}
	}
	q := url.Values{}
	for k, v := range req.Query {
		q[k] = v
	}
	size := 500
	for _, k := range sizeKeys {
		if s := q.Get(k); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n <= 0 {
				return usageErrorf("--paginate needs a positive %s, got %q", k, s)
			}
			size = n
			q.Del(k)
		}
	}
	q.Set(sizeKeys[0], strconv.Itoa(size))
	start := 0
	if s := q.Get(startKey); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return usageErrorf("invalid %s %q", startKey, s)
		}
		start = n
	}

	c, err := newClient(ctx)
	if err != nil {
		return err
	}
	defer c.CloseQuietly()
	rows := []json.RawMessage{}
	var prevFirst json.RawMessage
	for {
		q.Set(startKey, strconv.Itoa(start))
		req.Query = q
		resp, err := apiSend(ctx, c, req)
		if err != nil {
			return err
		}
		page, err := readPage(resp)
		if err != nil {
			return err
		}
		// A server that ignores the offset returns the same page forever.
		if len(page.Data) > 0 && prevFirst != nil && bytes.Equal(page.Data[0], prevFirst) {
			return fmt.Errorf("page at %s=%d repeats the previous page: the server ignores the offset", startKey, start)
		}
		if len(page.Data) > 0 {
			prevFirst = page.Data[0]
		}
		rows = append(rows, page.Data...)
		start += len(page.Data)
		// v16's v2 list says whether more pages follow; v1 and v15's v2
		// only end with a short page.
		last := len(page.Data) < size
		if page.HasNextPage != nil {
			last = !*page.HasNextPage
		}
		if len(page.Data) == 0 || last {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if apiSilent {
		return nil
	}
	out, err := json.Marshal(map[string]interface{}{"data": rows})
	if err != nil {
		return fmt.Errorf("encoding the rows: %w", err)
	}
	if machineOutput() {
		return printBody(bytes.NewReader(out))
	}
	return writeBody(bytes.NewReader(append(out, '\n')), "application/json")
}

type listPage struct {
	Data        []json.RawMessage `json:"data"`
	HasNextPage *bool             `json:"has_next_page"`
}

// readPage reads one page of a paginated list, or the error it reports.
func readPage(resp *client.RawResponse) (listPage, error) {
	defer resp.Body.Close()
	if apiInclude {
		printResponseHead(resp)
	}
	limit := int64(client.MaxResponseBytes)
	if resp.Status >= 400 {
		limit = maxErrorBody
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return listPage{}, fmt.Errorf("reading the response: %w", err)
	}
	if resp.Status >= 400 {
		return listPage{}, client.ResponseError(resp.Status, body)
	}
	if int64(len(body)) > limit {
		return listPage{}, fmt.Errorf("a page is larger than %d MiB: lower the page size (limit)", client.MaxResponseBytes>>20)
	}
	var page listPage
	if err := json.Unmarshal(body, &page); err != nil {
		return listPage{}, fmt.Errorf("unexpected list response: %w", err)
	}
	if page.Data == nil {
		return listPage{}, errors.New(`unexpected list response: no "data" array`)
	}
	return page, nil
}

func init() {
	apiCmd.Flags().StringArrayVarP(&apiRawFields, "raw-field", "f", nil, "Add a string field key=value (repeatable)")
	apiCmd.Flags().StringArrayVarP(&apiFields, "field", "F", nil, "Add a typed field key=value: true/false/null/number/JSON, @FILE or @- (repeatable)")
	apiCmd.Flags().StringVar(&apiInput, "input", "", "Send the content of FILE (- for stdin) as the request body")
	apiCmd.Flags().StringArrayVarP(&apiHeaders, "header", "H", nil, `Add a request header "Name: value" (repeatable)`)
	apiCmd.Flags().BoolVarP(&apiInclude, "include", "i", false, "Print the response status line and headers to stderr")
	apiCmd.Flags().BoolVar(&apiPaginate, "paginate", false, "Fetch every page of a /api/resource or /api/v2/document list")
	apiCmd.Flags().StringVar(&apiOutFile, "output-file", "", "Save the response body to a file (for binary downloads)")
	apiCmd.Flags().BoolVar(&apiSilent, "silent", false, "Do not print the response body")
	apiCmd.MarkFlagsMutuallyExclusive("output-file", "silent")
	rootCmd.AddCommand(apiCmd)
}
