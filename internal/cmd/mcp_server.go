package cmd

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// MCPOptions is everything an in-process MCP server is built from; the
// fields are the options of `ffc mcp`.
type MCPOptions struct {
	ConfigPath string   // config file; "" is the default path
	Site       string   // the default site; "" is the config's default_site
	Sites      []string // serve these sites (like --sites)
	AllSites   bool     // serve every site in the config (like --all-sites)
	// Policy narrows every served site's own policy, exactly as the
	// --allow-*, --deny-* and --confirm flags do; it cannot widen it.
	// Policy.ReadOnly is --read-only. Confirm may be "", "always" or
	// "if-supported"; "never" is only for the config.
	Policy config.MCPPolicy
	// Toolsets limits the tool sets exposed; nil is core and lifecycle.
	Toolsets []string
}

// MCPServer is an MCP server built by NewMCPServer.
type MCPServer struct {
	*server.MCPServer
	// Sites are the served sites, the default one first.
	Sites []string
	// Warnings are the lines `ffc mcp` prints to stderr at start.
	Warnings []string
	close    func()
}

// Close releases the server's clients. It is safe to call more than once.
func (s *MCPServer) Close() {
	if s != nil && s.close != nil {
		s.close()
	}
}

// NewMCPServer builds the MCP server `ffc mcp` serves, for a program that
// runs it in its own process (the desktop app): the options are validated as
// the flags of `ffc mcp` are, the default site's credentials are checked, and
// the tools the served sites' policies allow are registered. Warnings are
// returned in MCPServer.Warnings, never printed. Close the server when done.
//
// Servers built from different options in one process are independent, with
// these process-wide exceptions: client.Timeout and client.Debug, the
// plain-HTTP warning (printed once per URL), and FFC_API_KEY, FFC_API_SECRET
// and FFC_URL from the environment. Other diagnostics still go to stderr:
// warnings of an OAuth refresh, of the config and of the client (the
// FFC_API_KEY half-pair warning, the plain-HTTP warning), the --debug trace
// and the audit writer's. The audit log is always mcp-audit.jsonl
// next to the config.
//
// A tool that runs jq re-executes os.Executable(); the main of a program that
// calls NewMCPServer must call RunJQChildIfRequested first.
func NewMCPServer(ctx context.Context, o MCPOptions) (*MCPServer, error) {
	opts := mcpOptions{
		configPath: o.ConfigPath,
		site:       o.Site,
		sites:      o.Sites,
		allSites:   o.AllSites,
		policy:     o.Policy,
		toolsets:   o.Toolsets,
		api:        true,
	}
	// A nil list is unset; an empty or all-blank one is refused, as
	// "--allow-doctypes=" is.
	if err := cleanMCPOptions(&opts, false, func(opt string) bool {
		switch opt {
		case "allow-tools":
			return o.Policy.AllowTools != nil
		case "allow-doctypes":
			return o.Policy.AllowDoctypes != nil
		case "deny-doctypes":
			return o.Policy.DenyDoctypes != nil
		case "allow-methods":
			return o.Policy.AllowMethods != nil
		case "deny-methods":
			return o.Policy.DenyMethods != nil
		case "toolsets":
			return o.Toolsets != nil
		}
		return false
	}); err != nil {
		return nil, err
	}
	s, sites, warnings, closeEnv, err := buildMCP(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &MCPServer{MCPServer: s, Sites: slices.Clone(sites), Warnings: warnings, close: sync.OnceFunc(closeEnv)}, nil
}

// cleanMCPOptions validates o the way the flags of `ffc mcp` are: it trims
// the list values and refuses one given with no value ("--allow-doctypes="
// reads as "none" but would mean "no limit"), an unknown tool set, a Confirm
// other than always or if-supported, and sites with all-sites. given says
// whether the option named opt (the flag name) was given; cli picks flag
// names or option field names for the errors, which are usage errors for the
// command line.
func cleanMCPOptions(o *mcpOptions, cli bool, given func(opt string) bool) error {
	fail := func(format string, a ...interface{}) error {
		if cli {
			return usageErrorf(format, a...)
		}
		return fmt.Errorf(format, a...)
	}
	name := func(flag, field string) string {
		if cli {
			return "--" + flag
		}
		return field
	}
	for _, l := range []struct {
		flag, field string
		list        *[]string
	}{
		{"allow-tools", "Policy.AllowTools", &o.policy.AllowTools},
		{"allow-doctypes", "Policy.AllowDoctypes", &o.policy.AllowDoctypes},
		{"deny-doctypes", "Policy.DenyDoctypes", &o.policy.DenyDoctypes},
		{"allow-methods", "Policy.AllowMethods", &o.policy.AllowMethods},
		{"deny-methods", "Policy.DenyMethods", &o.policy.DenyMethods},
	} {
		var out []string
		for _, v := range *l.list {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
		if given(l.flag) && len(out) == 0 {
			return fail("%s needs at least one value", name(l.flag, l.field))
		}
		*l.list = out
	}
	var sets []string
	for _, v := range o.toolsets {
		switch v = strings.TrimSpace(v); v {
		case "":
		case toolsetCore, toolsetLifecycle, toolsetCollab, toolsetAdmin, toolsetFiles, toolsetERP:
			if !contains(sets, v, false) {
				sets = append(sets, v)
			}
		default:
			return fail("%s: unknown tool set %q (known: %s)", name("toolsets", "Toolsets"), v, strings.Join(knownToolsets, ", "))
		}
	}
	if given("toolsets") && len(sets) == 0 {
		return fail("%s needs at least one value", name("toolsets", "Toolsets"))
	}
	o.toolsets = sets
	switch o.policy.Confirm = strings.TrimSpace(o.policy.Confirm); o.policy.Confirm {
	case "", config.ConfirmIfSupported, config.ConfirmAlways:
	case config.ConfirmNever:
		return fail("%s can only tighten the site's setting; to turn confirmation off, set sites.<site>.mcp.confirm: never in the config", name("confirm", "Policy.Confirm"))
	default:
		return fail("%s must be always or if-supported, not %q", name("confirm", "Policy.Confirm"), o.policy.Confirm)
	}
	if o.allSites && len(o.sites) > 0 {
		return fail("use %s or %s, not both", name("sites", "Sites"), name("all-sites", "AllSites"))
	}
	return nil
}

// RunJQChildIfRequested runs this process as the jq child of an MCP server
// and exits, when FFC_JQ_CHILD asks for it; otherwise it returns at once.
// The jq filter of an MCP tool runs in a child process (runJQ, mcp_jq.go) so
// that a runaway filter can be killed, and the child is os.Executable()
// started again: any main that embeds the MCP server (the desktop app) must
// call this first, before it starts anything else, or the child would start
// the application instead of running the filter.
func RunJQChildIfRequested() {
	if os.Getenv(jqChildEnv) == jqChildOn {
		os.Exit(jqChild(os.Stdin, os.Stdout)) // a jq run for the MCP server (mcp_jq.go)
	}
}
