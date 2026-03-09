package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// SiteConfig holds connection details for a single Frappe site.
type SiteConfig struct {
	URL       string `mapstructure:"url"`
	APIKey    string `mapstructure:"api_key"`
	APISecret string `mapstructure:"api_secret"`
}

// Config is the top-level config structure.
type Config struct {
	DefaultSite string                `mapstructure:"default_site"`
	Sites       map[string]SiteConfig `mapstructure:"sites"`
}

// Load reads the config file and returns the SiteConfig for the requested site.
// siteFlag selects the site; if empty, DefaultSite is used.
// configPath overrides the default config file location.
func Load(siteFlag, configPath string) (*SiteConfig, error) {
	v := viper.New()

	// Resolve config file path
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

	// Env var overrides (single-site shortcut, useful for CI)
	v.SetEnvPrefix("FFC")
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		// If no config file found, check for bare env vars
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			return loadFromEnv()
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	// Pick the site
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

	// Allow env vars to override individual site credentials
	if url := os.Getenv("FFC_URL"); url != "" {
		site.URL = url
	}
	if key := os.Getenv("FFC_API_KEY"); key != "" {
		site.APIKey = key
	}
	if secret := os.Getenv("FFC_API_SECRET"); secret != "" {
		site.APISecret = secret
	}

	return &site, nil
}

// loadFromEnv constructs a SiteConfig purely from environment variables.
// Useful when no config file exists (e.g. CI pipelines).
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

// defaultConfigDir returns ~/.config/ffc
func defaultConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ffc"), nil
}
