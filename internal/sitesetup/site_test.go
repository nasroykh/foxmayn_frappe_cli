package sitesetup

import (
	"context"
	"errors"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/frappetest"
)

// Verify proves each credential kind with the calls the setup has always
// made: a key or token through get_logged_user, a password by a login that
// is logged out again.
func TestVerify(t *testing.T) {
	ctx := context.Background()
	site := frappetest.New(t)

	for name, sc := range map[string]config.SiteConfig{
		"apikey": {URL: site.URL, APIKey: frappetest.APIKey, APISecret: frappetest.APISecret},
		"oauth":  {URL: site.URL, AccessToken: frappetest.Token},
	} {
		user, err := Verify(ctx, sc)
		if err != nil || user == "" {
			t.Errorf("%s: user %q, err %v", name, user, err)
		}
	}

	user, err := Verify(ctx, config.SiteConfig{URL: site.URL, Username: frappetest.Username, Password: frappetest.Password})
	if err != nil || user != "" || site.Logins() != 1 || site.Logouts() != 1 {
		t.Errorf("password: user %q, err %v, logins %d, logouts %d", user, err, site.Logins(), site.Logouts())
	}

	_, err = Verify(ctx, config.SiteConfig{URL: site.URL, APIKey: frappetest.APIKey, APISecret: "wrong"})
	var ae *client.APIError
	if !errors.As(err, &ae) {
		t.Errorf("wrong secret: err = %v, want an APIError", err)
	}
	if _, err := Verify(ctx, config.SiteConfig{URL: site.URL, APIKey: "only-a-key"}); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("no credentials: err = %v", err)
	}
}

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		in, want string
		wantErr  bool
	}{
		{"erp.example.com", "https://erp.example.com", false},
		{"  erp.example.com/  ", "https://erp.example.com", false},
		{"HTTPS://erp.example.com", "https://erp.example.com", false},
		{"Http://localhost:8000", "http://localhost:8000", false},
		{"erp.example.com/app/home?x=1#frag", "https://erp.example.com", false},
		{"https://erp.example.com:8443/", "https://erp.example.com:8443", false},
		{"erp.example.com:8000", "https://erp.example.com:8000", false},
		{"http://[::1]:8000/x", "http://[::1]:8000", false},
		{"", "", true},
		{"   ", "", true},
		{"ftp://erp.example.com", "", true},
		{"https://", "", true},
		{"https:///path", "", true},
		{"https://user:pw@erp.example.com", "", true},
		{"erp.example.com:notaport", "", true},
	}
	for _, tt := range tests {
		got, err := NormalizeURL(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("NormalizeURL(%q) = (%q, %v), want (%q, err=%v)", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"dev", "Prod", "erp.example.com", "#dev", "my_site-2", " padded "} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "   ", "my site", "a\tb", "a\nb", "esc\x1b[31m", "nul\x00", "bad\xff"} {
		if err := ValidateName(bad); err == nil {
			t.Errorf("ValidateName(%q) = nil, want error", bad)
		}
	}
}
