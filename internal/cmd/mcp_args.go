package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
)

// maxToolResultBytes caps a tool result. Anything bigger is refused with a
// hint to narrow the request, instead of flooding the model's context (and
// the server's memory) with megabytes of rows.
const maxToolResultBytes = 512 << 10

// maxMCPBulkItems caps the items a single bulk_* tool call may touch.
const maxMCPBulkItems = 200

// toolCall is the site work of a tool, run once its arguments are valid.
type toolCall func(ctx context.Context, c *client.FrappeClient) (interface{}, error)

// mcpEnv is what every tool call needs: the site (read on every call, so a
// config edit applies at once), its client, the policy flags of `ffc mcp`
// and the audit log.
type mcpEnv struct {
	site   func(ctx context.Context) (*config.SiteConfig, error)
	client func(ctx context.Context, site *config.SiteConfig) (*client.FrappeClient, error)
	flags  config.MCPPolicy
	audit  *auditLog // nil: no audit log
}

// toolHandler runs a tool call in a fixed order: validate the arguments (so
// a bad call never costs a login or a request), apply the site's MCP policy,
// then get the client and run the call. Every call, refused or not, leaves a
// line in the audit log. Every failure becomes a tool error with a nil Go
// error, so the model sees it and can correct itself instead of the client
// treating it as a protocol failure.
func toolHandler(env *mcpEnv, parse func(req mcp.CallToolRequest) (toolCall, error)) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rec := auditRecord{Time: time.Now().UTC(), Tool: req.Params.Name, Client: mcpClientName(ctx)}
		res := env.run(ctx, req, parse, &rec)
		rec.DurationMS = time.Since(rec.Time).Milliseconds()
		env.audit.write(rec, req.GetArguments())
		return res, nil
	}
}

func (env *mcpEnv) run(ctx context.Context, req mcp.CallToolRequest, parse func(req mcp.CallToolRequest) (toolCall, error), rec *auditRecord) *mcp.CallToolResult {
	fail := func(status string, err error) *mcp.CallToolResult {
		rec.Status, rec.Error = status, err.Error()
		return mcp.NewToolResultError(err.Error())
	}
	// The site is read first only so every audit line names it; a bad
	// argument is still reported before a config error.
	site, siteErr := env.site(ctx)
	if siteErr == nil {
		rec.Site = site.Name
	}
	call, err := parse(req)
	if err != nil {
		return fail(auditInvalid, err)
	}
	scope, err := scopeOf(req)
	if err != nil {
		return fail(auditDenied, err)
	}
	rec.Doctypes, rec.Names, rec.Method = scope.Doctypes, scope.Names, scope.Method
	if siteErr != nil {
		return fail(auditError, siteErr)
	}
	policy := newMCPPolicy(site, env.flags)
	if err := policy.check(req.Params.Name, scope); err != nil {
		return fail(auditDenied, err)
	}
	c, err := env.client(ctx, site)
	if err != nil {
		return fail(auditError, err)
	}
	if err := policy.checkReport(ctx, c, scope); err != nil {
		return fail(auditDenied, err)
	}
	out, err := call(ctx, c)
	if err != nil {
		return fail(auditError, err)
	}
	var res *mcp.CallToolResult
	if s, ok := out.(string); ok {
		res = mcp.NewToolResultText(s)
	} else {
		res = marshalResult(out)
	}
	rec.Status = auditOK
	if res.IsError {
		rec.Status = auditError // the result was too large to return
	}
	return res
}

// mcpClientName is the name the MCP client gave for itself. It is
// unauthenticated, so it is only recorded, never trusted.
func mcpClientName(ctx context.Context) string {
	if info := server.RequestProtocolInfoFromContext(ctx); info != nil && info.ClientInfo != nil && info.ClientInfo.Name != "" {
		return info.ClientInfo.Name
	}
	if s, ok := server.ClientSessionFromContext(ctx).(server.SessionWithClientInfo); ok {
		return s.GetClientInfo().Name
	}
	return ""
}

// marshalResult serializes data as compact JSON, refusing oversized results.
func marshalResult(data interface{}) *mcp.CallToolResult {
	b, err := json.Marshal(data)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding result: %s", err))
	}
	if len(b) > maxToolResultBytes {
		return mcp.NewToolResultError(fmt.Sprintf(
			"result is too large (%d KiB, limit %d KiB): narrow it with limit, fields, filters or keys",
			len(b)>>10, maxToolResultBytes>>10))
	}
	return mcp.NewToolResultText(string(b))
}

