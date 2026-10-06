package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/itchyny/gojq"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
)

// The jq argument (T2.7): the read tools whose result can be large
// (toolSurface big, action read) take a jq filter that the server runs on
// the result before returning it, so a model can ask for the few values it
// needs. The query comes from a model, so it runs in a child process (this
// binary again, see jqChild): gojq checks its deadline only between steps
// and has no memory bound, so "a" * 2e9 or a runaway recursion would
// otherwise take the server down. The child is killed at jqTimeout, exits
// when its heap passes jqMaxHeap, starts with an empty environment, and
// gojq compiled without options has no $ENV, input or modules anyway.
const (
	jqParam    = "jq"
	jqChildEnv = "FFC_JQ_CHILD" // set to jqChildOn: run as the jq child (Execute)
	jqChildOn  = "ffc-mcp-jq-v1"
	jqMaxHeap  = 256 << 20
	jqExitHeap = 3 // the child's exit code when its heap passes jqMaxHeap
)

// jqTimeout bounds a jq run (a variable for tests).
var jqTimeout = 5 * time.Second

// jqDescription is the jq parameter's description on every tool that has it.
const jqDescription = `Optional jq filter run on the result before it is returned, e.g. "[.[] | {name, status}]" or ".data | length". The output is JSON: one output as is, none or several as an array. It runs on the result as this tool returns it (after truncation, so check "truncated"), sees nothing else, and is stopped after 5 s or 256 MiB.`

// jqTool reports whether a tool takes the jq parameter: read tools only, so
// a failing query never hides the outcome of a write.
func jqTool(name string) bool {
	return toolSurface[name].big && toolActions[name] == actRead
}

// jqArg checks the jq argument and returns it; "" when absent or empty. Only
// the tools that declare it accept it. The query is compiled here only to
// refuse a bad one before any request; it runs in the child.
func jqArg(req mcp.CallToolRequest) (string, error) {
	v, ok := req.GetArguments()[jqParam]
	if !ok || v == nil {
		return "", nil
	}
	s, isStr := v.(string)
	if !isStr {
		return "", fmt.Errorf("%s: expected a string", jqParam)
	}
	if s == "" {
		return "", nil
	}
	if !jqTool(req.Params.Name) {
		return "", fmt.Errorf("%s: %s does not take a jq filter", jqParam, req.Params.Name)
	}
	q, err := gojq.Parse(s)
	if err == nil {
		_, err = gojq.Compile(q)
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", jqParam, err)
	}
	return s, nil
}

// jqRequest is what the parent sends the child on stdin.
type jqRequest struct {
	Query string      `json:"query"`
	Input interface{} `json:"input"`
}

// jqResponse is what the child writes on stdout.
type jqResponse struct {
	Results []json.RawMessage `json:"results,omitempty"`
	Error   string            `json:"error,omitempty"`
}

// runJQ runs query on v in a child process and returns the result as JSON:
// one output as is, none or several as an array.
func runJQ(ctx context.Context, query string, v interface{}) (json.RawMessage, error) {
	in, err := output.Normalize(v)
	if err != nil {
		return nil, err
	}
	req, err := json.Marshal(jqRequest{Query: query, Input: in})
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", jqParam, err)
	}
	ctx, cancel := context.WithTimeout(ctx, jqTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = []string{jqChildEnv + "=" + jqChildOn}
	cmd.Stdin = bytes.NewReader(req)
	// More than the result cap is refused anyway: never hold more.
	stdout := &capBuffer{max: 2*maxToolResultBytes + 4096}
	stderr := &capBuffer{max: 4096}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	runErr := cmd.Run()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return nil, fmt.Errorf("%s: stopped after %s", jqParam, jqTimeout)
	case stdout.over:
		return nil, fmt.Errorf("%s: result is too large (limit %d KiB)", jqParam, maxToolResultBytes>>10)
	case runErr != nil:
		var exit *exec.ExitError
		if errors.As(runErr, &exit) && exit.ExitCode() == jqExitHeap {
			return nil, fmt.Errorf("%s: stopped after using %d MiB", jqParam, jqMaxHeap>>20)
		}
		return nil, fmt.Errorf("%s: %v %s", jqParam, runErr, strings.TrimSpace(stderr.String()))
	}
	var resp jqResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("%s: reading the result: %w", jqParam, err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("%s: %s", jqParam, resp.Error)
	}
	if len(resp.Results) == 1 {
		return resp.Results[0], nil
	}
	if resp.Results == nil {
		resp.Results = []json.RawMessage{}
	}
	return json.Marshal(resp.Results)
}

