package config

import "testing"

func TestFormatNumber(t *testing.T) {
	nbsp := " " // French thousands separator
	tests := []struct {
		name   string
		format NumberFormat
		in     float64
		want   string
	}{
		// Integral values render without decimals (M13).
		{"integer one, french", FormatFrench, 1, "1"},
		{"docstatus-like zero", FormatFrench, 0, "0"},
		{"year grouped, french", FormatFrench, 2024, "2" + nbsp + "024"},
		{"year grouped, us", FormatUS, 2024, "2,024"},
		// Fractions keep 2 decimals.
		{"half, french", FormatFrench, 1.5, "1,50"},
		{"currency, us", FormatUS, 1234.5, "1,234.50"},
		// Carry bug fixed: 1.999 rounds up to 2, not "1,100" (M13).
		{"carry to integer", FormatFrench, 1.999, "2"},
		{"carry sub-one", FormatUS, 0.999, "1"},
		{"carry twelve", FormatUS, 12.999, "13"},
		// Float representation: 1.255 stored as 1.2549999… rounds down.
		{"round-down float", FormatUS, 1.255, "1.25"},
		// Negatives.
		{"negative fraction", FormatUS, -1.5, "-1.50"},
		{"negative rounds to zero has no sign", FormatUS, -0.001, "0"},
		// int64 overflow guard (L19): no garbage, falls back to plain formatting.
		{"huge value", FormatUS, 1e19, "10000000000000000000"},
	}

	orig := ActiveFormat
	defer func() { ActiveFormat = orig }()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ActiveFormat = tt.format
			if got := FormatNumber(tt.in); got != tt.want {
				t.Errorf("FormatNumber(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormatDate(t *testing.T) {
	tests := []struct {
		name       string
		dateFormat DateFormat
		in         string
		want       string
	}{
		{"iso passthrough", FormatISODate, "2024-01-02", "2024-01-02"},
		{"iso to us", FormatUSDate, "2024-01-02", "01/02/2024"},
		{"iso to euro", FormatEuroDate, "2024-01-02", "02-01-2024"},
		{"datetime keeps time", FormatISODate, "2024-01-02 15:04:05", "2024-01-02 15:04:05"},
		// No silent truncation of an ISO-prefixed longer string (M17).
		{"iso-prefixed text unchanged", FormatISODate, "2024-01-02XYZ", "2024-01-02XYZ"},
		// No ambiguous DD/MM vs MM/DD guessing — non-ISO input is left alone (M17).
		{"slash date unchanged", FormatUSDate, "01/02/2024", "01/02/2024"},
		{"non-date name unchanged", FormatISODate, "CUST-2025-0001", "CUST-2025-0001"},
		{"plain text unchanged", FormatISODate, "hello world", "hello world"},
		{"short string unchanged", FormatISODate, "abc", "abc"},
	}

	orig := ActiveDateFormat
	defer func() { ActiveDateFormat = orig }()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ActiveDateFormat = tt.dateFormat
			if got := FormatDate(tt.in); got != tt.want {
				t.Errorf("FormatDate(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsSessionAuthAndOAuth(t *testing.T) {
	cases := []struct {
		name                string
		cfg                 SiteConfig
		wantOAuth, wantSess bool
	}{
		{"api key only", SiteConfig{APIKey: "k", APISecret: "s"}, false, false},
		{"oauth token", SiteConfig{AccessToken: "t"}, true, false},
		{"full session", SiteConfig{Username: "u", Password: "p"}, false, true},
		{"username only is not session", SiteConfig{Username: "u"}, false, false},
		{"password only is not session", SiteConfig{Password: "p"}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cfg.IsOAuth(); got != c.wantOAuth {
				t.Errorf("IsOAuth() = %v, want %v", got, c.wantOAuth)
			}
			if got := c.cfg.IsSessionAuth(); got != c.wantSess {
				t.Errorf("IsSessionAuth() = %v, want %v", got, c.wantSess)
			}
		})
	}
}