// jsonArg decodes a JSON-valued argument into out. Models send these either
// as native JSON ({"status":"Open"}) or as a JSON-encoded string; both are
// accepted. mcp-go's GetString returns "" for a non-string value, which used
// to drop object filters silently and return unfiltered results.
func jsonArg(req mcp.CallToolRequest, key string, out interface{}) (bool, error) {
	v, ok := req.GetArguments()[key]
	if !ok || v == nil {
		return false, nil
	}
	var raw []byte
	if s, isStr := v.(string); isStr {
		if s == "" {
			return false, nil
		}
		raw = []byte(s)
	} else {
		var err error
		if raw, err = json.Marshal(v); err != nil {
			return false, fmt.Errorf("%s: %w", key, err)
		}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return false, fmt.Errorf("%s: invalid JSON: %w", key, err)
	}
	return true, nil
}

// rawJSONArg returns a JSON argument re-encoded as a string, for parameters
// such as list filters that the API takes as JSON text. It must be an object
// or an array.
func rawJSONArg(req mcp.CallToolRequest, key string) (string, error) {
	var v interface{}
	ok, err := jsonArg(req, key, &v)
	if err != nil || !ok {
		return "", err
	}
	switch v.(type) {
	case map[string]interface{}, []interface{}:
	default:
		return "", fmt.Errorf("%s: expected a JSON object or array", key)
	}
	b, err := json.Marshal(v)
	return string(b), err
}

// objectArg decodes a JSON object argument; null and non-objects are errors.
func objectArg(req mcp.CallToolRequest, key string, required bool) (map[string]interface{}, error) {
	var m map[string]interface{}
	ok, err := jsonArg(req, key, &m)
	switch {
	case err != nil:
		return nil, err
	case !ok || m == nil:
		if required {
			return nil, fmt.Errorf("%s: a JSON object is required", key)
		}
		return nil, nil
	}
	return m, nil
}

// intArg reads a non-negative integer argument (number or numeric string).
func intArg(req mcp.CallToolRequest, key string, def int) (int, error) {
	v, ok := req.GetArguments()[key]
	if !ok || v == nil {
		return def, nil
	}
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case int:
		f = float64(n)
	case string:
		p, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return 0, fmt.Errorf("%s: expected an integer, got %q", key, n)
		}
		f = p
	default:
		return 0, fmt.Errorf("%s: expected an integer", key)
	}
	if f != math.Trunc(f) || f < 0 || f > math.MaxInt32 {
		return 0, fmt.Errorf("%s: expected a non-negative integer, got %v", key, v)
	}
	return int(f), nil
}

// stringArg reads an optional string argument. Unlike req.GetString, which
// returns "" for any non-string value, a wrong type is an error, so a model
// that sends an array or number learns the call was not understood.
func stringArg(req mcp.CallToolRequest, key string) (string, error) {
	v, ok := req.GetArguments()[key]
	if !ok || v == nil {
		return "", nil
	}
	s, isString := v.(string)
	if !isString {
		return "", fmt.Errorf("%s: expected a string", key)
	}
	return s, nil
}

// nameArg reads a document name, which may arrive as a JSON number for
// integer-named DocTypes (the same rule as the CLI's docName).
func nameArg(req mcp.CallToolRequest, key string, required bool) (string, error) {
	v, ok := req.GetArguments()[key]
	if !ok || v == nil || v == "" {
		if required {
			return "", fmt.Errorf("required argument %q not found", key)
		}
		return "", nil
	}
	name, valid := docName(v)
	if !valid {
		return "", fmt.Errorf("%s: expected a string or number", key)
	}
	return name, nil
}

// keysArg reads a key list given as an array of strings (native or
// JSON-encoded) or as a comma-separated string.
func keysArg(req mcp.CallToolRequest, key string) ([]string, error) {
	if s, ok := req.GetArguments()[key].(string); ok && !strings.HasPrefix(strings.TrimSpace(s), "[") {
		return splitCSV(s), nil
	}
	return stringsArg(req, key)
}

// stringsArg reads an array of strings (native or JSON-encoded) argument.
func stringsArg(req mcp.CallToolRequest, key string) ([]string, error) {
	var out []string
	if _, err := jsonArg(req, key, &out); err != nil {
		return nil, fmt.Errorf("%s: expected an array of strings", key)
	}
	return out, nil
}

// rawArg returns an argument re-encoded as JSON bytes (for the bulk parsers).
func rawArg(req mcp.CallToolRequest, key string) ([]byte, error) {
	var v interface{}
	ok, err := jsonArg(req, key, &v)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("required argument %q not found", key)
	}
	return json.Marshal(v)
}
