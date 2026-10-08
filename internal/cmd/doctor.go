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

// pingPath is the unauthenticated method the reachability probe calls.
const pingPath = "/api/method/frappe.ping"

// checkNetwork probes the URL without credentials: it must answer, speak
// TLS properly, not redirect, and agree on the time. It reports false when
// the site cannot be reached or redirects, so the checks that need it are
// skipped.
//
// One GET does it all: the certificate is read from that response, so a
// proxy from the environment is honoured (a separate dial would bypass it),
// and a redirect shows in the final URL.
func (d *doctor) checkNetwork(ctx context.Context, cfg *config.SiteConfig) bool {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		d.add("net.reachable", checkFail, fmt.Sprintf("%q is not an http(s) URL", redactedURL(cfg.URL)), "fix the url of the site in the config ('ffc site edit')")
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	rc := client.NewHTTPClient(probeTimeout)
	if doctorTLSRoots != nil {
		rc.SetTLSClientConfig(&tls.Config{RootCAs: doctorTLSRoots, MinVersion: tls.VersionTLS12})
	}
	start := doctorNow()
	resp, err := rc.R().SetContext(ctx).SetHeader("Accept", "application/json").
		Get(strings.TrimRight(cfg.URL, "/") + pingPath)
	elapsed := doctorNow().Sub(start)

	var state *tls.ConnectionState
	if resp != nil && resp.RawResponse != nil {
		state = resp.RawResponse.TLS
	}
	d.checkTLS(u, state, err)
	if err != nil {
		d.add("net.reachable", checkFail, "no answer from "+redactedURL(cfg.URL)+": "+errText(err), "check the URL, your network and any proxy or VPN; 'ffc site edit' fixes the URL")
		return false
	}
	if final := resp.RawResponse.Request.URL; !sameEndpoint(final, resp.Request.RawRequest.URL) {
		site := strings.TrimSuffix(final.String(), pingPath)
		d.add("net.reachable", checkFail, fmt.Sprintf("%s redirected to %s: reads follow it only on the same host, writes (create, update, delete) fail with \"site redirected\"", redactedURL(cfg.URL), final.Redacted()),
			"set the site URL to "+redactedURL(strings.TrimRight(site, "/"))+" ('ffc site edit'), then run doctor again")
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

// isTLSError reports whether err comes from certificate verification or the
// TLS handshake rather than from reaching the host.
func isTLSError(err error) bool {
	var cv *tls.CertificateVerificationError
	var ua x509.UnknownAuthorityError
	var he x509.HostnameError
	var ci x509.CertificateInvalidError
	var rh tls.RecordHeaderError
	if errors.As(err, &cv) || errors.As(err, &ua) || errors.As(err, &he) || errors.As(err, &ci) || errors.As(err, &rh) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "x509:") || strings.Contains(msg, "tls:")
}

// checkTLS judges the connection the probe used: state is the TLS state of
// its response (nil for plain http or a failed request), err the request's
// error.
func (d *doctor) checkTLS(u *url.URL, state *tls.ConnectionState, err error) {
	if u.Scheme == "http" {
		if isLoopback(u.Hostname()) {
			d.add("net.tls", checkPass, "plain http on a loopback address", "")
		} else {
			d.add("net.tls", checkWarn, "plain http: credentials and data travel unencrypted", "use an https:// URL for the site ('ffc site edit')")
		}
		return
	}
	switch {
	case err != nil && isTLSError(err):
		d.add("net.tls", checkFail, "TLS check failed: "+errText(err), "install a valid certificate for "+u.Hostname()+" (ffc has no option to skip verification)")
		return
	case err != nil:
		port := u.Port()
		if port == "" {
			port = "443"
		}
		d.add("net.tls", checkFail, "cannot open a connection to "+net.JoinHostPort(u.Hostname(), port)+": "+errText(err), "see net.reachable")
		return
	case state == nil || len(state.PeerCertificates) == 0:
		d.add("net.tls", checkFail, "the server sent no certificate", "")
		return
	}
	cert := state.PeerCertificates[0]
	days := int(cert.NotAfter.Sub(doctorNow()).Hours() / 24)
	if days < certWarnDays {
		d.add("net.tls", checkWarn, fmt.Sprintf("the certificate is valid but ends %s (in %d days)", cert.NotAfter.Format("2006-01-02"), days), "renew the certificate")
		return
	}
	d.add("net.tls", checkPass, fmt.Sprintf("certificate valid until %s (%d days)", cert.NotAfter.Format("2006-01-02"), days), "")
}

// sameEndpoint reports whether two URLs name the same scheme, host and path:
// what a redirect would change.
func sameEndpoint(a, b *url.URL) bool {
	return a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host) && a.Path == b.Path
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
// It changes nothing: an expired OAuth token is reported, not renewed (the
// next command renews it), and the versions are read live, never through the
// cache. The client is built with client.New, not newClient, for the same
// reason: newClient refreshes the token under the config lock. A password
// site signs in to prove the login works and signs out again.
func (d *doctor) checkAuthAndServer(ctx context.Context, cfg *config.SiteConfig) {
	d.checkOAuth(cfg)
	if cfg.IsOAuth() && cfg.IsTokenExpired() {
		d.add("auth.valid", checkWarn, "not checked: the access token has expired and doctor does not renew it", "run any ffc command that talks to the site: it renews the token; then run doctor again")
		return
	}

	c, err := client.New(ctx, cfg)
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
	d.add("auth.valid", checkPass, fmt.Sprintf("authenticated as %s (%s)", user, authMethod(cfg)), "")

	info, err := c.ServerVersions(ctx)
	if err != nil {
		d.add("server.versions", checkWarn, "cannot read the site's app versions: "+err.Error(), "")
		info = nil
	} else {
		d.checkVersions(info)
	}
	d.checkV2(ctx, c, info)
}

func (d *doctor) checkVersions(info *client.ServerInfo) {
	var parts []string
	for _, n := range info.AppNames() {
		parts = append(parts, n+" "+info.Apps[n].Version)
	}
	msg := strings.Join(parts, ", ")
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

// checkOAuth reports on an OAuth site's token as stored. It renews nothing.
func (d *doctor) checkOAuth(cfg *config.SiteConfig) {
	if !cfg.IsOAuth() {
		return
	}
	now := doctorNow()
	switch {
	case cfg.TokenExpiry == 0:
		d.add("auth.oauth_token", checkPass, "the access token has no recorded expiry", "")
	case !cfg.IsTokenExpired():
		left := time.Unix(cfg.TokenExpiry, 0).Sub(now).Round(time.Second)
		if cfg.RefreshToken == "" && left < time.Hour {
			d.add("auth.oauth_token", checkWarn, fmt.Sprintf("the access token expires in %s and there is no refresh token", left), "sign in again with 'ffc site add --oauth' before it does")
		} else {
			d.add("auth.oauth_token", checkPass, fmt.Sprintf("the access token expires in %s", left), "")
		}
	case cfg.RefreshToken == "":
		d.add("auth.oauth_token", checkFail, "the access token has expired and there is no refresh token", "sign in again with 'ffc site add --oauth'")
	default:
		d.add("auth.oauth_token", checkWarn, "the access token has expired; the next command refreshes it", "if that fails, sign in again with 'ffc site add --oauth'")
	}
}

// ─── local state ─────────────────────────────────────────────────────────────

func (d *doctor) checkMCP() {
	// The state file holds the bearer token, so its mode matters whether or
	// not the server it describes is still alive. Read before the status
	// call, which may drop a stale file.
	path := mcpStatePath()
	var mode fs.FileMode
	haveFile := false
	if runtime.GOOS != "windows" {
		if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
			mode, haveFile = st.Mode().Perm(), true
		}
	}
	d.checkMCPDaemon()
	if haveFile && mode&0o077 != 0 {
		d.add("mcp.state_file", checkFail, fmt.Sprintf("%s is readable by other users (mode %04o) and holds the server's bearer token", path, mode), "chmod 600 "+path)
	}
}

func (d *doctor) checkMCPDaemon() {
	state, kind, err := mcpDaemonStatus()
	switch {
	case err != nil:
		d.add("mcp.daemon", checkWarn, "cannot read the MCP server state: "+err.Error(), "remove "+mcpStatePath()+" if no 'ffc mcp --detach' is meant to be running")
	case kind == mcpNotRunning:
		d.add("mcp.daemon", checkPass, "no detached MCP server", "")
	case kind == mcpRunning:
		d.add("mcp.daemon", checkPass, fmt.Sprintf("running: PID %d on port %d for site %s", state.PID, state.Port, state.Site), "")
	case kind == mcpUnresponsive:
		d.add("mcp.daemon", checkWarn, fmt.Sprintf("PID %d is alive but does not answer on port %d (starting up, wedged, or a reused PID)", state.PID, state.Port), "ffc mcp stop --force")
	default:
		d.add("mcp.daemon", checkWarn, fmt.Sprintf("stale state file: PID %d is gone", state.PID), "ffc mcp status removes it")
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
		d.add("update.check", checkWarn, fmt.Sprintf("ffc %s is available (running %s)", state.Latest, cur), updateCommand())
	default:
		d.add("update.check", checkPass, fmt.Sprintf("ffc %s is the latest release (checked %s ago)", cur, doctorNow().Sub(state.CheckedAt).Round(time.Minute)), "")
	}
}

// ─── command ─────────────────────────────────────────────────────────────────

func runDoctor(ctx context.Context) []doctorCheck {
	d := &doctor{}
	if cfg := d.checkConfig(); cfg != nil {
		if d.checkNetwork(ctx, cfg) {
			d.checkAuthAndServer(ctx, cfg)
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
  net.reachable              the URL answers like a Frappe site, without redirecting
  net.tls                    the certificate verifies (plain http is a warning)
  net.clock                  the local clock agrees with the server's (Date header)
  auth.valid                 the credentials log in (the user is named)
  auth.oauth_token           an OAuth token's expiry (reported, not renewed)
  server.versions            the installed apps and their versions (read live)
  server.api_v2              whether /api/v2 exists
  mcp.daemon                 the detached MCP server's health
  mcp.state_file             its state file (it holds a token) is not readable by others
  update.check               whether a newer ffc was seen

A failed check ends the run with exit code 1; warnings do not. The checks that
need the site are skipped when it cannot be reached or redirects. Secrets are
never printed. With --json the output is an array of {check, status, message,
hint}.

doctor changes nothing: it does not renew an expired OAuth token (it reports
that the next command will), it neither reads nor writes the server version
cache (the versions are always read live; use 'ffc whoami --refresh' to
refresh the cache), and a password site signs in and out again. The network
probe is one GET of /api/method/frappe.ping through your proxy settings; the
certificate is read from that response.

Examples:
  ffc doctor
  ffc doctor --site prod --json
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		var checks []doctorCheck
		if err := runSpinner("Checking…", func() { checks = runDoctor(cmd.Context()) }); err != nil {
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
	rootCmd.AddCommand(doctorCmd)
}