// jqChild is the jq child process: it reads a jqRequest on r, writes a
// jqResponse on w and returns the exit code. A watchdog exits with
// jqExitHeap when the heap passes jqMaxHeap.
func jqChild(r io.Reader, w io.Writer) int {
	debug.SetMemoryLimit(jqMaxHeap / 2)
	go func() {
		var m runtime.MemStats
		for {
			time.Sleep(10 * time.Millisecond)
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > jqMaxHeap {
				os.Exit(jqExitHeap)
			}
		}
	}()
	respond := func(resp jqResponse) int {
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			return 1
		}
		return 0
	}
	var req jqRequest
	dec := json.NewDecoder(r)
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		return respond(jqResponse{Error: "reading the request: " + err.Error()})
	}
	q, err := gojq.Parse(req.Query)
	if err != nil {
		return respond(jqResponse{Error: err.Error()})
	}
	code, err := gojq.Compile(q)
	if err != nil {
		return respond(jqResponse{Error: err.Error()})
	}
	results := []json.RawMessage{}
	size := 0
	iter := code.Run(req.Input)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, isErr := v.(error); isErr {
			var halt *gojq.HaltError
			if errors.As(err, &halt) && halt.ExitCode() == 0 {
				break // halt: a normal stop
			}
			return respond(jqResponse{Error: err.Error()})
		}
		b, err := json.Marshal(v)
		if err != nil {
			return respond(jqResponse{Error: "encoding the result: " + err.Error()})
		}
		if size += len(b); size > maxToolResultBytes {
			return respond(jqResponse{Error: fmt.Sprintf("result is too large (limit %d KiB)", maxToolResultBytes>>10)})
		}
		results = append(results, b)
	}
	return respond(jqResponse{Results: results})
}

// capBuffer keeps at most max bytes and records that more were written.
type capBuffer struct {
	bytes.Buffer
	max  int
	over bool
}

func (b *capBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); len(p) > room {
		b.over = true
		if room > 0 {
			b.Buffer.Write(p[:room])
		}
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

// addJQParam declares the jq parameter on a tool's input schema.
func addJQParam(tool *mcp.Tool) {
	props := make(map[string]any, len(tool.InputSchema.Properties)+1)
	for k, v := range tool.InputSchema.Properties {
		props[k] = v
	}
	props[jqParam] = map[string]any{"type": "string", "description": jqDescription}
	tool.InputSchema.Properties = props
}

// response_format (T2.7) on list_docs and run_report: concise, the default,
// is the compact answer they always gave; detailed asks for everything (all
// fields of each document, the report as Frappe returns it).
const responseFormatParam = "response_format"

// responseFormatOption declares response_format on a tool.
func responseFormatOption(detailed string) mcp.ToolOption {
	return mcp.WithString(responseFormatParam,
		mcp.Enum("concise", "detailed"),
		mcp.Description("concise (default) or detailed: "+detailed),
	)
}

// detailedArg reports whether response_format asks for the detailed answer.
func detailedArg(req mcp.CallToolRequest) (bool, error) {
	v, err := stringArg(req, responseFormatParam)
	switch {
	case err != nil:
		return false, err
	case v == "" || v == "concise":
		return false, nil
	case v == "detailed":
		return true, nil
	}
	return false, fmt.Errorf("%s: %q is not concise or detailed", responseFormatParam, v)
}
