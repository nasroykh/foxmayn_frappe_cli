package cmd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/output"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/version"

	"github.com/spf13/cobra"
)

// Check statuses. fail makes `ffc doctor` exit 1; warn does not.
const (
	checkPass = "pass"
	checkWarn = "warn"
	checkFail = "fail"
)

// doctorCheck is one line of the report. The JSON form is a stable array of
// these: check ids are never renamed, and hint is always present ("" when
// there is nothing to do).
type doctorCheck struct {
	Check   string `json:"check"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

const (
	// clockWarn and clockFail are the local clock's distance from the
	// server's that is worth a warning and a failure. The Date header has
	// one-second resolution, so smaller differences are noise.
	clockWarn = time.Minute
	clockFail = 10 * time.Minute
	// certWarnDays: a certificate that ends within this many days is a warning.
	certWarnDays = 14
	// probeTimeout bounds each probe that does not use the site client.
	probeTimeout = 10 * time.Second
)

// Seams for tests.
var (
	doctorNow      = time.Now
	doctorTLSRoots *x509.CertPool // nil: the system's
)

// quotedValue matches the `value` yaml.v3 puts in a conversion error: it can
// be a secret, so it never reaches the report.
var quotedValue = regexp.MustCompile("`[^`]*`")

type doctor struct {
	checks []doctorCheck
}

func (d *doctor) add(check, status, message, hint string) {
	d.checks = append(d.checks, doctorCheck{check, status, message, hint})
}

// ─── config ──────────────────────────────────────────────────────────────────

// checkConfig inspects the config file. It returns the selected site, or nil
// when the later checks cannot run.
func (d *doctor) checkConfig() *config.SiteConfig {
	path, err := resolveCfgPath()
	if err != nil {
		d.add("config.file", checkFail, err.Error(), "")
		return nil
	}
	st, statErr := os.Stat(path)
	switch {
	case statErr == nil:
		d.checkPerms(path, st)
	case errors.Is(statErr, fs.ErrNotExist) && configPath == "":
		d.add("config.file", checkPass, "no config file; the site comes from the FFC_* environment variables", "")
	case errors.Is(statErr, fs.ErrNotExist):
		d.add("config.file", checkFail, fmt.Sprintf("%s does not exist", path), "create it with 'ffc init' or point --config at an existing file")
		return nil
	default:
		d.add("config.file", checkFail, fmt.Sprintf("cannot read %s: %v", path, statErr), "")
		return nil
	}

	if statErr == nil {
		if _, err := config.Read(path); err != nil {
			d.add("config.parse", checkFail, "the config file does not parse: "+quotedValue.ReplaceAllString(err.Error(), "`…`"), fmt.Sprintf("fix the YAML in %s, or recreate it with 'ffc init'", path))
			return nil
		}
	}
	cfg, err := loadSiteConfig()
	if err != nil {
		d.add("config.parse", checkFail, quotedValue.ReplaceAllString(err.Error(), "`…`"), "select a site with --site, or set default_site in the config; 'ffc site list' shows the sites")
		return nil
	}
	d.add("config.parse", checkPass, fmt.Sprintf("site %s (%s), %s auth", siteLabel(cfg), redactedURL(cfg.URL), authMethod(cfg)), "")

	if statErr == nil {
		d.checkLock(path)
	}
	return cfg
}

func siteLabel(cfg *config.SiteConfig) string {
	if cfg.Name == "" {
		return "from the environment"
	}
	return cfg.Name
}

// redactedURL hides a password in the URL's userinfo.
func redactedURL(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Redacted()
	}
	return raw
}

func (d *doctor) checkPerms(path string, st fs.FileInfo) {
	if runtime.GOOS == "windows" {
		d.add("config.file", checkPass, path+" (permission bits are not checked on Windows)", "")
		return
	}
	if mode := st.Mode().Perm(); mode&0o077 != 0 {
		d.add("config.file", checkFail, fmt.Sprintf("%s is readable by other users (mode %04o) and holds credentials", path, mode), "chmod 600 "+path)
	} else {
		d.add("config.file", checkPass, fmt.Sprintf("%s (mode %04o)", path, st.Mode().Perm()), "")
	}
	dir := pathDir(path)
	dst, err := os.Stat(dir)
	switch {
	case err != nil:
		d.add("config.dir", checkWarn, fmt.Sprintf("cannot read %s: %v", dir, err), "")
	case dst.Mode().Perm() != 0o700:
		d.add("config.dir", checkWarn, fmt.Sprintf("%s has mode %04o; ffc creates it as 0700", dir, dst.Mode().Perm()), "chmod 700 "+dir)
	default:
		d.add("config.dir", checkPass, fmt.Sprintf("%s (mode 0700)", dir), "")
	}
}

func pathDir(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i > 0 {
		return p[:i]
	}
	return "."
}

func (d *doctor) checkLock(path string) {
	l := config.LockStatus(path)
	switch {
	case !l.Exists:
		d.add("config.lock", checkPass, "no lock file", "")
	case l.Stale:
		d.add("config.lock", checkWarn, fmt.Sprintf("%s is %s old: left by a crashed ffc", l.Path, l.Age.Round(time.Second)), "ffc breaks it on the next config write; or remove "+l.Path)
	default:
		d.add("config.lock", checkPass, fmt.Sprintf("held for %s by a running ffc", l.Age.Round(time.Second)), "")
	}
}

// ─── network ─────────────────────────────────────────────────────────────────

// checkNetwork probes the URL without credentials: it must answer, speak
// TLS properly and agree on the time. It reports false when the site cannot
// be reached, so the checks that need it are skipped.
func (d *doctor) checkNetwork(ctx context.Context, cfg *config.SiteConfig) bool {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		d.add("net.reachable", checkFail, fmt.Sprintf("%q is not an http(s) URL", redactedURL(cfg.URL)), "fix the url of the site in the config ('ffc site edit')")
		return false
	}
	d.checkTLS(ctx, u)

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	start := doctorNow()
	resp, err := client.NewHTTPClient(probeTimeout).R().SetContext(ctx).
		SetHeader("Accept", "application/json").
		Get(strings.TrimRight(cfg.URL, "/") + "/api/method/frappe.ping")
	elapsed := doctorNow().Sub(start)
	if err != nil {
		d.add("net.reachable", checkFail, "no answer from "+redactedURL(cfg.URL)+": "+errText(err), "check the URL, your network and any proxy or VPN; 'ffc site edit' fixes the URL")
		return false
	}
	var body struct {
		Message string `json:"message"`
	}
	if resp.StatusCode() != http.StatusOK || json.Unmarshal(resp.Body(), &body) != nil || body.Message != "pong" {
		d.add("net.reachable", checkFail, fmt.Sprintf("%s answered HTTP %d, but not like a Frappe site (no pong from frappe.ping)", redactedURL(cfg.URL), resp.StatusCode()),
			"check the URL points at the Frappe site itself, not a login page or another service")
		return false
	}
	d.add("net.reachable", checkPass, fmt.Sprintf("pong from %s in %s", redactedURL(cfg.URL), elapsed.Round(time.Millisecond)), "")

	// The Date header was set by the server while it handled the request,
	// so the local time in the middle of the exchange is the fair comparison.
	d.checkClock(resp.Header().Get("Date"), start.Add(elapsed/2))
	return true
}

