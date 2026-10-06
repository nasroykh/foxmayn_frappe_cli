package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/itchyny/gojq"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
)

// The jq argument (T2.7): the tools whose result can be large (toolSurface
// big) take a jq filter that the server runs on the result before returning
// it, so a model can ask for the few values it needs. gojq compiled without
// options has no access to the environment, no input and no modules, so a
// query sees only the result. It runs under jqTimeout: an endless
// generator is stopped, not left to spin.
const jqParam = "jq"

// jqTimeout bounds a jq run (a variable for tests).
var jqTimeout = 5 * time.Second

// jqDescription is the jq parameter's description on every tool that has it.
const jqDescription = `Optional jq filter run on the result before it is returned, e.g. "[.[] | {name, status}]" or ".data | length". One output is returned as is; none or several are returned as an array. It runs on the result as this tool returns it (after truncation, so check "truncated"), and cannot read anything else.`

// jqArg compiles the jq argument; nil when it is absent or empty. Only the
// tools that declare it accept it.
func jqArg(req mcp.CallToolRequest) (*gojq.Code, error) {
	v, ok := req.GetArguments()[jqParam]
	if !ok || v == nil {
		return nil, nil
	}
	s, isStr := v.(string)
	if !isStr {
		return nil, fmt.Errorf("%s: expected a string", jqParam)
	}
	if s == "" {
		return nil, nil
	}
	if info, ok := toolSurface[req.Params.Name]; !ok || !info.big {
		return nil, fmt.Errorf("%s: %s does not take a jq filter", jqParam, req.Params.Name)
	}
	q, err := gojq.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", jqParam, err)
	}
	code, err := gojq.Compile(q)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", jqParam, err)
	}
	return code, nil
}

// runJQ runs code on v: one output is returned as is, none or several as an
// array.
func runJQ(ctx context.Context, code *gojq.Code, v interface{}) (interface{}, error) {
	if so, ok := v.(structuredOut); ok {
		v = so.Text // the filtered value no longer has the declared shape
	}
	in, err := output.Normalize(v)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, jqTimeout)
	defer cancel()
	results := []interface{}{}
	iter := code.RunWithContext(ctx, in)
	for {
		r, ok := iter.Next()
		if !ok {
			break
		}
		if err, isErr := r.(error); isErr {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("%s: stopped after %s", jqParam, jqTimeout)
			}
			var halt *gojq.HaltError
			if errors.As(err, &halt) && halt.ExitCode() == 0 {
				break // halt: a normal stop
			}
			return nil, fmt.Errorf("%s: %w", jqParam, err)
		}
		results = append(results, r)
	}
	if len(results) == 1 {
		return results[0], nil
	}
	return results, nil
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
