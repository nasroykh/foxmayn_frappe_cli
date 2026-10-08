package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
)

// adminDoctypes are the DocTypes each admin view reads, so the DocType
// rules apply to them. System Settings gives the time zone of since. The
// health report is built on the site from Error Log (top error titles),
// RQ Job, Email Queue and User (counts) too.
var adminDoctypes = map[string][]string{
	"site_health":      {"System Health Report", "Scheduled Job Type", "Error Log", "RQ Job", "Email Queue", "User"},
	"list_jobs":        {"RQ Job"},
	"list_errors":      {"Error Log", "System Settings"},
	"scheduler_status": {"Scheduled Job Type", "Scheduled Job Log", "System Settings"},
}

// mcpAdminLimit caps list_jobs and list_errors (the CLI allows 500).
const mcpAdminLimit = 100

// registerAdminTools adds the read-only site views of `ffc health`, `jobs`,
// `errors` and `scheduler`. They share the CLI's fetch functions.
func registerAdminTools(s *server.MCPServer, env *mcpEnv) {
	ro := mcp.WithReadOnlyHintAnnotation(true)
	open := mcp.WithOpenWorldHintAnnotation(true)

	s.AddTool(mcp.NewTool("site_health",
		mcp.WithDescription("Read the site's System Health Report (System Manager only): background workers and queues, the scheduler and failing scheduled jobs, emails (7 days), errors (24 hours), database, cache, storage and users. attention lists what needs attention. Building it takes a few seconds and, as the desk's page does, queues one frappe.ping background job to test the queue; it changes nothing else. A check that failed on the site leaves its fields out."),
		ro, open,
	), toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			return fetchHealth(ctx, c)
		}, nil
	}))

	s.AddTool(mcp.NewTool("list_jobs",
		mcp.WithDescription("List the site's background jobs (RQ Job) that Redis still holds, newest first (System Manager only). Redis keeps finished and failed jobs only for a while. Each job has error, the last line of its traceback; pass name (a job_id) for one job with its arguments and full traceback (exc_info), which can hold any DocType's data. Answers {jobs, warning?}, or the job with name. On Frappe v15 at most 20 jobs come back, not necessarily the newest (warning): filter with status or queue."),
		ro, open,
		mcp.WithString("status", mcp.Enum(rqJobStatuses...), mcp.Description("Only jobs in this state")),
		mcp.WithString("queue", mcp.Description("Only jobs of this queue: default, short, long or a custom one")),
		mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Most jobs to list (1-%d, default 20)", mcpAdminLimit))),
		mcp.WithString("name", mcp.Description("A job_id: return that job whole instead of a list")),
	), toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		name, err := nameArg(req, "name", false)
		if err != nil {
			return nil, err
		}
		status, err := stringArg(req, "status")
		if err != nil {
			return nil, err
		}
		if status != "" && !slices.Contains(rqJobStatuses, status) {
			return nil, fmt.Errorf("status: one of %s", strings.Join(rqJobStatuses, ", "))
		}
		limit, err := adminLimitArg(req)
		if err != nil {
			return nil, err
		}
		queue, err := stringArg(req, "queue")
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			if name != "" {
				doc, err := c.GetDoc(ctx, "RQ Job", name)
				if err != nil {
					return nil, err
				}
				return cleanJob(doc), nil
			}
			site, _ := siteFrom(ctx) // env.run sets it; nil would only skip the v15 warning
			list, err := fetchJobs(ctx, c, site, status, queue, limit)
			if err != nil {
				return nil, err
			}
			for _, j := range list.rows {
				if e := lastTextLine(adminString(j["exc_info"]), 300); e != "" {
					j["error"] = e
				}
				delete(j, "exc_info")
				delete(j, "arguments")
			}
			out := map[string]interface{}{"jobs": list.rows}
			if list.warning != "" {
				out["warning"] = list.warning
			}
			return out, nil
		}, nil
	}))

	s.AddTool(mcp.NewTool("list_errors",
		mcp.WithDescription("List the site's Error Log, newest first (System Manager only). Answers {errors, hidden_by_policy}: each entry has method (its title), the document it concerns (reference_doctype, reference_name) and error, the last line of its traceback; entries about a DocType this server may not read are left out and counted in hidden_by_policy. Pass name for one entry with its full traceback. since counts back in the site's time zone. Reading does not mark entries as seen."),
		ro, open,
		mcp.WithString("since", mcp.Description(`Only errors newer than this: 30m, 1h, 7d, ... (default 24h; "0" for all)`)),
		mcp.WithString("doctype", mcp.Description("Only errors about this DocType (reference_doctype)")),
		mcp.WithString("method", mcp.Description("Only errors whose title contains this text (% and _ are wildcards)")),
		mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Most errors to list (1-%d, default 20)", mcpAdminLimit))),
		mcp.WithString("name", mcp.Description("An Error Log name: return that entry whole instead of a list")),
	), toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		name, err := nameArg(req, "name", false)
		if err != nil {
			return nil, err
		}
		since, err := sinceArg(req)
		if err != nil {
			return nil, err
		}
		limit, err := adminLimitArg(req)
		if err != nil {
			return nil, err
		}
		doctype, err := stringArg(req, "doctype")
		if err != nil {
			return nil, err
		}
		method, err := stringArg(req, "method")
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			// An entry holds data of the document it is about: the
			// DocType rules apply to its reference_doctype too.
			policy, ok := policyFrom(ctx)
			if !ok {
				return nil, fmt.Errorf("policy: no policy to filter the Error Log by")
			}
			allowed := func(r map[string]interface{}) error {
				if dt := strings.TrimSpace(adminString(r["reference_doctype"])); dt != "" {
					return policy.doctypeAllowed(dt, false)
				}
				return nil
			}
			if name != "" {
				doc, err := c.GetDoc(ctx, "Error Log", name)
				if err != nil {
					return nil, err
				}
				if err := allowed(doc); err != nil {
					return nil, err
				}
				rec, _ := normalizeCustomDoc(doc, "", nil, nil)
				rec["creation"] = doc["creation"]
				return rec, nil
			}
			rows, err := fetchErrors(ctx, c, since, doctype, method, limit)
			if err != nil {
				return nil, err
			}
			out := make([]map[string]interface{}, 0, len(rows))
			for _, r := range rows {
				if allowed(r) != nil {
					continue
				}
				r["error"] = lastTextLine(adminString(r["error"]), 300)
				out = append(out, r)
			}
			return map[string]interface{}{"errors": out, "hidden_by_policy": len(rows) - len(out)}, nil
		}, nil
	}))

	s.AddTool(mcp.NewTool("scheduler_status",
		mcp.WithDescription("Show whether the site's scheduler is active, how many scheduled job types are enabled and stopped, and which ones failed within since, with their failure count and the last line of the last error (System Manager only; list_errors and list_jobs have whole tracebacks). Only job types with Create Log leave a log (Frappe sets it for every frequency but All and Cron); other failures show only in list_jobs status=failed."),
		ro, open,
		mcp.WithString("since", mcp.Description(`Count failed runs newer than this: 1h, 7d, ... (default 24h; "0" for all)`)),
	), toolHandler(env, func(req mcp.CallToolRequest) (toolCall, error) {
		since, err := sinceArg(req)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *client.FrappeClient) (interface{}, error) {
			rep, err := fetchScheduler(ctx, c, since)
			if err != nil {
				return nil, err
			}
			for i := range rep.Failures {
				rep.Failures[i].Error = lastTextLine(rep.Failures[i].Error, 300)
			}
			return rep, nil
		}, nil
	}))
}

func sinceArg(req mcp.CallToolRequest) (time.Duration, error) {
	s, err := stringArg(req, "since")
	if err != nil {
		return 0, err
	}
	if _, set := req.GetArguments()["since"]; !set {
		s = "24h"
	}
	return parseSince(s)
}

func adminLimitArg(req mcp.CallToolRequest) (int, error) {
	n, err := intArg(req, "limit", 20)
	if err != nil {
		return 0, err
	}
	if n < 1 || n > mcpAdminLimit {
		return 0, fmt.Errorf("limit: between 1 and %d", mcpAdminLimit)
	}
	return n, nil
}