// errText is a transport error's text without the request wrapper.
func errText(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}

func (d *doctor) checkTLS(ctx context.Context, u *url.URL) {
	if u.Scheme == "http" {
		if isLoopback(u.Hostname()) {
			d.add("net.tls", checkPass, "plain http on a loopback address", "")
		} else {
			d.add("net.tls", checkWarn, "plain http: credentials and data travel unencrypted", "use an https:// URL for the site ('ffc site edit')")
		}
		return
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	dialer := tls.Dialer{
		NetDialer: &net.Dialer{},
		Config:    &tls.Config{ServerName: u.Hostname(), RootCAs: doctorTLSRoots, MinVersion: tls.VersionTLS12},
	}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
	if err != nil {
		var ne net.Error
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" || (errors.As(err, &ne) && ne.Timeout()) {
			d.add("net.tls", checkFail, "cannot open a connection to "+net.JoinHostPort(u.Hostname(), port)+": "+errText(err), "see net.reachable")
			return
		}
		d.add("net.tls", checkFail, "TLS check failed: "+errText(err), "install a valid certificate for "+u.Hostname()+" (ffc has no option to skip verification)")
		return
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		d.add("net.tls", checkFail, "the server sent no certificate", "")
		return
	}
	left := certs[0].NotAfter.Sub(doctorNow())
	days := int(left.Hours() / 24)
	if days < certWarnDays {
		d.add("net.tls", checkWarn, fmt.Sprintf("the certificate is valid but ends %s (in %d days)", certs[0].NotAfter.Format("2006-01-02"), days), "renew the certificate")
		return
	}
	d.add("net.tls", checkPass, fmt.Sprintf("certificate valid until %s (%d days)", certs[0].NotAfter.Format("2006-01-02"), days), "")
}

