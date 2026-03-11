package cmd

import (
	"errors"
	"fmt"
	"os"
	"sort"
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

		raw, err := os.ReadFile(cfgPath)
		if err != nil {
			return fmt.Errorf("reading config %s: %w. Make sure it exists or use ffc init.", cfgPath, err)
		}

		var root yaml.Node
		if err := yaml.Unmarshal(raw, &root); err != nil {
			return fmt.Errorf("parsing config: %w", err)
		}

		var vConfig config.Config
		if err := yaml.Unmarshal(raw, &vConfig); err != nil {
			return fmt.Errorf("parsing config values: %w", err)
		}

		var action string
		for {
			err = huh.NewForm(
				huh.NewGroup(
					huh.NewSelect[string]().
						Title("ffc Configuration").
						Description(fmt.Sprintf(
							"Site: %s   Numbers: %s   Dates: %s",
							orDefault(vConfig.DefaultSite, "none"),
							orDefault(string(vConfig.NumberFormat), "french"),
							orDefault(string(vConfig.DateFormat), "yyyy-mm-dd"),
						)).
						Options(
							huh.NewOption("Set Default Site", "site"),
							huh.NewOption("Set Number Format", "number"),
							huh.NewOption("Set Date Format", "date"),
							huh.NewOption("Save and Exit", "save"),
							huh.NewOption("Cancel (discard changes)", "cancel"),
						).
						Value(&action),
				),
			).WithKeyMap(escQuitKeyMap()).Run()

			if err != nil || action == "cancel" {
				return nil
			}

			if action == "save" {
				break
			}

			switch action {
			case "site":
				var siteNames []string
				for name := range vConfig.Sites {
					siteNames = append(siteNames, name)
				}
				sort.Strings(siteNames)

				if len(siteNames) == 0 {
					output.PrintError("No sites configured in config.yaml")
					continue
				}

				opts := make([]huh.Option[string], len(siteNames))
				for i, name := range siteNames {
					label := name
					if name == vConfig.DefaultSite {
						label += "  (current)"
					}
					opts[i] = huh.NewOption(label, name)
				}

				chosen := vConfig.DefaultSite
				err = huh.NewForm(
					huh.NewGroup(
						huh.NewSelect[string]().
							Title("Choose Default Site").
							Description("Press esc to go back").
							Options(opts...).
							Value(&chosen),
					),
				).WithKeyMap(escQuitKeyMap()).Run()

				if errors.Is(err, huh.ErrUserAborted) {
					continue
				}
				if err != nil {
					return err
				}
				if chosen != "" {
					vConfig.DefaultSite = chosen
					updateYAMLValue(&root, "default_site", chosen)
				}

			case "number":
				opts := make([]huh.Option[string], len(config.AllFormats))
				for i, f := range config.AllFormats {
					label := fmt.Sprintf("%-20s  e.g. %s", f.Label, f.Example)
					if f.Key == vConfig.NumberFormat {
						label += "  (current)"
					}
					opts[i] = huh.NewOption(label, string(f.Key))
				}

				chosen := string(vConfig.NumberFormat)
				err = huh.NewForm(
					huh.NewGroup(
						huh.NewSelect[string]().
							Title("Choose Number Format").
							Description("Press esc to go back").
							Options(opts...).
							Value(&chosen),
					),
				).WithKeyMap(escQuitKeyMap()).Run()

				if errors.Is(err, huh.ErrUserAborted) {
					continue
				}
				if err != nil {
					return err
				}
				if chosen != "" {
					vConfig.NumberFormat = config.NumberFormat(chosen)
					updateYAMLValue(&root, "number_format", chosen)
				}

			case "date":
				opts := make([]huh.Option[string], len(config.AllDateFormats))
				for i, f := range config.AllDateFormats {
					label := fmt.Sprintf("%-25s e.g. %s", f.Label, f.Example)
					if f.Key == vConfig.DateFormat {
						label += "  (current)"
					}
					opts[i] = huh.NewOption(label, string(f.Key))
				}

				chosen := string(vConfig.DateFormat)
				err = huh.NewForm(
					huh.NewGroup(
						huh.NewSelect[string]().
							Title("Choose Date Format").
							Description("Press esc to go back").
							Options(opts...).
							Value(&chosen),
					),
				).WithKeyMap(escQuitKeyMap()).Run()

				if errors.Is(err, huh.ErrUserAborted) {
					continue
				}
				if err != nil {
					return err
				}
				if chosen != "" {
					vConfig.DateFormat = config.DateFormat(chosen)
					updateYAMLValue(&root, "date_format", chosen)
				}
			}
		}

		return saveConfig(cfgPath, raw, &root)
	},
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

		raw, err := os.ReadFile(cfgPath)
		if err != nil {
			return fmt.Errorf("reading config %s: %w", cfgPath, err)
		}

		var vConfig config.Config
		if err := yaml.Unmarshal(raw, &vConfig); err != nil {
			return fmt.Errorf("parsing config: %w", err)
		}

		data := map[string]interface{}{
			"default_site":  orDefault(vConfig.DefaultSite, "(not set)"),
			"number_format": orDefault(string(vConfig.NumberFormat), "french"),
			"date_format":   orDefault(string(vConfig.DateFormat), "yyyy-mm-dd"),
		}
		fields := []string{"default_site", "number_format", "date_format"}

		switch {
		case jsonOutput:
			ordered := make([]map[string]interface{}, 0, len(fields))
			_ = ordered
			// Build ordered map for JSON output
			jsonData := map[string]string{
				"default_site":  data["default_site"].(string),
				"number_format": data["number_format"].(string),
				"date_format":   data["date_format"].(string),
			}
			output.PrintJSON(jsonData)

		case cgYAML:
			fmt.Printf("default_site: %s\nnumber_format: %s\ndate_format: %s\n",
				data["default_site"],
				data["number_format"],
				data["date_format"],
			)

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

		raw, err := os.ReadFile(cfgPath)
		if err != nil {
			return fmt.Errorf("reading config %s: %w", cfgPath, err)
		}

		var root yaml.Node
		if err := yaml.Unmarshal(raw, &root); err != nil {
			return fmt.Errorf("parsing config: %w", err)
		}

		var vConfig config.Config
		if err := yaml.Unmarshal(raw, &vConfig); err != nil {
			return fmt.Errorf("parsing config values: %w", err)
		}

		if siteChanged {
			if _, ok := vConfig.Sites[csDefaultSite]; !ok {
				var names []string
				for n := range vConfig.Sites {
					names = append(names, n)
				}
				sort.Strings(names)
				return fmt.Errorf("site %q not found in config (available: %s)", csDefaultSite, strings.Join(names, ", "))
			}
			updateYAMLValue(&root, "default_site", csDefaultSite)
			output.PrintSuccess(fmt.Sprintf("default_site → %s", csDefaultSite))
		}
		if numberChanged {
			updateYAMLValue(&root, "number_format", csNumberFormat)
			output.PrintSuccess(fmt.Sprintf("number_format → %s", csNumberFormat))
		}
		if dateChanged {
			updateYAMLValue(&root, "date_format", csDateFormat)
			output.PrintSuccess(fmt.Sprintf("date_format → %s", csDateFormat))
		}

		return saveConfig(cfgPath, raw, &root)
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

// saveConfig marshals the YAML node and saves it to disk, preserving any
// leading comment header from the original file.
func saveConfig(path string, original []byte, root *yaml.Node) error {
	out, err := yaml.Marshal(root.Content[0])
	if err != nil {
		return fmt.Errorf("serialising config: %w", err)
	}
	final := preserveHeader(original, out)
	if err := os.WriteFile(path, []byte(final), 0o644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	output.PrintSuccess(fmt.Sprintf("Configuration saved to %s", path))
	return nil
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
	return fmt.Errorf("invalid number format %q; valid values: %s", s, strings.Join(valid, ", "))
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
	return fmt.Errorf("invalid date format %q; valid values: %s", s, strings.Join(valid, ", "))
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

// updateYAMLValue finds a key in a YAML mapping node and updates its value.
// If the key doesn't exist, it appends it.
func updateYAMLValue(root *yaml.Node, key, value string) {
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return
	}
	mapping := root.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].Value = value
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
	valNode := &yaml.Node{Kind: yaml.ScalarNode, Value: value}
	mapping.Content = append(mapping.Content, keyNode, valNode)
}

// preserveHeader tries to extract leading comments from the original file.
func preserveHeader(original, marshaled []byte) string {
	rawStr := string(original)
	if !strings.HasPrefix(rawStr, "#") {
		return string(marshaled)
	}
	lines := strings.Split(rawStr, "\n")
	var commentLines []string
	for _, l := range lines {
		if strings.HasPrefix(l, "#") || l == "" {
			commentLines = append(commentLines, l)
		} else {
			break
		}
	}
	return strings.Join(commentLines, "\n") + "\n" + string(marshaled)
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
