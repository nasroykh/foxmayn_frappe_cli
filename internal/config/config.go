package config

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// ─── Number format ────────────────────────────────────────────────────────────

// NumberFormat defines the display style for numeric values.
type NumberFormat string

const (
	// FormatFrench  1 000 000,00  (non-breaking space thousands, comma decimal) — DEFAULT
	FormatFrench NumberFormat = "french"
	// FormatUS      1,000,000.00  (comma thousands, period decimal)
	FormatUS NumberFormat = "us"
	// FormatGerman  1.000.000,00  (period thousands, comma decimal)
	FormatGerman NumberFormat = "german"
	// FormatPlain   1000000.00    (no grouping, period decimal)
	FormatPlain NumberFormat = "plain"
)

// AllFormats lists every supported number format.
var AllFormats = []struct {
	Key     NumberFormat
	Label   string
	Example string
}{
	{FormatFrench, "French / European", "1 000 000,00"},
	{FormatUS, "US / English", "1,000,000.00"},
	{FormatGerman, "German / Spanish", "1.000.000,00"},
	{FormatPlain, "Plain (no grouping)", "1000000.00"},
}

// ─── Date format ──────────────────────────────────────────────────────────────

// DateFormat defines the display style for dates.
type DateFormat string

const (
	// FormatISODate YYYY-MM-DD (Frappe default)
	FormatISODate DateFormat = "yyyy-mm-dd"
	// FormatEuroDate DD-MM-YYYY
	FormatEuroDate DateFormat = "dd-mm-yyyy"
	// FormatEuroSlashDate DD/MM/YYYY
	FormatEuroSlashDate DateFormat = "dd/mm/yyyy"
	// FormatUSDate   MM/DD/YYYY
	FormatUSDate DateFormat = "mm/dd/yyyy"
)

// AllDateFormats lists every supported date format.
var AllDateFormats = []struct {
	Key     DateFormat
	Label   string
	Example string
}{
	{FormatISODate, "ISO (YYYY-MM-DD)", "2025-12-31"},
	{FormatEuroDate, "European (DD-MM-YYYY)", "31-12-2025"},
	{FormatEuroSlashDate, "European (DD/MM/YYYY)", "31/12/2025"},
	{FormatUSDate, "US (MM/DD/YYYY)", "12/31/2025"},
}

// ActiveFormat and ActiveDateFormat are the formats used by the output layer.
var ActiveFormat NumberFormat = FormatFrench
var ActiveDateFormat DateFormat = FormatISODate

// FormatNumber formats f according to the active number format. Integral values
// are rendered without a decimal part; fractional values keep 2 decimals.
func FormatNumber(f float64) string {
	abs := math.Abs(f)

	// int64(abs) is undefined once abs exceeds the int64 range (~9.2e18); fall
	// back to a plain, ungrouped representation to avoid garbage output (L19).
	if abs >= float64(math.MaxInt64) {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}

	intPart := int64(abs)
	fracInt := int(math.Round((abs - float64(intPart)) * 100))
	if fracInt >= 100 { // carry, e.g. 1.999 → fracInt 100 (M13)
		intPart++
		fracInt -= 100
	}

	intStr := groupDigits(intPart, thousandsSep(ActiveFormat))
	result := intStr
	if fracInt != 0 { // only show decimals for non-integral values
		result = fmt.Sprintf("%s%s%02d", intStr, decimalSep(ActiveFormat), fracInt)
	}
	if f < 0 && (intPart != 0 || fracInt != 0) { // avoid "-0"
		result = "-" + result
	}
	return result
}

// dateOutLayout returns the output layout for the active date format, appending
// a time component when hasTime is set.
func dateOutLayout(hasTime bool) string {
	var l string
	switch ActiveDateFormat {
	case FormatEuroDate:
		l = "02-01-2006"
	case FormatEuroSlashDate:
		l = "02/01/2006"
	case FormatUSDate:
		l = "01/02/2006"
	default:
		l = "2006-01-02"
	}
	if hasTime {
		l += " 15:04:05"
	}
	return l
}

// FormatDate converts a Frappe date/datetime string into the active format.
// Frappe always stores dates in ISO form, so only exact ISO matches are
// reformatted — the function never guesses DD/MM vs MM/DD and never truncates a
// longer string down to a date prefix (M17). Anything else is returned as-is.
func FormatDate(s string) string {
	for _, layout := range []string{
		"2006-01-02 15:04:05.000000",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format(dateOutLayout(true))
		}
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Format(dateOutLayout(false))
	}
	return s
}

func thousandsSep(nf NumberFormat) string {
	switch nf {
	case FormatFrench:
		return "\u00a0" // non-breaking space
	case FormatUS:
		return ","
	case FormatGerman:
		return "."
	default:
		return ""
	}
}

func decimalSep(nf NumberFormat) string {
	switch nf {
	case FormatFrench, FormatGerman:
		return ","
	default:
		return "."
	}
}

