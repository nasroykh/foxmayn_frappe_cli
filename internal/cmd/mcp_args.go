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

// maxMCPBulkWorkers is how many items bulk_create, bulk_update and
// bulk_delete have in flight at once. bulk_submit and bulk_cancel run one at
// a time: concurrent submits can deadlock on ERPNext's GL and stock
// postings, and cancels must follow the order given (links).
const maxMCPBulkWorkers = 4

// toolCall is the site work of a tool, run once its arguments are valid.
type toolCall func(ctx context.Context, c *client.FrappeClient) (interface{}, error)

// mcpEnv is what every tool call needs: the sites the server serves, a
// site's config (read on every call, so a config edit applies at once), its
// client, the policy flags of `ffc mcp` and the audit log.
type mcpEnv struct {
	// sites are the served sites by their exact config names, the default
	// one first. A single-site server has one entry ("" for a site defined
	// only by FFC_* variables).
	sites  []string
	site   func(ctx context.Context, name string) (*config.SiteConfig, error)
	client func(ctx context.Context, site *config.SiteConfig) (*client.FrappeClient, error)
	flags  config.MCPPolicy
	audit  *auditLog // nil: no audit log
	// toolsets are the tool sets `ffc mcp --toolsets` exposes; nil means
	// defaultToolsets (core and lifecycle).
	toolsets []string
}

// toolHandler runs a tool call in a fixed order: validate the arguments (so
// a bad call never costs a login or a request), apply the site's MCP policy,
// then get the client and run the call. Every call, refused or not, leaves a
// line in the audit log. Every failure becomes a tool error with a nil Go
// error, so the model sees it and can correct itself instead of the client
// treating it as a protocol failure.
func toolHandler(env *mcpEnv, parse func(req mcp.CallToolRequest) (toolCall, error)) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rec := auditRecord{Time: time.Now().UTC(), Tool: req.Params.Name, Client: mcpClientName(ctx), Via: viaFrom(ctx)}
		res := env.run(ctx, req, parse, &rec)
		rec.DurationMS = time.Since(rec.Time).Milliseconds()
		env.audit.write(rec, req.GetArguments())
		if read := resourceReadFrom(ctx); read != nil {
			read.status, read.err = rec.Status, rec.cause
		}
		return res, nil
	}
}

// appendNew returns list with s added when it is not empty or already there.
// It copies, as list may share its array with the scope.
func appendNew(list []string, s string) []string {
	if s == "" || contains(list, s, false) {
		return list
	}
	return append(append([]string(nil), list...), s)
}

func (env *mcpEnv) run(ctx context.Context, req mcp.CallToolRequest, parse func(req mcp.CallToolRequest) (toolCall, error), rec *auditRecord) *mcp.CallToolResult {
	fail := func(status string, err error) *mcp.CallToolResult {
		rec.Status, rec.Error, rec.cause = status, err.Error(), err
		return mcp.NewToolResultError(err.Error())
	}
	if siteless[req.Params.Name] {
		if _, err := jqArg(req); err != nil {
			return fail(auditInvalid, err)
		}
		return env.runSiteless(ctx, req, parse, rec)
	}
	// The site is read first only so every audit line names it; a bad
	// argument is still reported before a config error.
	name, nameErr := env.siteFor(req)
	var site *config.SiteConfig
	siteErr := nameErr
	if nameErr == nil {
		if site, siteErr = env.site(ctx, name); siteErr == nil {
			rec.Site = site.Name
		}
	}
	call, err := parse(req)
	if err == nil {
		err = nameErr
	}
	var jq string
	if err == nil {
		jq, err = jqArg(req)
	}
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
	if res := policy.confirm(ctx, req, scope, rec); res != nil {
		return res
	}
	c, err := env.client(ctx, site)
	if err != nil {
		return fail(auditError, err)
	}
	if err := policy.checkReport(ctx, c, scope); err != nil {
		return fail(auditDenied, err)
	}
	if scope.Restore != nil {
		id, dt, name, err := policy.checkRestore(ctx, c, scope)
		rec.Doctypes = appendNew(rec.Doctypes, dt)
		rec.Names = appendNew(appendNew(rec.Names, id), name)
		if err != nil {
			if strings.HasPrefix(err.Error(), "policy:") {
				return fail(auditDenied, err)
			}
			return fail(auditError, err)
		}
		ctx = withRestoreID(ctx, id)
	}
	if err := checkCommentAuthor(ctx, c, policy, req, scope); err != nil {
		return fail(auditDenied, err)
	}
	out, err := call(withProgress(withSite(withPolicy(ctx, policy), site), req), c)
	if err != nil {
		return fail(auditError, err)
	}
	var res *mcp.CallToolResult
	if jq != "" {
		// The read succeeded; only the filter can fail now.
		raw, err := runJQ(ctx, jq, out)
		if err != nil {
			return fail(auditError, err)
		}
		res = marshalResult(raw)
	} else {
		res = toolResult(out)
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

// structuredOut is a tool result with structured content (for a tool that
// declares an output schema): Text is marshalled as the text content, as
// before, and Structured, a JSON object, is the structuredContent.
type structuredOut struct {
	Text       interface{}
	Structured interface{}
}

// toolResult turns what a toolCall returned into the tool result.
func toolResult(out interface{}) *mcp.CallToolResult {
	switch v := out.(type) {
	case string:
		return mcp.NewToolResultText(v)
	case structuredOut:
		res := marshalResult(v.Text)
		if !res.IsError {
			res.StructuredContent = v.Structured
		}
		return res
	}
	return marshalResult(out)
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

// strictBoolArg reads an optional boolean argument: true or false, or the text
// "true" or "false". Anything else is an error and never false, for a flag
// whose absence would change what the call does.
func strictBoolArg(req mcp.CallToolRequest, key string) (bool, error) {
	switch v := req.GetArguments()[key].(type) {
	case nil:
		return false, nil
	case bool:
		return v, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
	}
	return false, usageErrorf("%s: expected true or false", key)
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
