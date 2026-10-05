package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// ─── Config structs ───────────────────────────────────────────────────────────

// SiteConfig holds connection details for a single Frappe site.
type SiteConfig struct {
	// Name is populated by Load at runtime; it is not persisted to YAML.
	Name string `yaml:"-"`

	URL       string `yaml:"url"`
	APIKey    string `yaml:"api_key,omitempty"`
	APISecret string `yaml:"api_secret,omitempty"`

	// OAuth 2.0 fields (Authorization Code + PKCE).
	OAuthClientID     string `yaml:"oauth_client_id,omitempty"`
	OAuthClientSecret string `yaml:"oauth_client_secret,omitempty"`
	AccessToken       string `yaml:"access_token,omitempty"`
	RefreshToken      string `yaml:"refresh_token,omitempty"`
	TokenExpiry       int64  `yaml:"token_expiry,omitempty"`

	// Username/password session auth (POST /api/method/login, cookie-based).
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`

	// MCP is the policy `ffc mcp` enforces for this site; nil means the
	// defaults (see internal/cmd/mcp_policy.go).
	MCP *MCPPolicy `yaml:"mcp,omitempty"`
}

// MCPPolicy limits what MCP tools may do on a site. DocType and tool names
// match exactly (DocTypes ignoring case); a method entry ending in "*" is a
// prefix.
type MCPPolicy struct {
	// ReadOnly registers and allows only read tools.
	ReadOnly bool `yaml:"read_only,omitempty"`
	// AllowTools, when set, is the only tools that are registered.
	AllowTools []string `yaml:"allow_tools,omitempty"`
	// AllowDoctypes, when set, is the only DocTypes tools may touch. It is
	// also the only way to let MCP write to a sensitive DocType.
	AllowDoctypes []string `yaml:"allow_doctypes,omitempty"`
	DenyDoctypes  []string `yaml:"deny_doctypes,omitempty"`
	// AllowMethods, when set, is the only methods call_method may call. It is
	// also the only way to call a method on the built-in deny list.
	AllowMethods []string `yaml:"allow_methods,omitempty"`
	DenyMethods  []string `yaml:"deny_methods,omitempty"`
}

// mcpPolicyKeys are the keys MCPPolicy accepts. A misspelt key is an error,
// not ignored: a policy that silently does not apply fails open.
var mcpPolicyKeys = map[string]bool{
	"read_only": true, "allow_tools": true, "allow_doctypes": true, "deny_doctypes": true,
	"allow_methods": true, "deny_methods": true,
}

// UnmarshalYAML decodes a policy, refusing unknown keys.
func (p *MCPPolicy) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			if k := node.Content[i].Value; !mcpPolicyKeys[k] {
				return fmt.Errorf("line %d: unknown mcp policy key %q", node.Content[i].Line, k)
			}
		}
	}
	type plain MCPPolicy
	return node.Decode((*plain)(p))
}

// IsOAuth reports whether this site uses OAuth Bearer tokens for authentication.
func (s *SiteConfig) IsOAuth() bool {
	return s.AccessToken != ""
}

// IsSessionAuth reports whether this site uses username/password session-cookie
// authentication (POST /api/method/login on every client construction).
func (s *SiteConfig) IsSessionAuth() bool {
	return s.Username != "" && s.Password != ""
}

// IsTokenExpired reports whether the OAuth access token has expired
// (with a 60-second safety margin).
func (s *SiteConfig) IsTokenExpired() bool {
	if s.TokenExpiry == 0 {
		return false
	}
	return time.Now().Unix() > s.TokenExpiry-60
}

// Config is the top-level config structure.
type Config struct {
	DefaultSite  string                `yaml:"default_site"`
	NumberFormat NumberFormat          `yaml:"number_format"`
	DateFormat   DateFormat            `yaml:"date_format"`
	Sites        map[string]SiteConfig `yaml:"sites"`
}

// ─── Loading ──────────────────────────────────────────────────────────────────

// Read parses the config file at path. The file is decoded with yaml.v3 (not
// viper) so site names keep their case and may contain dots: viper lowercases
// map keys and splits them on ".", which made sites such as "Prod" or
// "erp.example.com" impossible to load even though init/site add accepted them.
func Read(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &cfg, nil
}

// Load reads the config file and returns the SiteConfig for the requested site.
// siteFlag selects the site; if empty, DefaultSite is used.
// configPath overrides the default config file location.
func Load(siteFlag, configPath string) (*SiteConfig, error) {
	path := configPath
	if path == "" {
		p, err := DefaultConfigPath()
		if err != nil {
			return nil, fmt.Errorf("cannot determine config directory: %w", err)
		}
		path = p
	}

	cfg, err := Read(path)
	if err != nil {
		// Only the implicit default path falls back to env vars; an explicit
		// --config that does not exist is an error.
		if errors.Is(err, fs.ErrNotExist) && configPath == "" {
			return loadFromEnv()
		}
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("reading config: %w", err)
		}
		return nil, err
	}

	applyFormats(cfg)

	siteName := siteFlag
	if siteName == "" {
		siteName = cfg.DefaultSite
	}
	if siteName == "" {
		return nil, fmt.Errorf("no site selected: set 'default_site' in config or use --site flag")
	}

	site, ok := cfg.Sites[siteName]
	if !ok {
		// Before viper was removed, lookups ignored case; keep `--site prod`
		// working for a site named "Prod" when the match is unambiguous.
		var matches []string
		for name := range cfg.Sites {
			if strings.EqualFold(name, siteName) {
				matches = append(matches, name)
			}
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("site %q not found in config", siteName)
		}
		siteName = matches[0]
		site = cfg.Sites[siteName]
	}
	if site.URL == "" {
		return nil, fmt.Errorf("site %q has no URL configured", siteName)
	}

	if err := applyEnvOverrides(&site); err != nil {
		return nil, err
	}
	site.Name = siteName
	return &site, nil
}

// applyFormats sets the package-level display formats from cfg, warning about
// (and ignoring) unknown values instead of silently rendering them as plain.
func applyFormats(cfg *Config) {
	ActiveFormat = FormatFrench
	switch {
	case cfg.NumberFormat == "":
	case cfg.NumberFormat.Valid():
		ActiveFormat = cfg.NumberFormat
	default:
		fmt.Fprintf(os.Stderr, "warning: unknown number_format %q in config, using %q\n", cfg.NumberFormat, FormatFrench)
	}

	ActiveDateFormat = FormatISODate
	switch {
	case cfg.DateFormat == "":
	case cfg.DateFormat.Valid():
		ActiveDateFormat = cfg.DateFormat
	default:
		fmt.Fprintf(os.Stderr, "warning: unknown date_format %q in config, using %q\n", cfg.DateFormat, FormatISODate)
	}
}

// applyEnvOverrides lets FFC_URL / FFC_API_KEY / FFC_API_SECRET override a
// configured site. Env credentials are only honoured as a complete key+secret
// pair, and then they replace every stored credential (otherwise the stored
// OAuth token or password would silently win in client.New). FFC_URL is only
// applied together with env credentials: redirecting a stored API secret,
// Bearer token or password to another host would leak it. FFC_URL alone is an
// error rather than a warning, so a script meant for another host never runs
// against the stored one.
func applyEnvOverrides(site *SiteConfig) error {
	key, secret := os.Getenv("FFC_API_KEY"), os.Getenv("FFC_API_SECRET")
	envCreds := key != "" && secret != ""
	if envCreds {
		*site = SiteConfig{URL: site.URL, APIKey: key, APISecret: secret, MCP: site.MCP}
	} else if key != "" || secret != "" {
		fmt.Fprintln(os.Stderr, "warning: FFC_API_KEY and FFC_API_SECRET must be set together; ignoring them")
	}
	if u := os.Getenv("FFC_URL"); u != "" && u != site.URL {
		if !envCreds {
			return fmt.Errorf("FFC_URL (%s) differs from the site URL (%s) and only applies together with FFC_API_KEY and FFC_API_SECRET, so stored credentials are never sent to another host: set all three, or unset FFC_URL", u, site.URL)
		}
		site.URL = u
	}
	return nil
}

// loadFromEnv constructs a SiteConfig purely from environment variables.
func loadFromEnv() (*SiteConfig, error) {
	url := os.Getenv("FFC_URL")
	if url == "" {
		return nil, fmt.Errorf(
			"no config file found and FFC_URL is not set\n" +
				"Create ~/.config/ffc/config.yaml (ffc init) or set FFC_URL, FFC_API_KEY, FFC_API_SECRET",
		)
	}
	return &SiteConfig{
		URL:       url,
		APIKey:    os.Getenv("FFC_API_KEY"),
		APISecret: os.Getenv("FFC_API_SECRET"),
	}, nil
}

// ─── Paths ────────────────────────────────────────────────────────────────────

// DefaultConfigDir returns ~/.config/ffc, the directory holding config.yaml
// and ffc's state files.
func DefaultConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ffc"), nil
}

// DefaultConfigPath returns the full path to the default config file.
func DefaultConfigPath() (string, error) {
	dir, err := DefaultConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}
