package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/relsig"
)

func TestVerifyChecksumSignature(t *testing.T) {
	pub, seed, err := relsig.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	_, otherSeed, _ := relsig.GenerateKey()
	saved := relsig.ReleaseKeys
	relsig.ReleaseKeys = []string{pub}
	t.Cleanup(func() { relsig.ReleaseKeys = saved })

	const asset = "ffc_9.9.9_linux_amd64.tar.gz"
	archive := []byte("archive bytes")
	sum := sha256.Sum256(archive)
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n")
	goodSig, _ := relsig.Sign(seed, sums)
	otherSig, _ := relsig.Sign(otherSeed, sums)

	files := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	tests := []struct {
		name    string
		sums    []byte
		sig     []byte
		noSig   bool
		archive []byte
		wantErr string
	}{
		{name: "valid", sums: sums, sig: goodSig, archive: archive},
		{name: "missing signature asset", sums: sums, noSig: true, archive: archive, wantErr: "no checksums.txt.sig"},
		{name: "signed by another key", sums: sums, sig: otherSig, archive: archive, wantErr: "signature check failed"},
		{name: "checksums replaced", sums: []byte(strings.Repeat("0", 64) + "  " + asset + "\n"), sig: goodSig, archive: archive, wantErr: "signature check failed"},
		{name: "archive replaced", sums: sums, sig: goodSig, archive: []byte("evil"), wantErr: "checksum mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files["/checksums.txt"] = tt.sums
			files["/checksums.txt.sig"] = tt.sig
			sigURL := srv.URL + "/checksums.txt.sig"
			if tt.noSig {
				sigURL = ""
			}
			err := verifyChecksum(context.Background(), tt.archive, srv.URL+"/checksums.txt", sigURL, asset)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}
