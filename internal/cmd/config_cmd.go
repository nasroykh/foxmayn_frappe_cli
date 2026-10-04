package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

// ─── config get flags ─────────────────────────────────────────────────────────

var cgYAML bool

// ─── config set flags ─────────────────────────────────────────────────────────

var (
	csDefaultSite  string
	csNumberFormat string
	csDateFormat   string
)

// ─── ffc config (TUI) ─────────────────────────────────────────────────────────

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage ffc settings (TUI or subcommands)",
	Long: `Open an interactive TUI to manage ffc settings, or use subcommands to
read and write settings non-interactively.

Settings are saved to your config.yaml file.

Examples:
  ffc config                                          # interactive TUI
  ffc config get                                      # show all settings
  ffc config get --json                               # show as JSON
  ffc config set --default-site dev                   # set default site
  ffc config set --number-format us --date-format dd/mm/yyyy
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, err := resolveCfgPath()
		if err != nil {
			return err
		}
		orig, err := readConfig(cfgPath)
		if err != nil {
			return err
		}
		cur := *orig

		for {
			var action string
			if err := runForm(huh.NewGroup(
				huh.NewSelect[string]().
					Title("ffc Configuration").
					Description(fmt.Sprintf(
						"Site: %s   Numbers: %s   Dates: %s",
						orDefault(cur.DefaultSite, "none"),
						orDefault(string(cur.NumberFormat), string(config.FormatFrench)),
						orDefault(string(cur.DateFormat), string(config.FormatISODate)),
					)).
					Options(
						huh.NewOption("Set Default Site", "site"),
						huh.NewOption("Set Number Format", "number"),
						huh.NewOption("Set Date Format", "date"),
						huh.NewOption("Save and Exit", "save"),
						huh.NewOption("Cancel (discard changes)", "cancel"),
					).
					Value(&action),
			)); err != nil {
				return err
			}

			switch action {
			case "cancel":
				fmt.Fprintln(os.Stderr, "Changes discarded.")
				return nil

			case "save":
				var changes []configValue
				if cur.DefaultSite != orig.DefaultSite {
					changes = append(changes, configValue{"default_site", cur.DefaultSite})
				}
				if cur.NumberFormat != orig.NumberFormat {
					changes = append(changes, configValue{"number_format", string(cur.NumberFormat)})
				}
				if cur.DateFormat != orig.DateFormat {
					changes = append(changes, configValue{"date_format", string(cur.DateFormat)})
				}
				if len(changes) == 0 {
					fmt.Fprintln(os.Stderr, "No changes to save.")
					return nil
				}
				if err := setConfigValues(cfgPath, changes); err != nil {
					return err
				}
				output.PrintSuccess(fmt.Sprintf("Configuration saved to %s", cfgPath))
				return nil

			case "site":
				names := sortedSiteNames(&cur)
				if len(names) == 0 {
					output.PrintError("No sites configured in config.yaml")
					continue
				}
				opts := make([]huh.Option[string], len(names))
				for i, name := range names {
					label := name
					if name == cur.DefaultSite {
						label += "  (current)"
					}
					opts[i] = huh.NewOption(label, name)
				}
				if chosen, err := pickSetting("Choose Default Site", cur.DefaultSite, opts); err != nil {
					return err
				} else if chosen != "" {
					cur.DefaultSite = chosen
				}

			case "number":
				opts := make([]huh.Option[string], len(config.AllFormats))
				for i, f := range config.AllFormats {
					label := fmt.Sprintf("%-20s  e.g. %s", f.Label, f.Example)
					if f.Key == cur.NumberFormat {
						label += "  (current)"
					}
					opts[i] = huh.NewOption(label, string(f.Key))
				}
				if chosen, err := pickSetting("Choose Number Format", string(cur.NumberFormat), opts); err != nil {
					return err
				} else if chosen != "" {
					cur.NumberFormat = config.NumberFormat(chosen)
				}

			case "date":
				opts := make([]huh.Option[string], len(config.AllDateFormats))
				for i, f := range config.AllDateFormats {
					label := fmt.Sprintf("%-25s e.g. %s", f.Label, f.Example)
					if f.Key == cur.DateFormat {
						label += "  (current)"
					}
					opts[i] = huh.NewOption(label, string(f.Key))
				}
				if chosen, err := pickSetting("Choose Date Format", string(cur.DateFormat), opts); err != nil {
					return err
				} else if chosen != "" {
					cur.DateFormat = config.DateFormat(chosen)
				}
			}
		}
	},
}

// pickSetting shows a sub-menu of the TUI. Esc goes back to the main menu
// (returns "", nil); other errors are returned.
func pickSetting(title, current string, opts []huh.Option[string]) (string, error) {
	chosen := current
	err := runForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title(title).
			Description("Press esc to go back").
			Options(opts...).
			Value(&chosen),
	))
	if errors.Is(err, errAborted) {
		return "", nil
	}
	return chosen, err
}

// ─── ffc config get ───────────────────────────────────────────────────────────

var configGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Show current ffc configuration settings",
	Long: `Print all ffc configuration settings.

