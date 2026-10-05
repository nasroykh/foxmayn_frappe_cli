package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
)

// The files tool set (T2.6): list_attachments, attach_file, get_print_html.
// It is off unless `ffc mcp --toolsets` names it. No tool returns binary:
// files and PDFs are the CLI's (ffc download, ffc pdf).

// maxAttachBytes caps the decoded content attach_file sends.
const maxAttachBytes = 5 << 20

func registerFileTools(s *server.MCPServer, env *mcpEnv) {
	s.AddTool(docTool("list_attachments",
		"List the files attached to a document (File documents with attached_to_doctype/attached_to_name), oldest first: name, file_name, file_url, is_private, file_size, attached_to_field, folder, creation. File contents are not returned.",
		true, false,
		mcp.WithNumber("limit", mcp.Description("Maximum files to return. Default 100; 0 means no limit.")),
	), docHandler(env, func(req mcp.CallToolRequest, doctype, name string) (toolCall, error) {
		limit, err := intArg(req, "limit", 100)
		if err != nil {
			return nil, err
		}
		limit, _ = listLimit("limit", limit)
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			return c.Attachments(ctx, doctype, name, limit)
		}, nil
	}))

	s.AddTool(docTool("attach_file",
		fmt.Sprintf("Attach a file to a document (frappe.client.attach_file). data is the content, base64 by default or UTF-8 text with encoding=text; at most %d MiB decoded. The file is private unless is_private is false. With field, that Attach field of the document is set to the file URL and the document saved. Returns the File document. Writing File is denied unless the site's MCP config lists File in allow_doctypes.", maxAttachBytes>>20),
		false, false,
		mcp.WithString("filename", mcp.Required(), mcp.Description("File name on the site, e.g. 'notes.txt'")),
		mcp.WithString("data", mcp.Required(), mcp.Description("The file content: base64, or text with encoding=text")),
		mcp.WithString("encoding", mcp.Enum("base64", "text"), mcp.Description("How data is encoded (default base64)")),
		mcp.WithBoolean("is_private", mcp.Description("Store under /private/files, readable only by users who may read the document (default true)")),
		mcp.WithString("folder", mcp.Description(`File folder (default "Home/Attachments")`)),
		mcp.WithString("field", mcp.Description("Attach field of the document to set to the file URL")),
	), docHandler(env, func(req mcp.CallToolRequest, doctype, name string) (toolCall, error) {
		u, err := attachArgs(req)
		if err != nil {
			return nil, err
		}
		u.Doctype, u.Docname = doctype, name
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			return c.AttachFile(ctx, u)
		}, nil
	}))

	s.AddTool(docTool("get_print_html",
		"Render a document with a print format, as HTML (frappe.www.printview.get_html_and_style): returns {html, style}, or {text} with text_only, which is the readable text without tags. Needs read access to the document. Results over 512 KiB drop the style, then cut the HTML (truncated: true). For a PDF use the CLI: ffc pdf.",
		true, false,
		mcp.WithString("print_format", mcp.Description("Print Format (default: the DocType's default)")),
		mcp.WithString("letterhead", mcp.Description("Letter Head (default: the default one)")),
		mcp.WithBoolean("no_letterhead", mcp.Description("Render without a letter head")),
		mcp.WithString("language", mcp.Description("Language code, e.g. fr (default: the user's)")),
		mcp.WithBoolean("text_only", mcp.Description("Return only the text, without tags and styles (smaller)")),
	), docHandler(env, func(req mcp.CallToolRequest, doctype, name string) (toolCall, error) {
		var o client.PrintOptions
		var err error
		for k, p := range map[string]*string{"print_format": &o.Format, "letterhead": &o.Letterhead, "language": &o.Language} {
			if *p, err = stringArg(req, k); err != nil {
				return nil, err
			}
		}
		o.NoLetterhead = req.GetBool("no_letterhead", false)
		textOnly := req.GetBool("text_only", false)
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			res, err := c.PrintHTML(ctx, doctype, name, o)
			if err != nil {
				return nil, err
			}
			h, _ := res["html"].(string)
			if textOnly {
				return fitPrint(map[string]interface{}{"text": htmlText(h)}, "text"), nil
			}
			return fitPrint(map[string]interface{}{"html": h, "style": res["style"]}, "html"), nil
		}, nil
	}))
}

