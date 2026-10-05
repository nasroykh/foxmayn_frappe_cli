package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/spf13/cobra"
)

// Exit codes. They are part of the CLI contract (README "Exit codes"):
// change them only with a release note.
const (
	exitOK          = 0
	exitGeneric     = 1
	exitUsage       = 2
	exitAuth        = 3
	exitNotFound    = 4
	exitPermission  = 5
	exitValidation  = 6
	exitNetwork     = 7
	exitPartial     = 8
	exitInterrupted = 130
)

// usageError is a bad invocation: unknown command or flag, wrong arguments,
// or a flag value that cannot be valid.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

// usageErrorf returns a usage error (exit code 2).
func usageErrorf(format string, a ...interface{}) error {
	return &usageError{fmt.Errorf(format, a...)}
}

// codeError ends the command with a given exit code, e.g. a jq halt_error.
type codeError struct {
	code int
	msg  string
}

func (e *codeError) Error() string { return e.msg }

// deniedError is a permission check the site answered "no" to (ffc can).
type deniedError struct{ msg string }

func (e *deniedError) Error() string { return e.msg }

// partialError is a bulk run in which some items did not succeed.
type partialError struct{ msg string }

func (e *partialError) Error() string { return e.msg }

// classify returns the exit code and a stable kind name for err.
func classify(err error) (int, string) {
	var (
		usage   *usageError
		partial *partialError
		coded   *codeError
		denied  *deniedError
		auth    *client.AuthError
		api     *client.APIError
		tr      *client.TransportError
		state   *client.StateError
	)
	switch {
	case err == nil:
		return exitOK, "ok"
	case errors.Is(err, context.Canceled):
		return exitInterrupted, "interrupted"
	case errors.As(err, &usage):
		return exitUsage, "usage"
	case errors.As(err, &partial):
		return exitPartial, "partial"
	case errors.As(err, &coded):
		return coded.code, "error"
	case errors.As(err, &denied):
		return exitPermission, "permission"
	case errors.As(err, &auth):
		return exitAuth, "auth"
	case errors.As(err, &api):
		return classifyAPI(api)
	case errors.As(err, &state):
		return exitValidation, "validation"
	case errors.As(err, &tr):
		return exitNetwork, "network"
	}
	return exitGeneric, "error"
}

func classifyAPI(e *client.APIError) (int, string) {
	switch {
	case e.Status == http.StatusUnauthorized, e.ExcType == "AuthenticationError", e.ExcType == "SessionExpired":
		return exitAuth, "auth"
	case e.Status == http.StatusForbidden:
		return exitPermission, "permission"
	case e.Status == http.StatusNotFound, e.MissingDocType:
		return exitNotFound, "not_found"
	case e.Status == http.StatusTooManyRequests, e.Status >= 500:
		return exitNetwork, "server"
	case e.Status == http.StatusBadRequest, e.Status == http.StatusConflict,
		e.Status == http.StatusExpectationFailed, e.Status == http.StatusUnprocessableEntity,
		e.ExcType == "TimestampMismatchError":
		return exitValidation, "validation"
	}
	return exitGeneric, "error"
}

// errorJSON is the shape of an error on stderr under --json.
type errorJSON struct {
	Error struct {
		Code     string `json:"code"`
		ExitCode int    `json:"exit_code"`
		Status   int    `json:"status,omitempty"`
		ExcType  string `json:"exc_type,omitempty"`
		Message  string `json:"message"`
	} `json:"error"`
}

// reportError writes err to w, as JSON when asJSON is set, and returns the
// exit code.
func reportError(w io.Writer, err error, asJSON bool) int {
	code, kind := classify(err)
	if !asJSON {
		fmt.Fprintln(w, err)
		return code
	}
	var out errorJSON
	out.Error.Code, out.Error.ExitCode, out.Error.Message = kind, code, err.Error()
	var api *client.APIError
	if errors.As(err, &api) {
		out.Error.Status, out.Error.ExcType = api.Status, api.ExcType
	}
	var auth *client.AuthError
	if errors.As(err, &auth) {
		out.Error.Status = auth.Status
	}
	b, _ := json.Marshal(out)
	fmt.Fprintln(w, string(b))
	return code
}

// runStarted is set when a command's RunE begins. An error returned before
// that came from cobra's argument and flag checks: a usage error.
var runStarted bool

// trackRunStart wraps every RunE in the tree to set runStarted, and to fail
// on an invalid environment setting (applyEnv cannot return errors).
func trackRunStart(c *cobra.Command) {
	if run := c.RunE; run != nil {
		c.RunE = func(cmd *cobra.Command, args []string) error {
			runStarted = true
			if envErr != nil {
				return envErr
			}
			cmd.SetContext(withDryRun(cmd))
			return run(cmd, args)
		}
	}
	for _, sub := range c.Commands() {
		trackRunStart(sub)
	}
}

func init() {
	// RunE wrapping happens in execute, after every init has registered its
	// command; flag errors are marked here.
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &usageError{err} })
}

// argsWantJSON reports whether args (or FFC_OUTPUT) ask for JSON output,
// for errors cobra returns before it has parsed the flags.
func argsWantJSON(args []string) bool {
	jsonFormat := func(f string) bool {
		f = strings.ToLower(strings.TrimSpace(f))
		return f == "json" || f == "ndjson"
	}
	want, output := false, os.Getenv("FFC_OUTPUT")
	for i, a := range args {
		switch {
		case a == "--":
			return want || jsonFormat(output)
		case a == "--json", a == "-j":
			want = true
		case strings.HasPrefix(a, "--json="):
			v, err := strconv.ParseBool(strings.TrimPrefix(a, "--json="))
			want = err == nil && v
		case a == "--output" && i+1 < len(args):
			output = args[i+1]
		case strings.HasPrefix(a, "--output="):
			output = strings.TrimPrefix(a, "--output=")
		}
	}
	return want || jsonFormat(output)
}
