# Site health and operations

Four read-only commands show how a site is running: `health`, `jobs`, `errors` and `scheduler`. They need the System Manager role. A user without it gets a permission error (exit 5).

## health

```bash
ffc health
ffc health --json
```

`ffc health` shows Frappe's System Health Report, the same one as the System Health Report page in the desk:

- background workers and the jobs waiting in each queue;
- the scheduler, and the scheduled jobs that failed in the last 7 days;
- emails sent, pending, failed and received in the last 7 days;
- errors logged in the last 24 hours;
- the database, the cache, file storage and backups;
- users, new users, failed logins and active sessions.

A list of what needs attention follows, for example:

- no background workers, or a queue that cannot be reached;
- a scheduler that is not active;
- a scheduled job that is overdue or keeps failing;
- failed emails;
- recent errors.

Building the report takes a few seconds. Like the desk's page, it queues one `frappe.ping` background job to check that the queue works. It writes nothing else. A check that fails on the site leaves its part of the report empty.

With `--json` the answer is the report's fields, plus `attention` (a list of strings). The timestamps, the owner and the row identity of the tables are left out.

## jobs

```bash
ffc jobs
ffc jobs --status failed
ffc jobs --queue long -l 50
ffc jobs -n "<job id>"
```

`ffc jobs` lists the background jobs that Redis still holds, newest first. Redis keeps finished and failed jobs only for a while, so this is recent history, not a log.

On Frappe v15 the list holds at most 20 jobs: the first 20 that Frappe meets, in queue order, sorted afterwards. They are not necessarily the newest, and ffc warns when the page is full. Filter with `--status` or `--queue` to see the jobs you want.

| Flag | Meaning |
| --- | --- |
| `--status` | Only jobs in this state: `queued`, `started`, `failed`, `finished`, `deferred`, `scheduled` or `canceled`. |
| `--queue` | Only jobs of this queue: `default`, `short`, `long` or a custom queue. |
| `-l, --limit` | At most this many jobs (1-500, default 20). |
| `-n, --name` | Show one job with its arguments and its full traceback. |

The table shows the last line of a failed job's traceback. With `--json`, each job carries `exc_info` (the whole traceback) and `arguments`.

## errors

```bash
ffc errors
ffc errors --since 1h
ffc errors --since 7d -d "Sales Invoice"
ffc errors --method TimestampMismatch
ffc errors -n "<name>"
```

`ffc errors` lists the Error Log, newest first.

| Flag | Meaning |
| --- | --- |
| `--since` | Only errors newer than this: `30m`, `1h`, `7d`, and so on (default `24h`). `0` lists them all. |
| `-d, --doctype` | Only errors about this DocType. |
| `--method` | Only errors whose title contains this text. `%` and `_` are wildcards (SQL `like`). |
| `-l, --limit` | At most this many errors (1-500, default 20). |
| `-n, --name` | Show one entry with its traceback. |

Frappe stores times in the time zone set in System Settings. `--since` counts back from now in that zone, using your computer's clock. With `--json`, each row carries the whole traceback (`error`). Reading an entry here does not mark it as seen, unlike opening it in the desk.

## scheduler

```bash
ffc scheduler
ffc scheduler --since 7d
```

`ffc scheduler` shows whether the scheduler is active and how many scheduled job types are enabled and stopped. It then lists the jobs that failed within `--since` (default `24h`), with the number of failures and the last error of each.

The scheduler is inactive when any of these is true:

- it is disabled in System Settings or in the site config;
- the site is in maintenance mode;
- the scheduler is paused (`pause_scheduler`).

`ffc health` also says whether a scheduler process is running at all.

Only job types with "Create Log" leave a Scheduled Job Log, for failed runs as well as successful ones. Frappe sets it for every frequency except All and Cron. A failure of a job type without it shows only among the background jobs, while Redis keeps it: `ffc jobs --status failed`. At most the newest 1000 failed runs are counted.

With `--json` the answer is `{status, enabled, stopped, since, failures: [{job_type, method, count, last_failure, error}], warning}`. `since` is the start of the window in site time, left out with `--since 0`. `method`, `error` and `warning` are left out when empty; `warning` says when the 1000 cap was reached.