Output defaults to a styled table. Use --json or --yaml for machine-readable output.

Examples:
  ffc config get
  ffc config get --json
  ffc config get --yaml
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, err := resolveCfgPath()
		if err != nil {
			return err
		}

		vConfig, err := readConfig(cfgPath)
		if err != nil {
			return err
		}

		data := map[string]interface{}{
			"default_site":  orDefault(vConfig.DefaultSite, "(not set)"),
			"number_format": orDefault(string(vConfig.NumberFormat), "french"),
			"date_format":   orDefault(string(vConfig.DateFormat), "yyyy-mm-dd"),
		}
		fields := []string{"default_site", "number_format", "date_format"}

		switch {
		case machineOutput():
			jsonData := map[string]string{
				"default_site":  data["default_site"].(string),
				"number_format": data["number_format"].(string),
				"date_format":   data["date_format"].(string),
			}
			if err := printResult(jsonData); err != nil {
				return err
			}

		case cgYAML:
			// yaml.Marshal quotes values such as "#dev" that would otherwise
			// be read back as a comment.
			out, err := yaml.Marshal(struct {
				DefaultSite  string `yaml:"default_site"`
				NumberFormat string `yaml:"number_format"`
				DateFormat   string `yaml:"date_format"`
			}{
				data["default_site"].(string),
				data["number_format"].(string),
				data["date_format"].(string),
			})
			if err != nil {
				return fmt.Errorf("encoding YAML: %w", err)
			}
			if _, err := os.Stdout.Write(out); err != nil {
				return err
			}

		default:
			output.PrintDocTable(data, fields)
		}

		return nil
	},
}

// ─── ffc config set ───────────────────────────────────────────────────────────

var configSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Update one or more ffc configuration settings",
	Long: `Set one or more ffc configuration settings directly from the command line.

Valid values:
  --number-format   french | us | german | plain
  --date-format     yyyy-mm-dd | dd-mm-yyyy | dd/mm/yyyy | mm/dd/yyyy

Examples:
  ffc config set --default-site dev
  ffc config set --number-format us
  ffc config set --date-format dd/mm/yyyy
  ffc config set --default-site prod --number-format french --date-format yyyy-mm-dd
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		siteChanged := cmd.Flags().Changed("default-site")
		numberChanged := cmd.Flags().Changed("number-format")
		dateChanged := cmd.Flags().Changed("date-format")

		if !siteChanged && !numberChanged && !dateChanged {
			return fmt.Errorf("no settings specified; use --default-site, --number-format, or --date-format")
		}

		// Validate flag values before touching the file.
		if numberChanged {
			if err := validateNumberFormat(csNumberFormat); err != nil {
				return err
			}
		}
		if dateChanged {
			if err := validateDateFormat(csDateFormat); err != nil {
				return err
			}
		}

		cfgPath, err := resolveCfgPath()
		if err != nil {
			return err
		}
		// Unlike Edit, refuse to create a config that only holds formats.
		if _, err := readConfig(cfgPath); err != nil {
			return err
		}

		var changes []configValue
		if siteChanged {
			changes = append(changes, configValue{"default_site", csDefaultSite})
		}
		if numberChanged {
			changes = append(changes, configValue{"number_format", csNumberFormat})
		}
		if dateChanged {
			changes = append(changes, configValue{"date_format", csDateFormat})
		}
		if err := setConfigValues(cfgPath, changes); err != nil {
			return err
		}
		for _, c := range changes {
			output.PrintSuccess(fmt.Sprintf("%s → %s", c.key, c.value))
		}
		return nil
	},
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// resolveCfgPath returns configPath (global flag) or the default config path.
func resolveCfgPath() (string, error) {
	if configPath != "" {
		return configPath, nil
	}
	p, err := config.DefaultConfigPath()
	if err != nil {
		return "", fmt.Errorf("resolving config path: %w", err)
	}
	return p, nil
}

// readConfig reads the config at path, pointing at `ffc init` when it does
// not exist yet. Parse errors are returned, never ignored.
func readConfig(path string) (*config.Config, error) {
	cfg, err := config.Read(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no config found at %s — run 'ffc init' to create one", path)
	}
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	return cfg, nil
}

// configValue is one top-level setting to write.
type configValue struct {
	key, value string
}

// setConfigValues writes top-level settings under the config lock. A
// default_site must name an existing site. Nothing is written when every
// value is already set.
func setConfigValues(path string, values []configValue) error {
	return config.Edit(path, func(f *config.File) error {
		changed := false
		for _, v := range values {
			if v.key == "default_site" && !f.HasSite(v.value) {
				return fmt.Errorf("site %q not found in config (available: %s)", v.value, strings.Join(f.SiteNames(), ", "))
			}
			if f.Get(v.key) != v.value {
				f.Set(v.key, v.value)
				changed = true
			}
		}
		if !changed {
			return config.ErrUnchanged
		}
		return nil
	})
}

// validateNumberFormat returns an error if s is not a valid number format key.
func validateNumberFormat(s string) error {
	for _, f := range config.AllFormats {
		if string(f.Key) == s {
			return nil
		}
	}
	valid := make([]string, len(config.AllFormats))
	for i, f := range config.AllFormats {
		valid[i] = string(f.Key)
	}
	return usageErrorf("invalid number format %q; valid values: %s", s, strings.Join(valid, ", "))
}

// validateDateFormat returns an error if s is not a valid date format key.
func validateDateFormat(s string) error {
	for _, f := range config.AllDateFormats {
		if string(f.Key) == s {
			return nil
		}
	}
	valid := make([]string, len(config.AllDateFormats))
	for i, f := range config.AllDateFormats {
		valid[i] = string(f.Key)
	}
	return usageErrorf("invalid date format %q; valid values: %s", s, strings.Join(valid, ", "))
}

// escQuitKeyMap returns a keymap with both ctrl+c and esc mapped to Quit.
func escQuitKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
	return km
}

// orDefault returns s if non-empty, otherwise fallback.
func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// ─── init ─────────────────────────────────────────────────────────────────────

func init() {
	// config get
	configGetCmd.Flags().BoolVarP(&cgYAML, "yaml", "y", false, "Output as YAML")

	// config set
	configSetCmd.Flags().StringVar(&csDefaultSite, "default-site", "", "Set the default site name")
	configSetCmd.Flags().StringVar(&csNumberFormat, "number-format", "", "Set number format (french|us|german|plain)")
	configSetCmd.Flags().StringVar(&csDateFormat, "date-format", "", "Set date format (yyyy-mm-dd|dd-mm-yyyy|dd/mm/yyyy|mm/dd/yyyy)")

	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configSetCmd)
	rootCmd.AddCommand(configCmd)
}