// attachArgs reads attach_file's own arguments.
func attachArgs(req mcp.CallToolRequest) (client.FileUpload, error) {
	var u client.FileUpload
	var err error
	if u.Filename, err = stringArg(req, "filename"); err != nil {
		return u, err
	}
	if err := checkFileName(u.Filename); err != nil {
		return u, err
	}
	data, err := stringArg(req, "data")
	if err != nil {
		return u, err
	}
	enc, err := stringArg(req, "encoding")
	if err != nil {
		return u, err
	}
	switch enc {
	case "", "base64":
		// Refuse before decoding what could not fit anyway.
		if len(data) > base64.StdEncoding.EncodedLen(maxAttachBytes)+4 {
			return u, fmt.Errorf("data: the file is over %d MiB; upload it with the CLI (ffc upload)", maxAttachBytes>>20)
		}
		b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(data), ""))
		if err != nil {
			return u, fmt.Errorf("data: not valid base64 (%v); pass encoding=text for plain text", err)
		}
		u.Content = b
	case "text":
		u.Content = []byte(data)
	default:
		return u, fmt.Errorf("encoding: expected base64 or text, got %q", enc)
	}
	if len(u.Content) > maxAttachBytes {
		return u, fmt.Errorf("data: the file is %d bytes, over the %d MiB limit; upload it with the CLI (ffc upload)", len(u.Content), maxAttachBytes>>20)
	}
	if u.Folder, err = stringArg(req, "folder"); err != nil {
		return u, err
	}
	if u.Field, err = stringArg(req, "field"); err != nil {
		return u, err
	}
	u.Private = req.GetBool("is_private", true)
	return u, nil
}

// fitPrint keeps a print result under the tool result cap: first without
// the style, then with the content (key) cut at a rune boundary.
func fitPrint(out map[string]interface{}, key string) map[string]interface{} {
	if jsonSize(out) <= maxToolResultBytes {
		return out
	}
	if _, ok := out["style"]; ok {
		delete(out, "style")
		out["style_omitted"] = true
		if jsonSize(out) <= maxToolResultBytes {
			return out
		}
	}
	s, _ := out[key].(string)
	out["truncated"] = true
	out["hint"] = fmt.Sprintf("the print did not fit in %d KiB and was cut; pass text_only or a smaller print format", maxToolResultBytes>>10)
	// The longest prefix that fits, on a rune boundary (JSON escaping makes
	// the encoded size grow faster than the prefix).
	fits := func(n int) bool {
		for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
			n--
		}
		out[key] = s[:n]
		return jsonSize(out) <= maxToolResultBytes
	}
	n := sort.Search(len(s)+1, func(n int) bool { return !fits(n) }) - 1
	fits(max(n, 0))
	return out
}

var (
	htmlDropRE  = regexp.MustCompile(`(?is)<(style|script|head)\b.*?</(style|script|head)\s*>`)
	htmlBreakRE = regexp.MustCompile(`(?i)<(br|/p|/div|/tr|/h[1-6]|/li|/table)\b[^>]*>`)
	htmlCellRE  = regexp.MustCompile(`(?i)</t[dh]\s*>`)
	htmlTagRE   = regexp.MustCompile(`<[^>]*>`)
	blankRE     = regexp.MustCompile(`[ \t\r\f\v]+`)
	newlinesRE  = regexp.MustCompile(`\n\s*\n+`)
)

// htmlText is the readable text of an HTML print: styles and scripts
// dropped, block ends as line breaks, cells separated by spaces, entities
// decoded, runs of blanks and of empty lines collapsed.
func htmlText(h string) string {
	h = htmlDropRE.ReplaceAllString(h, "")
	h = htmlBreakRE.ReplaceAllString(h, "\n")
	h = htmlCellRE.ReplaceAllString(h, " ")
	h = html.UnescapeString(htmlTagRE.ReplaceAllString(h, ""))
	lines := strings.Split(h, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(blankRE.ReplaceAllString(l, " "))
	}
	return strings.TrimSpace(newlinesRE.ReplaceAllString(strings.Join(lines, "\n"), "\n"))
}
