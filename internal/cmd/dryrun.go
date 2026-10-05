package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"

	"github.com/spf13/cobra"
)

// dryRunAnnotation marks a command that takes --dry-run, with its scope.
const dryRunAnnotation = "ffc/dry-run"

// addDryRun gives a write command the --dry-run flag. With scope "all"
// (call-method, api) no request is sent at all; otherwise reads still run
// so the command can show what it would write.
func addDryRun(cmd *cobra.Command, all bool) {
	usage := "Show the write request instead of sending it (reads still run); exits 0"
	scope := "writes"
	if all {
		usage, scope = "Show the request instead of sending it; exits 0", "all"
	}
	cmd.Flags().Bool("dry-run", false, usage)
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[dryRunAnnotation] = scope
}

// dryRunOn reports whether cmd runs with --dry-run.
func dryRunOn(cmd *cobra.Command) bool {
	on, err := cmd.Flags().GetBool("dry-run")
	return err == nil && on
}

// withDryRun returns cmd's context, marked for a dry run when --dry-run is
// given: the client then returns the planned write as a *client.DryRunError
// instead of sending it.
func withDryRun(cmd *cobra.Command) context.Context {
	ctx := cmd.Context()
	if !dryRunOn(cmd) {
		return ctx
	}
	scope := client.DryRunWrites
	if cmd.Annotations[dryRunAnnotation] == "all" {
		scope = client.DryRunAll
	}
	return client.WithDryRun(ctx, scope)
}

// dryRunResult prints the plan when err is a dry run's held-back request,
// and reports whether it was one.
func dryRunResult(err error) (bool, error) {
	var plan *client.DryRunError
	if !errors.As(err, &plan) {
		return false, nil
	}
	return true, printPlan(plan)
}

// printPlan prints the requests a dry run did not send: as data in a
// machine format, otherwise as a readable list.
func printPlan(plan *client.DryRunError) error {
	if machineOutput() {
		return printResult(map[string]interface{}{"dry_run": true, "requests": plan.Requests})
	}
	clean := func(s string) string {
		if stdoutIsTerminal() {
			return text.Sanitize(s)
		}
		return s
	}
	fmt.Fprintln(os.Stderr, "Dry run: nothing was sent.")
	for i, r := range plan.Requests {
		if i > 0 {
			fmt.Println()
		}
		fmt.Println(clean(r.Method + " " + r.URL))
		if r.Body != nil {
			b, err := json.MarshalIndent(r.Body, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(clean(string(b)))
		}
		if len(r.Changes) > 0 {
			fields := make([]string, 0, len(r.Changes))
			for f := range r.Changes {
				fields = append(fields, f)
			}
			sort.Strings(fields)
			rows := make([]map[string]interface{}, len(fields))
			for j, f := range fields {
				ch, _ := r.Changes[f].(map[string]interface{})
				rows[j] = map[string]interface{}{"field": f, "current": ch["from"], "new": ch["to"]}
			}
			output.PrintTable(rows, []string{"field", "current", "new"})
		}
	}
	return nil
}

// fieldChanges compares the fields data would set with doc's current
// values: field → {"from", "to"} for each that differs.
func fieldChanges(doc, data map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for k, to := range data {
		from := doc[k]
		a, _ := json.Marshal(from)
		b, _ := json.Marshal(to)
		if string(a) != string(b) {
			out[k] = map[string]interface{}{"from": from, "to": to}
		}
	}
	return out
}
