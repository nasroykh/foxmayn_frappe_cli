package cmd

import (
	"testing"
)

func TestIsDevBuild(t *testing.T) {
	tests := map[string]bool{
		"v0.1.0":            false,
		"0.1.0":             false,
		"v1.20.3":           false,
		"dev":               true,
		"":                  true,
		"abc1234":           true, // git describe --always
		"v0.1.0-3-gdeadbee": true, // git describe with commits ahead
		"v0.1.0-dirty":      true,
		"v0.1.0-rc1":        true, // a pre-release is not a clean release tag
	}
	for v, want := range tests {
		if got := isDevBuild(v); got != want {
			t.Errorf("isDevBuild(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestNewerThan(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"v1.0.0", "v1.0.1", true},
		{"v1.0.0", "v1.1.0", true},
		{"v1.0.0", "v2.0.0", true},
		{"v1.2.0", "v1.2.0", false},
		{"v2.0.0", "v1.9.9", false},
		{"1.0.0", "1.0.1", true}, // v-prefix optional
		// Pre-release handling (L30): the final release supersedes its rc…
		{"v1.6.0-rc1", "v1.6.0", true},
		// …but we never offer a pre-release as an update over the final.
		{"v1.6.0", "v1.6.0-rc1", false},
	}
	for _, tt := range tests {
		if got := newerThan(tt.current, tt.latest); got != tt.want {
			t.Errorf("newerThan(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestClassifyUpdate(t *testing.T) {
	tests := []struct {
		current, latest string
		want            updateKind
	}{
		{"v1.5.0", "v1.5.0", kindUpToDate},
		{"v1.5.0", "v1.6.0", kindUpdate},
		{"v1.6.0", "v1.5.0", kindDowngrade},
		{"v1.6.0-rc1", "v1.5.0", kindDowngrade}, // rc ahead of latest stable
		{"v1.6.0-rc1", "v1.6.0", kindUpdate},    // final supersedes its rc
		{"v1.5.0-rc1", "v1.6.0", kindUpdate},
		{"v1.2.0-3-gabc1234", "v1.2.0", kindDowngrade}, // ahead of the tag
		{"v1.2.0-3-gabc1234", "v1.3.0", kindUpdate},
		{"v1.2.0-dirty", "v1.2.0", kindDowngrade},
		{"abc1234", "v1.2.0", kindDev},
		{"dev", "v1.2.0", kindDev},
		{"", "v1.2.0", kindDev},
	}
	for _, tt := range tests {
		if got := classifyUpdate(tt.current, tt.latest); got != tt.want {
			t.Errorf("classifyUpdate(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}
