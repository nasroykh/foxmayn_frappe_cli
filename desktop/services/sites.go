package services

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/sitecache"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/sitesetup"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Auth methods as the frontend names them.
const (
	AuthOAuth    = "oauth"
	AuthAPIKey   = "apikey"
	AuthPassword = "password"
	AuthNone     = "none"
)

// revokeTimeout bounds the token revocation when a site is removed, as in
// the CLI's site remove.
var revokeTimeout = 10 * time.Second

// Site is one configured site as the UI shows it: never a secret.
type Site struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Auth      string `json:"auth"`
	IsDefault bool   `json:"isDefault"`
	// PlainHTTP: the URL is http://, so credentials travel unencrypted.
	PlainHTTP bool `json:"plainHTTP"`
	// Username is the stored login of a username/password site (not the
	// password). Empty for other methods.
	Username string `json:"username,omitempty"`
	// LastCheck is the result of the last connection check in this run.
	LastCheck *CheckResult `json:"lastCheck,omitempty"`
}

// SiteList is the config's sites, sorted by name.
type SiteList struct {
	ConfigPath   string `json:"configPath"`
	ConfigExists bool   `json:"configExists"`
	DefaultSite  string `json:"defaultSite"`
	Sites        []Site `json:"sites"`
}

// CheckResult is the outcome of a connection check.
type CheckResult struct {
	OK bool `json:"ok"`
	// User is who the site says is signed in ("" when it does not say, or
	// for a username/password site, where Frappe does not answer it).
	User      string    `json:"user"`
	Message   string    `json:"message"`
	Code      string    `json:"code,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
	// Key and Args translate Message, as on Error.
	Key  string            `json:"key,omitempty"`
	Args map[string]string `json:"args,omitempty"`
}

// setError takes the code, message and key of e.
func (r *CheckResult) setError(e *Error) {
	r.Code, r.Message, r.Key, r.Args = e.Code, e.Message, e.Key, e.Args
}

// setKeyed sets the code and the message of key (errorKeys).
func (r *CheckResult) setKeyed(code, key string) {
	r.setError(keyed(code, key, nil))
}

// Validation is the check of a name and URL as typed in step 1 of the Add
// site form. Errors are per field; it never fails as a call.
type Validation struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	NameError string `json:"nameError,omitempty"`
	URLError  string `json:"urlError,omitempty"`
	// Exists: a site with this name is configured already.
	Exists    bool `json:"exists"`
	PlainHTTP bool `json:"plainHTTP"`
	OK        bool `json:"ok"`
}

// APIKeyRequest adds a site with an API key and secret.
type APIKeyRequest struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	APIKey    string `json:"apiKey"`
	APISecret string `json:"apiSecret"`
	// Replace an existing site of the same name.
	Replace bool `json:"replace"`
}

// PasswordRequest adds a site with a username (or email) and password.
type PasswordRequest struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
	Replace  bool   `json:"replace"`
}

// BrowserSignInRequest adds a site through the OAuth browser sign-in.
type BrowserSignInRequest struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	// ClientID and ClientSecret name an OAuth Client created on the site by
	// hand (the CLI's --client-id and FFC_OAUTH_CLIENT_SECRET). Empty: the
	// app registers one when the site allows it.
	ClientID     string `json:"clientID"`
	ClientSecret string `json:"clientSecret"`
	Replace      bool   `json:"replace"`
	// Attempt is an ID the UI picks for this sign-in; every progress event
	// of it carries the same ID.
	Attempt string `json:"attempt"`
}

// AddedSite is the site an Add call saved.
type AddedSite struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Auth      string `json:"auth"`
	User      string `json:"user"`
	IsDefault bool   `json:"isDefault"`
	// Replaced: an existing site of this name was replaced.
	Replaced bool `json:"replaced"`
	// Registered (OAuth): the app registered its own OAuth Client.
	Registered bool `json:"registered"`
}

// RemoveResult is what removing a site did besides deleting it.
type RemoveResult struct {
	// Revoked: the site's OAuth token was revoked on the server.
	Revoked bool `json:"revoked"`
	// RevokeError is set when revoking failed; the site was removed anyway
	// and its token stays valid until it expires or is revoked by hand.
	RevokeError string `json:"revokeError,omitempty"`
	WasDefault  bool   `json:"wasDefault"`
	NewDefault  string `json:"newDefault"`
}

// SitesService manages the sites in the ffc config file. Every write goes
// through sitesetup.Store (config.Edit or config.Overwrite: lock, re-read,
// atomic write), except the OAuth token refresh before a check, which uses
// config.Edit the way the CLI does.
type SitesService struct {
	host  Host
	path  string
	store sitesetup.Store

	// Seams for tests.
	verify      func(ctx context.Context, site config.SiteConfig) (string, error)
	revoke      func(ctx context.Context, site config.SiteConfig, timeout time.Duration) (bool, error)
	refresh     func(ctx context.Context, siteURL, clientID, clientSecret, refreshToken string) (*client.OAuthTokens, error)
	startFlow   func() (*sitesetup.OAuthFlow, error)
	resolveApp  func(ctx context.Context, siteURL, redirectURI string, manual sitesetup.OAuthApp) (sitesetup.OAuthApp, error)
	pollEvery   time.Duration
	flowTimeout time.Duration

	mu     sync.Mutex
	checks map[string]CheckResult // by site name, for this run
	signIn *signInState

	watchCancel context.CancelFunc
}

// NewSitesService manages the sites of the config file at path. A removed,
// renamed, re-URLed or replaced site loses its local ffc cache, as in the CLI.
func NewSitesService(host Host, path string) *SitesService {
	return &SitesService{
		host:       host,
		path:       path,
		store:      sitesetup.Store{Path: path, DropCache: sitecache.Drop},
		verify:     sitesetup.Verify,
		revoke:     sitesetup.RevokeToken,
		refresh:    client.RefreshOAuthToken,
		startFlow:  sitesetup.StartOAuthFlow,
		resolveApp: sitesetup.ResolveOAuthApp,
		pollEvery:  time.Second,
		checks:     map[string]CheckResult{},
	}
}

// ServiceStartup starts watching the config file.
func (s *SitesService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.startWatching(ctx)
	return nil
}

// startWatching polls the config file (mtime and size, every pollEvery) and
// emits "config:changed" when it changes, until ctx ends or
// ServiceShutdown runs.
func (s *SitesService) startWatching(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if s.watchCancel != nil {
		s.watchCancel()
	}
	s.watchCancel = cancel
	s.mu.Unlock()
	w := &configWatcher{path: s.path, every: s.pollEvery, onChange: func(exists bool) {
		s.host.Emit(EventConfigChanged, ConfigChanged{Path: s.path, Exists: exists})
	}}
	go w.run(ctx)
}

// ServiceShutdown stops the watcher and any sign-in in progress.
func (s *SitesService) ServiceShutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watchCancel != nil {
		s.watchCancel()
	}
	if s.signIn != nil {
		s.signIn.cancel()
	}
	return nil
}

// readConfig reads the config; a missing file is an empty config.
func (s *SitesService) readConfig() (*config.Config, bool, error) {
	cfg, err := config.Read(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return &config.Config{Sites: map[string]config.SiteConfig{}}, false, nil
	}
	if err != nil {
		return nil, true, newError(CodeFailed, "The ffc config file could not be read. Fix or remove it, then try again.", err)
	}
	if cfg.Sites == nil {
		cfg.Sites = map[string]config.SiteConfig{}
	}
	return cfg, true, nil
}

func authMethod(site config.SiteConfig) string {
	switch {
	case site.IsOAuth():
		return AuthOAuth
	case site.APIKey != "" && site.APISecret != "":
		return AuthAPIKey
	case site.IsSessionAuth():
		return AuthPassword
	}
	return AuthNone
}

// List returns the configured sites.
func (s *SitesService) List() (SiteList, error) {
	cfg, exists, err := s.readConfig()
	if err != nil {
		return SiteList{}, err
	}
	out := SiteList{ConfigPath: s.path, ConfigExists: exists, DefaultSite: cfg.DefaultSite, Sites: []Site{}}
	names := make([]string, 0, len(cfg.Sites))
	for n := range cfg.Sites {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range names {
		sc := cfg.Sites[n]
		site := Site{
			Name:      n,
			URL:       sc.URL,
			Auth:      authMethod(sc),
			IsDefault: n == cfg.DefaultSite,
			PlainHTTP: strings.HasPrefix(strings.ToLower(sc.URL), "http://"),
		}
		if site.Auth == AuthPassword {
			site.Username = text.Sanitize(sc.Username)
		}
		if c, ok := s.checks[n]; ok {
			c := c
			site.LastCheck = &c
		}
		out.Sites = append(out.Sites, site)
	}
	return out, nil
}

// Validate checks a site name and URL as typed.
func (s *SitesService) Validate(name, rawURL string) Validation {
	v := Validation{Name: strings.TrimSpace(name)}
	if err := sitesetup.ValidateName(v.Name); err != nil {
		v.NameError = nameMessage(v.Name, err)
	}
	if strings.TrimSpace(rawURL) == "" {
		v.URLError = "Enter the address of your site, like erp.example.com."
	} else if u, err := sitesetup.NormalizeURL(rawURL); err != nil {
		v.URLError = "This doesn't look like a web address: " + text.Sanitize(err.Error())
	} else {
		v.URL = u
		v.PlainHTTP = strings.HasPrefix(u, "http://")
	}
	if v.NameError == "" {
		if cfg, _, err := s.readConfig(); err == nil {
			_, v.Exists = cfg.Sites[v.Name]
		}
	}
	v.OK = v.NameError == "" && v.URLError == ""
	return v
}

func nameMessage(name string, err error) string {
	if name == "" {
		return "Give the site a name, like \"Main office\"."
	}
	return "This name can't be used: " + text.Sanitize(err.Error())
}

// prepare validates a request's name and URL and refuses an existing name
// unless replace is set.
func (s *SitesService) prepare(name, rawURL string, replace bool) (string, string, bool, error) {
	v := s.Validate(name, rawURL)
	switch {
	case v.NameError != "":
		return "", "", false, invalid("name", v.NameError)
	case v.URLError != "":
		return "", "", false, invalid("url", v.URLError)
	case v.Exists && !replace:
		return "", "", false, &Error{Code: CodeExists, Field: "name",
			Message: fmt.Sprintf("You already have a site called %q. Pick another name, or choose to replace it.", v.Name)}
	}
	return v.Name, v.URL, v.Exists, nil
}

// save stores a verified site with Store.AddOrInit: a missing config file is
// created with the site as the default (as ffc init writes it), an existing
// one is added to (the site becomes the default when none is set). The
// choice is made under the config lock, so a file the CLI creates meanwhile
// is never overwritten.
func (s *SitesService) save(name string, site config.SiteConfig, added *AddedSite) error {
	if err := s.store.AddOrInit(name, site); err != nil {
		return newError(CodeFailed, "The site could not be saved to the ffc config file.", err)
	}
	if cfg, _, err := s.readConfig(); err == nil {
		added.IsDefault = cfg.DefaultSite == name
	}
	s.mu.Lock()
	s.checks[name] = CheckResult{OK: true, User: added.User, Message: "Connected", CheckedAt: time.Now()}
	s.mu.Unlock()
	return nil
}

// AddWithAPIKey checks an API key and secret against the site and saves it.
func (s *SitesService) AddWithAPIKey(ctx context.Context, req APIKeyRequest) (AddedSite, error) {
	name, siteURL, exists, err := s.prepare(req.Name, req.URL, req.Replace)
	if err != nil {
		return AddedSite{}, err
	}
	site := config.SiteConfig{URL: siteURL, APIKey: strings.TrimSpace(req.APIKey), APISecret: strings.TrimSpace(req.APISecret)}
	if site.APIKey == "" {
		return AddedSite{}, invalid("apiKey", "Enter the API key.")
	}
	if site.APISecret == "" {
		return AddedSite{}, invalid("apiSecret", "Enter the API secret.")
	}
	user, err := s.verify(ctx, site)
	if err != nil {
		return AddedSite{}, siteError(stepAPIKey, err)
	}
	added := AddedSite{Name: name, URL: siteURL, Auth: AuthAPIKey, User: text.Sanitize(user), Replaced: exists}
	return added, s.save(name, site, &added)
}

// AddWithPassword checks a username and password by logging in (and out
// again) and saves them. Frappe accounts with two-factor sign-in cannot use
// this method.
func (s *SitesService) AddWithPassword(ctx context.Context, req PasswordRequest) (AddedSite, error) {
	name, siteURL, exists, err := s.prepare(req.Name, req.URL, req.Replace)
	if err != nil {
		return AddedSite{}, err
	}
	// The password is stored verbatim, as the CLI does.
	site := config.SiteConfig{URL: siteURL, Username: strings.TrimSpace(req.Username), Password: req.Password}
	if site.Username == "" {
		return AddedSite{}, invalid("username", "Enter your username or email.")
	}
	if site.Password == "" {
		return AddedSite{}, invalid("password", "Enter your password.")
	}
	if _, err := s.verify(ctx, site); err != nil {
		return AddedSite{}, siteError(stepSignIn, err)
	}
	added := AddedSite{Name: name, URL: siteURL, Auth: AuthPassword, User: text.Sanitize(site.Username), Replaced: exists}
	return added, s.save(name, site, &added)
}

// lookup returns a configured site by its exact name.
func (s *SitesService) lookup(name string) (config.SiteConfig, *config.Config, error) {
	cfg, _, err := s.readConfig()
	if err != nil {
		return config.SiteConfig{}, nil, err
	}
	site, ok := cfg.Sites[name]
	if !ok {
		return config.SiteConfig{}, nil, &Error{Code: CodeNotFound, Message: fmt.Sprintf("There is no site called %q any more.", name)}
	}
	site.Name = name
	return site, cfg, nil
}

// Check tests a site's stored credentials. An expired OAuth token is
// refreshed first and the new token saved, as ffc does before talking to a
// site. The result is remembered for List until the app quits.
func (s *SitesService) Check(ctx context.Context, name string) (CheckResult, error) {
	site, _, err := s.lookup(name)
	if err != nil {
		return CheckResult{}, err
	}
	res := CheckResult{CheckedAt: time.Now()}
	if site.IsOAuth() && site.IsTokenExpired() {
		if site, err = s.refreshToken(ctx, site); err != nil {
			if ctx.Err() != nil {
				return CheckResult{}, siteError(stepCheck, ctx.Err())
			}
			if signInExpired(err) {
				res.setKeyed(CodeAuth, "site.signInExpired")
			} else {
				// The network, the site or the config lock: trying again may work.
				var e *Error
				if errors.As(siteError(stepRenew, err), &e) {
					res.setError(e)
				}
			}
			s.remember(name, res)
			return res, nil
		}
	}
	user, err := s.verify(ctx, site)
	if err != nil {
		if ctx.Err() != nil {
			return CheckResult{}, siteError(stepCheck, ctx.Err())
		}
		var e *Error
		if errors.As(siteError(stepCheck, err), &e) {
			res.setError(e)
			if e.Code == CodeAuth && site.IsOAuth() {
				res.setKeyed(CodeAuth, "site.signInRejected")
			}
		}
	} else {
		res.OK, res.User = true, text.Sanitize(user)
		res.setKeyed("", "site.connected")
	}
	s.remember(name, res)
	return res, nil
}

// signInExpired reports whether a failed token refresh means the sign-in is
// over, as the CLI's permanentRefreshError does: no refresh token, a refused
// login, or the token endpoint refusing the grant or the client (400, 401,
// or Frappe's 403 for a revoked refresh token). Anything else (network,
// timeout, a 5xx, a busy config lock) may pass.
func signInExpired(err error) bool {
	var ae *client.APIError
	var authErr *client.AuthError
	switch {
	case errors.Is(err, client.ErrNoRefreshToken), errors.As(err, &authErr):
		return true
	case errors.As(err, &ae):
		return ae.Status == http.StatusBadRequest || ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden
	}
	return false
}

func (s *SitesService) remember(name string, res CheckResult) {
	s.mu.Lock()
	s.checks[name] = res
	s.mu.Unlock()
}

// refreshToken refreshes an expired OAuth token and stores it, under the
// config lock like the CLI's refreshOAuth: when another process refreshed
// meanwhile, its token is used instead of spending the refresh token again.
func (s *SitesService) refreshToken(ctx context.Context, site config.SiteConfig) (config.SiteConfig, error) {
	ctx, cancel := context.WithTimeout(ctx, config.MaxLockHold)
	defer cancel()
	out := site
	err := config.Edit(s.path, func(f *config.File) error {
		cur, ok := f.Site(site.Name)
		if !ok {
			return fmt.Errorf("site %q not found", site.Name)
		}
		if cur.AccessToken != "" && !cur.IsTokenExpired() {
			out.AccessToken, out.RefreshToken, out.TokenExpiry = cur.AccessToken, cur.RefreshToken, cur.TokenExpiry
			return config.ErrUnchanged
		}
		if cur.RefreshToken == "" {
			return client.ErrNoRefreshToken
		}
		tokens, err := s.refresh(ctx, cur.URL, cur.OAuthClientID, cur.OAuthClientSecret, cur.RefreshToken)
		if err != nil {
			return err
		}
		out.AccessToken, out.TokenExpiry = tokens.AccessToken, tokens.ExpiresAt
		if tokens.RefreshToken != "" {
			out.RefreshToken = tokens.RefreshToken
		}
		return f.SetSiteTokens(site.Name, tokens.AccessToken, tokens.RefreshToken, tokens.ExpiresAt)
	})
	return out, err
}

// SetDefault makes a site the default (what ffc and "follow my default"
// assistants use).
func (s *SitesService) SetDefault(name string) error {
	if err := s.store.SetDefault(name); err != nil {
		return newError(CodeFailed, "The default site could not be changed.", err)
	}
	return nil
}

// Rename renames a site. The default follows it; an assistant pinned to the
// old name must be connected again.
func (s *SitesService) Rename(oldName, newName string) error {
	newName = strings.TrimSpace(newName)
	if err := sitesetup.ValidateName(newName); err != nil {
		return invalid("name", nameMessage(newName, err))
	}
	if newName == oldName {
		return nil
	}
	if err := s.store.Rename(oldName, newName); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return &Error{Code: CodeExists, Field: "name", Message: fmt.Sprintf("You already have a site called %q.", newName)}
		}
		return newError(CodeFailed, "The site could not be renamed.", err)
	}
	s.mu.Lock()
	if c, ok := s.checks[oldName]; ok {
		s.checks[newName] = c
		delete(s.checks, oldName)
	}
	s.mu.Unlock()
	return nil
}

// ChangeURL points a site at a new address after checking its stored
// credentials there. OAuth sites are refused: their OAuth client and tokens
// belong to the old server, so they sign in again instead (as in the CLI).
func (s *SitesService) ChangeURL(ctx context.Context, name, rawURL string) (string, error) {
	site, _, err := s.lookup(name)
	if err != nil {
		return "", err
	}
	if site.IsOAuth() {
		return "", &Error{Code: CodeUnavailable, Message: "This site uses browser sign-in, which belongs to its current address. Sign in again with the new address instead."}
	}
	newURL, err := sitesetup.NormalizeURL(rawURL)
	if err != nil {
		return "", invalid("url", "This doesn't look like a web address: "+text.Sanitize(err.Error()))
	}
	site.URL = newURL
	if _, err := s.verify(ctx, site); err != nil {
		return "", siteError(stepNewURL, err)
	}
	if err := s.store.SetURL(name, newURL); err != nil {
		return "", newError(CodeFailed, "The new address could not be saved.", err)
	}
	s.mu.Lock()
	delete(s.checks, name)
	s.mu.Unlock()
	return newURL, nil
}

// Remove deletes a site. An OAuth site's token is revoked on the server
// first, at most revokeTimeout, before the config lock is taken; a failed
// revocation is reported but does not stop the removal (as in the CLI).
func (s *SitesService) Remove(ctx context.Context, name string) (RemoveResult, error) {
	site, _, err := s.lookup(name)
	if err != nil {
		return RemoveResult{}, err
	}
	var res RemoveResult
	if site.RefreshToken != "" || site.AccessToken != "" {
		revoked, err := s.revoke(ctx, site, revokeTimeout)
		if ctx.Err() != nil {
			return RemoveResult{}, newError(CodeCancelled, "Removing the site was cancelled.", nil)
		}
		res.Revoked = revoked && err == nil
		if err != nil {
			res.RevokeError = text.Sanitize(err.Error())
		}
	}
	removed, err := s.store.Remove(name)
	if err != nil {
		return RemoveResult{}, newError(CodeFailed, "The site could not be removed.", err)
	}
	res.WasDefault, res.NewDefault = removed.WasDefault, removed.NewDefault
	s.mu.Lock()
	delete(s.checks, name)
	s.mu.Unlock()
	return res, nil
}