// groupDigits formats n inserting sep every 3 digits from the right.
func groupDigits(n int64, sep string) string {
	s := fmt.Sprintf("%d", n)
	if sep == "" || len(s) <= 3 {
		return s
	}
	var b strings.Builder
	start := len(s) % 3
	if start > 0 {
		b.WriteString(s[:start])
	}
	for i := start; i < len(s); i += 3 {
		if i > 0 || start > 0 {
			b.WriteString(sep)
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// ─── Config structs ───────────────────────────────────────────────────────────

// SiteConfig holds connection details for a single Frappe site.
type SiteConfig struct {
	// Name is populated by Load at runtime; it is not persisted to YAML.
	Name string `mapstructure:"-" yaml:"-"`

	URL       string `mapstructure:"url" yaml:"url"`
	APIKey    string `mapstructure:"api_key" yaml:"api_key,omitempty"`
	APISecret string `mapstructure:"api_secret" yaml:"api_secret,omitempty"`

	// OAuth 2.0 fields (Authorization Code + PKCE).
	OAuthClientID     string `mapstructure:"oauth_client_id" yaml:"oauth_client_id,omitempty"`
	OAuthClientSecret string `mapstructure:"oauth_client_secret" yaml:"oauth_client_secret,omitempty"`
	AccessToken       string `mapstructure:"access_token" yaml:"access_token,omitempty"`
	RefreshToken      string `mapstructure:"refresh_token" yaml:"refresh_token,omitempty"`
	TokenExpiry       int64  `mapstructure:"token_expiry" yaml:"token_expiry,omitempty"`

	// Username/password session auth (POST /api/method/login, cookie-based).
	Username string `mapstructure:"username" yaml:"username,omitempty"`
	Password string `mapstructure:"password" yaml:"password,omitempty"`
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
	DefaultSite  string                `mapstructure:"default_site" yaml:"default_site"`
	NumberFormat NumberFormat          `mapstructure:"number_format" yaml:"number_format"`
	DateFormat   DateFormat            `mapstructure:"date_format" yaml:"date_format"`
	Sites        map[string]SiteConfig `mapstructure:"sites" yaml:"sites"`
}

// ─── Loading ──────────────────────────────────────────────────────────────────

// Load reads the config file and returns the SiteConfig for the requested site.
// siteFlag selects the site; if empty, DefaultSite is used.
// configPath overrides the default config file location.
func Load(siteFlag, configPath string) (*SiteConfig, error) {
	v := viper.New()

	// Resolve config file path.
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		cfgDir, err := defaultConfigDir()
		if err != nil {
			return nil, fmt.Errorf("cannot determine config directory: %w", err)
		}
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(cfgDir)
	}

	// Env var overrides for the documented FFC_URL/FFC_API_KEY/FFC_API_SECRET
	// are applied explicitly below; viper's AutomaticEnv does not resolve the
	// nested sites.<name>.* keys those would need, so it is not wired here (I3).

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			return loadFromEnv()
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Apply number format globally (default to french if unset).
	if cfg.NumberFormat != "" {
		ActiveFormat = cfg.NumberFormat
	} else {
		ActiveFormat = FormatFrench
	}

	// Apply date format globally (default to ISO if unset).
	if cfg.DateFormat != "" {
		ActiveDateFormat = cfg.DateFormat
	} else {
		ActiveDateFormat = FormatISODate
	}

	// Pick the site.
	siteName := siteFlag
	if siteName == "" {
		siteName = cfg.DefaultSite
	}
	if siteName == "" {
		return nil, fmt.Errorf("no site selected: set 'default_site' in config or use --site flag")
	}

	site, ok := cfg.Sites[siteName]
	if !ok {
		return nil, fmt.Errorf("site %q not found in config", siteName)
	}
	if site.URL == "" {
		return nil, fmt.Errorf("site %q has no URL configured", siteName)
	}

	// Allow env vars to override individual site credentials.
	// The URL override is skipped for OAuth/session sites: redirecting a stored
	// Bearer token or account password to a different host would leak it (L6).
	if url := os.Getenv("FFC_URL"); url != "" {
		if site.IsOAuth() || site.IsSessionAuth() {
			fmt.Fprintln(os.Stderr, "warning: FFC_URL ignored for this site (OAuth/session auth is bound to its configured URL)")
		} else {
			site.URL = url
		}
	}
	if key := os.Getenv("FFC_API_KEY"); key != "" {
		site.APIKey = key
	}
	if secret := os.Getenv("FFC_API_SECRET"); secret != "" {
		site.APISecret = secret
	}

	site.Name = siteName
	return &site, nil
}

// loadFromEnv constructs a SiteConfig purely from environment variables.
func loadFromEnv() (*SiteConfig, error) {
	url := os.Getenv("FFC_URL")
	if url == "" {
		return nil, fmt.Errorf(
			"no config file found and FFC_URL is not set\n" +
				"Create ~/.config/ffc/config.yaml or set FFC_URL, FFC_API_KEY, FFC_API_SECRET",
		)
	}
	return &SiteConfig{
		URL:       url,
		APIKey:    os.Getenv("FFC_API_KEY"),
		APISecret: os.Getenv("FFC_API_SECRET"),
	}, nil
}

// ─── Paths ────────────────────────────────────────────────────────────────────

// defaultConfigDir returns ~/.config/ffc.
func defaultConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ffc"), nil
}

// DefaultConfigPath returns the full path to the default config file.
func DefaultConfigPath() (string, error) {
	dir, err := defaultConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}
