//go:build contract

package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// contractFiles pins T2.6 on a real site: upload_file stores a private file
// when asked (and is_private is explicit: Frappe's default is public),
// attachments lists it, download returns the same bytes with credentials
// and a 403 without, attach_file decodes base64, get_html_and_style renders,
// and download_pdf returns a PDF (or a JSON error, never saved).
func contractFiles(t *testing.T, c *client.FrappeClient, sc *config.SiteConfig) {
	name := createContractDoc(t, c, map[string]interface{}{"title": "files"})
	t.Cleanup(func() { deleteContractFiles(t, c, name) })
	cfg := contractConfig(t, sc)
	dir := t.TempDir()

	content := make([]byte, 4096)
	_, _ = rand.Read(content)
	src := filepath.Join(dir, fmt.Sprintf("ffc-contract-%d.bin", time.Now().UnixNano()))
	if err := os.WriteFile(src, content, 0o600); err != nil {
		t.Fatal(err)
	}
	r := runFFC(t, cfg, "", "upload", src, "-d", contractDT, "-n", name, "--json")
	if r.Code != 0 {
		t.Fatalf("upload: exit %d: %s", r.Code, r.Stderr)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
		t.Fatal(err)
	}
	fileURL, _ := doc["file_url"].(string)
	if !strings.HasPrefix(fileURL, "/private/files/") || fmt.Sprint(doc["is_private"]) != "1" ||
		doc["folder"] != client.DefaultAttachFolder || fmt.Sprint(doc["file_size"]) != "4096" {
		t.Fatalf("uploaded File = %v", doc)
	}

	r = runFFC(t, cfg, "", "attachments", "-d", contractDT, "-n", name, "--json")
	if r.Code != 0 || !strings.Contains(r.Stdout, fileURL) {
		t.Errorf("attachments: exit %d %s %s", r.Code, r.Stdout, r.Stderr)
	}

	out := filepath.Join(dir, "out.bin")
	r = runFFC(t, cfg, "", "download", fileURL, "-o", out)
	if got, _ := os.ReadFile(out); r.Code != 0 || !bytes.Equal(got, content) {
		t.Errorf("download: exit %d %s; %d bytes equal %v", r.Code, r.Stderr, len(got), bytes.Equal(got, content))
	}
	r = runFFC(t, cfg, "", "download", strings.TrimRight(sc.URL, "/")+fileURL, "-o", "-")
	if r.Code != 0 || r.Stdout != string(content) {
		t.Errorf("download by site URL to stdout: exit %d %s", r.Code, r.Stderr)
	}
	// Without credentials a private file is a 403, and so is a missing one.
	for _, u := range []string{fileURL, "/private/files/ffc-contract-missing.bin"} {
		resp, err := http.Get(strings.TrimRight(sc.URL, "/") + u)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("anonymous GET %s: %d, want 403", u, resp.StatusCode)
		}
	}
	r = runFFC(t, cfg, "", "download", "/private/files/ffc-contract-missing.bin", "-o", filepath.Join(dir, "m"))
	if r.Code != exitPermission {
		t.Errorf("missing private file: exit %d %s", r.Code, r.Stderr)
	}

	// attach_file (the MCP tool's call) decodes base64; private when asked.
	ctx := contractCtx(t)
	att, err := c.AttachFile(ctx, client.FileUpload{Filename: "ffc-contract-attach.txt", Content: []byte("attached by ffc"),
		Doctype: contractDT, Docname: name, Private: true})
	if err != nil {
		t.Fatalf("attach_file: %v", err)
	}
	attURL, _ := att["file_url"].(string)
	if !strings.HasPrefix(attURL, "/private/files/") {
		t.Errorf("attach_file File = %v", att)
	}
	resp, err := c.Download(ctx, attURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	_, _ = got.ReadFrom(resp.Body)
	_ = resp.Body.Close()
	if got.String() != "attached by ffc" {
		t.Errorf("attached content %q", got.String())
	}

	// get_html_and_style renders the document.
	ph, err := c.PrintHTML(ctx, contractDT, name, client.PrintOptions{NoLetterhead: true})
	if err != nil {
		t.Fatalf("get_html_and_style: %v", err)
	}
	if h, _ := ph["html"].(string); !strings.Contains(h, "files") {
		t.Errorf("print HTML lacks the title: %.300s", h)
	}
	if _, ok := ph["style"].(string); !ok {
		t.Errorf("no style: %v", ph["style"])
	}

	t.Run("pdf", func(t *testing.T) {
		missing := filepath.Join(dir, "missing.pdf")
		if r := runFFC(t, cfg, "", "pdf", "-d", contractDT, "-n", "ffc-contract-no-such-doc", "-o", missing); r.Code != exitNotFound {
			t.Errorf("missing document: exit %d %s", r.Code, r.Stderr)
		}
		pdf := filepath.Join(dir, "doc.pdf")
		r := runFFC(t, cfg, "", "pdf", "-d", contractDT, "-n", name, "--no-letterhead", "-o", pdf)
		b, statErr := os.ReadFile(pdf)
		if r.Code != 0 {
			// A site in Docker whose wkhtmltopdf cannot reach the site's own
			// URL fails with OSError: the error must not be saved as a PDF.
			if statErr == nil {
				t.Fatalf("an error response was saved: %.200s", b)
			}
			if strings.Contains(r.Stderr, "wkhtmltopdf") {
				t.Skipf("the site cannot render PDFs: %s", r.Stderr)
			}
			t.Fatalf("pdf: exit %d %s", r.Code, r.Stderr)
		}
		if !bytes.HasPrefix(b, []byte("%PDF-")) {
			t.Errorf("not a PDF: %.40q", b)
		}
	})
}

// deleteContractFiles removes the File documents attached to a fixture
// document (deleting the document would leave a public copy's file behind).
func deleteContractFiles(t *testing.T, c *client.FrappeClient, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	rows, err := c.Attachments(ctx, contractDT, name, -1)
	if err != nil {
		t.Logf("cleanup: listing attachments: %v", err)
		return
	}
	for _, r := range rows {
		if err := c.DeleteDoc(ctx, "File", fmt.Sprint(r["name"])); err != nil {
			t.Logf("cleanup: delete File %v: %v", r["name"], err)
		}
	}
}