func isLoopback(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (d *doctor) checkClock(date string, local time.Time) {
	if date == "" {
		d.add("net.clock", checkWarn, "the site sent no Date header: the clock cannot be compared", "")
		return
	}
	server, err := http.ParseTime(date)
	if err != nil {
		d.add("net.clock", checkWarn, fmt.Sprintf("the site's Date header %q is not a date", date), "")
		return
	}
	skew := local.Sub(server) // positive: the local clock is ahead
	abs := skew
	if abs < 0 {
		abs = -abs
	}
	dir := "ahead of"
	if skew < 0 {
		dir = "behind"
	}
	msg := fmt.Sprintf("the local clock is %s %s the server's", abs.Round(time.Second), dir)
	switch {
	case abs < 2*time.Second:
		d.add("net.clock", checkPass, "the local clock agrees with the server's", "")
	case abs >= clockFail:
		d.add("net.clock", checkFail, msg, "set the clock right (enable NTP): token expiry and sessions are judged by it")
	case abs >= clockWarn:
		d.add("net.clock", checkWarn, msg, "set the clock right (enable NTP): token expiry and sessions are judged by it")
	default:
		d.add("net.clock", checkPass, msg, "")
	}
}

// ─── auth and server ─────────────────────────────────────────────────────────

func authHint(err error) string {
	code, _ := classify(err)
	switch code {
	case exitAuth:
		return "check the credentials of the site ('ffc site edit'), or sign in again with 'ffc init'"
	case exitPermission:
		return "the user is known but refused: check its roles on the site"
	case exitNetwork:
		return "the site had a problem or could not be reached: see net.reachable, then retry"
	}
	return ""
}

// checkAuthAndServer logs in and reads what the site reports about itself.
func (d *doctor) checkAuthAndServer(ctx context.Context, cfg *config.SiteConfig, refresh bool) {
	fresh := refreshSite(ctx, cfg) // like every command: an expired OAuth token is renewed first
	d.checkOAuth(cfg, fresh)

	c, err := client.New(ctx, fresh)
	if err != nil {
		d.add("auth.valid", checkFail, "cannot sign in: "+err.Error(), authHint(err))
		return
	}
	defer c.CloseQuietly()
	user, err := c.LoggedUser(ctx)
	switch {
	case err != nil:
		d.add("auth.valid", checkFail, "the site refused the credentials: "+err.Error(), authHint(err))
		return
	case user == "Guest":
		d.add("auth.valid", checkFail, "the site sees the credentials as Guest: they are not authenticated", authHint(&client.AuthError{}))
		return
	}
	d.add("auth.valid", checkPass, fmt.Sprintf("authenticated as %s (%s)", user, authMethod(fresh)), "")

	info, cached, err := serverInfo(ctx, c, fresh, refresh)
	if err != nil {
		d.add("server.versions", checkWarn, "cannot read the site's app versions: "+err.Error(), "")
	} else {
		d.checkVersions(info, cached)
	}
	d.checkV2(ctx, c, info)
}

func (d *doctor) checkVersions(info *client.ServerInfo, cached bool) {
	var parts []string
	for _, n := range info.AppNames() {
		parts = append(parts, n+" "+info.Apps[n].Version)
	}
	msg := strings.Join(parts, ", ")
	if cached {
		msg += fmt.Sprintf(" (cached %s ago; --refresh reads them again)", doctorNow().Sub(info.FetchedAt).Round(time.Minute))
	}
	if major := info.FrappeMajor(); major > 0 && major < 15 {
		d.add("server.versions", checkWarn, msg, fmt.Sprintf("ffc is tested against Frappe v15 and v16; v%d may not work", major))
		return
	}
	d.add("server.versions", checkPass, msg, "")
}

func (d *doctor) checkV2(ctx context.Context, c *client.FrappeClient, info *client.ServerInfo) {
	resp, err := c.Raw(ctx, client.RawRequest{Method: http.MethodGet, Path: "/api/v2/method/ping"})
	if err != nil {
		d.add("server.api_v2", checkWarn, "cannot probe /api/v2: "+err.Error(), "")
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	switch {
	case resp.Status == http.StatusOK:
		d.add("server.api_v2", checkPass, "/api/v2 is available (ffc uses /api/v1; ffc api can call v2)", "")
	case info != nil && info.FrappeMajor() >= 16:
		d.add("server.api_v2", checkWarn, fmt.Sprintf("/api/v2 answered HTTP %d, but Frappe v%d provides it", resp.Status, info.FrappeMajor()), "a proxy may block /api/v2")
	default:
		d.add("server.api_v2", checkPass, fmt.Sprintf("/api/v2 is not available (HTTP %d); ffc uses /api/v1, which this site has", resp.Status), "")
	}
}

// checkOAuth reports on an OAuth site's token. orig is the config as stored,
// fresh the one after the renewal refreshSite may have made.
func (d *doctor) checkOAuth(orig, fresh *config.SiteConfig) {
	if !orig.IsOAuth() {
		return
	}
	now := doctorNow()
	switch {
	case orig.TokenExpiry == 0:
		d.add("auth.oauth_token", checkPass, "the access token has no recorded expiry", "")
	case !orig.IsTokenExpired():
		left := time.Unix(orig.TokenExpiry, 0).Sub(now).Round(time.Second)
		if orig.RefreshToken == "" && left < time.Hour {
			d.add("auth.oauth_token", checkWarn, fmt.Sprintf("the access token expires in %s and there is no refresh token", left), "sign in again with 'ffc site add --oauth' before it does")
		} else {
			d.add("auth.oauth_token", checkPass, fmt.Sprintf("the access token expires in %s", left), "")
		}
	case fresh.TokenExpiry != orig.TokenExpiry || fresh.AccessToken != orig.AccessToken:
		d.add("auth.oauth_token", checkPass, fmt.Sprintf("the access token had expired; it was renewed and now expires in %s", time.Unix(fresh.TokenExpiry, 0).Sub(now).Round(time.Second)), "")
	case orig.RefreshToken == "":
		d.add("auth.oauth_token", checkFail, "the access token has expired and there is no refresh token", "sign in again with 'ffc site add --oauth'")
	default:
		d.add("auth.oauth_token", checkFail, "the access token has expired and could not be renewed", "sign in again with 'ffc site add --oauth'")
	}
}

// ─── local state ─────────────────────────────────────────────────────────────

func (d *doctor) checkMCP() {
	state, kind, err := mcpDaemonStatus()
	switch {
	case err != nil:
		d.add("mcp.daemon", checkWarn, "cannot read the MCP server state: "+err.Error(), "remove "+mcpStatePath()+" if no 'ffc mcp --detach' is meant to be running")
		return
	case kind == mcpNotRunning:
		d.add("mcp.daemon", checkPass, "no detached MCP server", "")
		return
	case kind == mcpRunning:
		d.add("mcp.daemon", checkPass, fmt.Sprintf("running: PID %d on port %d for site %s", state.PID, state.Port, state.Site), "")
	case kind == mcpUnresponsive:
		d.add("mcp.daemon", checkWarn, fmt.Sprintf("PID %d is alive but does not answer on port %d (starting up, wedged, or a reused PID)", state.PID, state.Port), "ffc mcp stop --force")
		return
	default:
		d.add("mcp.daemon", checkWarn, fmt.Sprintf("stale state file: PID %d is gone", state.PID), "ffc mcp status removes it")
		return
	}
	// The state file holds the bearer token.
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(mcpStatePath()); err == nil && st.Mode().Perm()&0o077 != 0 {
			d.add("mcp.state_file", checkFail, fmt.Sprintf("%s is readable by other users (mode %04o) and holds the server's bearer token", mcpStatePath(), st.Mode().Perm()), "chmod 600 "+mcpStatePath())
		}
	}
}

func (d *doctor) checkUpdate() {
	if os.Getenv("FFC_NO_UPDATE_CHECK") != "" {
		d.add("update.check", checkPass, "the background update check is off (FFC_NO_UPDATE_CHECK)", "")
		return
	}
	path := updateCheckPath()
	var state updateCheckState
	if b, err := os.ReadFile(path); path == "" || err != nil || json.Unmarshal(b, &state) != nil || state.Latest == "" {
		d.add("update.check", checkPass, "no update check recorded yet; it runs in the background on a later command", "")
		return
	}
	cur := version.Version
	switch {
	case isDevBuild(cur):
		d.add("update.check", checkPass, fmt.Sprintf("development build %s; latest release %s", cur, state.Latest), "")
	case newerThan(cur, state.Latest):
		d.add("update.check", checkWarn, fmt.Sprintf("ffc %s is available (running %s)", state.Latest, cur), "ffc update")
	default:
		d.add("update.check", checkPass, fmt.Sprintf("ffc %s is the latest release (checked %s ago)", cur, doctorNow().Sub(state.CheckedAt).Round(time.Minute)), "")
	}
}

// ─── command ─────────────────────────────────────────────────────────────────

var doctorRefresh bool

func runDoctor(ctx context.Context, refresh bool) []doctorCheck {
	d := &doctor{}
	if cfg := d.checkConfig(); cfg != nil {
		if d.checkNetwork(ctx, cfg) {
			d.checkAuthAndServer(ctx, cfg, refresh)
		}
	}
	d.checkMCP()
	d.checkUpdate()
	return d.checks
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check the setup: config, network, login, server and local state",
	Long: `Run a series of checks on the ffc setup and report each as pass, warn or fail
with a hint to fix it:

  config.file / config.dir   the config file is 0600 and its directory 0700
  config.parse               the file parses and the site resolves
  config.lock                no stale lock left by a crashed ffc
  net.reachable              the URL answers like a Frappe site
  net.tls                    the certificate verifies (plain http is a warning)
  net.clock                  the local clock agrees with the server's (Date header)
  auth.valid                 the credentials log in (the user is named)
  auth.oauth_token           an OAuth token's expiry (renewed when expired)
  server.versions            the installed apps and their versions (cached 24 h)
  server.api_v2              whether /api/v2 exists
  mcp.daemon                 the detached MCP server's health
  update.check               whether a newer ffc was seen

A failed check ends the run with exit code 1; warnings do not. The checks that
need the site are skipped when it cannot be reached. Secrets are never
printed. With --json the output is an array of {check, status, message, hint}.

Examples:
  ffc doctor
  ffc doctor --site prod --json
  ffc doctor --refresh
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var checks []doctorCheck
		if err := runSpinner("Checking…", func() { checks = runDoctor(cmd.Context(), doctorRefresh) }); err != nil {
			return err
		}
		if checks == nil {
			checks = []doctorCheck{}
		}
		if err := render(checks, []string{"check", "status", "message", "hint"}, func() error {
			for _, c := range checks {
				output.PrintCheck(c.Status, c.Check, c.Message, c.Hint)
			}
			return nil
		}); err != nil {
			return err
		}
		failed := 0
		for _, c := range checks {
			if c.Status == checkFail {
				failed++
			}
		}
		if failed > 0 {
			return &codeError{code: exitGeneric, msg: fmt.Sprintf("doctor: %d check(s) failed", failed)}
		}
		return nil
	},
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorRefresh, "refresh", false, "Read the site's app versions again instead of using the cache")
	rootCmd.AddCommand(doctorCmd)
}
