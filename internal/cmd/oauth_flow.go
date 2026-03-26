package cmd

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/huh/spinner"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/config"

	"go.yaml.in/yaml/v3"
)

// ─── PKCE ────────────────────────────────────────────────────────────────────

func generateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// ─── Browser ─────────────────────────────────────────────────────────────────

func openBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Start()
}

// ─── Local callback server ───────────────────────────────────────────────────

// callbackServer holds a running local HTTP server waiting for the OAuth callback.
type callbackServer struct {
	port   int
	codeCh chan string
	errCh  chan error
	srv    *http.Server
}

// startCallbackServer binds to a free port and starts the HTTP server immediately.
// Call this before showing the redirect URI to the user so the port is guaranteed
// to be held when Frappe redirects back.
func startCallbackServer() (*callbackServer, error) {
	// Bind the port first so we hold it for the lifetime of the flow.
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, fmt.Errorf("binding callback port: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	srv := &http.Server{
		Addr:    fmt.Sprintf("localhost:%d", port),
		Handler: mux,
	}

	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if errParam := r.URL.Query().Get("error"); errParam != "" {
			desc := r.URL.Query().Get("error_description")
			msg := errParam
			if desc != "" {
				msg += ": " + desc
			}
			errCh <- fmt.Errorf("authorization denied — %s", msg)
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "<html><body style='font-family:sans-serif;padding:2rem'><h2>Authorization denied</h2><p>%s</p><p>You can close this tab.</p></body></html>", msg)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			errCh <- fmt.Errorf("no authorization code in callback URL")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "<html><body style='font-family:sans-serif;padding:2rem'><h2>Error</h2><p>No code received.</p></body></html>")
			return
		}
		codeCh <- code
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `<html><body style="font-family:sans-serif;padding:2rem;text-align:center">
<h2>&#10003; Authorization successful</h2>
<p>You can close this tab and return to the terminal.</p>
</body></html>`)
	})

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	return &callbackServer{port: port, codeCh: codeCh, errCh: errCh, srv: srv}, nil
}

// wait blocks until the OAuth callback delivers a code or the 5-minute timeout elapses.
func (cs *callbackServer) wait() (string, error) {
	defer cs.srv.Shutdown(context.Background()) //nolint:errcheck
	select {
	case code := <-cs.codeCh:
		return code, nil
	case err := <-cs.errCh:
		return "", err
	case <-time.After(5 * time.Minute):
		return "", fmt.Errorf("timed out waiting for browser authorization (5 minutes)")
	}
}

// ─── OAuth init wizard ───────────────────────────────────────────────────────

// runOAuthInitFlow runs the full interactive OAuth setup wizard.
// It handles the huh forms, browser launch, token exchange, and config writing.
func runOAuthInitFlow(cfgPath string) error {
	// ── Step 1: site name + URL ──────────────────────────────────────────────
	var siteName, siteURL string

	siteForm := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Site name").
				Description("A short identifier, e.g. dev or production").
				Placeholder("dev").
				Validate(func(s string) error {
					s = strings.TrimSpace(s)
					if s == "" {
						return fmt.Errorf("site name cannot be empty")
					}
					if strings.ContainsAny(s, " \t") {
						return fmt.Errorf("site name must not contain spaces")
					}
					return nil
				}).
				Value(&siteName),

			huh.NewInput().
				Title("Site URL").
				Description("Base URL of your Frappe site (https:// added if omitted)").
				Placeholder("mysite.example.com").
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return fmt.Errorf("URL cannot be empty")
					}
					return nil
				}).
				Value(&siteURL),
		),
	)

	if err := siteForm.WithKeyMap(escQuitKeyMap()).Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(os.Stderr, "Aborted. No config written.")
			return nil
		}
		return err
	}

	siteName = strings.TrimSpace(siteName)
	siteURL = strings.TrimRight(strings.TrimSpace(siteURL), "/")
	if !strings.HasPrefix(siteURL, "http://") && !strings.HasPrefix(siteURL, "https://") {
		siteURL = "https://" + siteURL
	}

	// ── Step 2: start callback server (before showing instructions so the port
	//            is guaranteed held when Frappe redirects back) ───────────────
	cs, err := startCallbackServer()
	if err != nil {
		return err
	}
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", cs.port)

	fmt.Fprintf(os.Stderr, `
OAuth Client setup (one-time, on your Frappe site)
──────────────────────────────────────────────────
1. Go to: %s/app/oauth-client/new-oauth-client-1
2. Fill in:
     App Name:      ffc (or any name)
     Grant Type:    Authorization Code
     Scopes:        openid all
     Redirect URIs: %s
3. Save → copy the Client ID (and Client Secret if using Confidential type).

`, siteURL, redirectURI)

	// ── Step 3: client ID + secret ──────────────────────────────────────────
	var clientID, clientSecret string

	credForm := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("OAuth Client ID").
				Description("From the OAuth Client you just created on Frappe").
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return fmt.Errorf("Client ID cannot be empty")
					}
					return nil
				}).
				Value(&clientID),

			huh.NewInput().
				Title("OAuth Client Secret").
				Description("Leave empty if using a Public client (no secret)").
				EchoMode(huh.EchoModePassword).
				Value(&clientSecret),
		),
	)

	if err := credForm.WithKeyMap(escQuitKeyMap()).Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(os.Stderr, "Aborted. No config written.")
			return nil
		}
		return err
	}

	clientID = strings.TrimSpace(clientID)

	// ── Step 4: run OAuth flow ───────────────────────────────────────────────
	verifier, err := generateCodeVerifier()
	if err != nil {
		return fmt.Errorf("generating PKCE verifier: %w", err)
	}
	challenge := generateCodeChallenge(verifier)

	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {"openid all"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	authURL := strings.TrimRight(siteURL, "/") + "/api/method/frappe.integrations.oauth2.authorize?" + params.Encode()

	fmt.Fprintf(os.Stderr, "\nOpening browser for authorization...\n")
	fmt.Fprintf(os.Stderr, "If the browser doesn't open automatically, visit:\n  %s\n\n", authURL)

	if err := openBrowser(authURL); err != nil {
		fmt.Fprintf(os.Stderr, "(Could not open browser: %v)\n\n", err)
	}

	fmt.Fprintf(os.Stderr, "Waiting for authorization (timeout: 5 minutes)...\n")

	code, err := cs.wait()
	if err != nil {
		return fmt.Errorf("authorization: %w", err)
	}

	// ── Step 5: exchange code for tokens ─────────────────────────────────────
	var tokens *client.OAuthTokens
	var exchangeErr error
	_ = spinner.New().
		Title("Exchanging authorization code for tokens...").
		Action(func() {
			tokens, exchangeErr = client.ExchangeOAuthCode(siteURL, clientID, clientSecret, code, redirectURI, verifier)
		}).
		Run()
	if exchangeErr != nil {
		return fmt.Errorf("token exchange: %w", exchangeErr)
	}

	// ── Step 6: fetch logged-in user ─────────────────────────────────────────
	var loggedUser string
	_ = spinner.New().
		Title("Fetching user info...").
		Action(func() {
			loggedUser, _ = client.GetOAuthUser(siteURL, tokens.AccessToken)
		}).
		Run()

	// ── Step 7: review + confirm ─────────────────────────────────────────────
	userDisplay := loggedUser
	if userDisplay == "" {
		userDisplay = "(unknown)"
	}

	var reviewChoice string
	reviewErr := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Review OAuth configuration").
				Description(fmt.Sprintf(
					"Site name:  %s\nSite URL:   %s\nClient ID:  %s\nLogged in:  %s",
					siteName, siteURL, clientID, userDisplay,
				)).
				Options(
					huh.NewOption("Confirm", "confirm"),
					huh.NewOption("Cancel", "cancel"),
				).
				Value(&reviewChoice),
		),
	).WithKeyMap(escQuitKeyMap()).Run()

	if errors.Is(reviewErr, huh.ErrUserAborted) || reviewChoice == "cancel" {
		fmt.Fprintln(os.Stderr, "Aborted. No config written.")
		return nil
	}
	if reviewErr != nil {
		return reviewErr
	}

	// ── Step 8: write config ─────────────────────────────────────────────────
	var writeErr error
	_ = spinner.New().
		Title("Writing config...").
		Action(func() {
			writeErr = writeConfigOAuth(cfgPath, siteName, siteURL, clientID, clientSecret, tokens)
		}).
		Run()
	if writeErr != nil {
		return fmt.Errorf("write config: %w", writeErr)
	}

	fmt.Fprintf(os.Stderr, "\n✓ Config written to %s\n", cfgPath)
	fmt.Fprintf(os.Stderr, "  Logged in as: %s\n", userDisplay)
	fmt.Fprintf(os.Stderr, "  Run: ffc --site %s list-docs --doctype \"Sales Invoice\"\n", siteName)
	return nil
}

// ─── Config writing ──────────────────────────────────────────────────────────

// writeConfigOAuth writes a fresh config file for an OAuth-authenticated site.
func writeConfigOAuth(path, siteName, siteURL, clientID, clientSecret string, tokens *client.OAuthTokens) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	expiry := int64(0)
	if tokens.ExpiresIn > 0 {
		expiry = time.Now().Unix() + int64(tokens.ExpiresIn)
	}

	var sb strings.Builder
	sb.WriteString("# ffc configuration — generated by 'ffc init --oauth'\n")
	sb.WriteString("# Edit this file to add more sites.\n\n")
	sb.WriteString(fmt.Sprintf("default_site: %s\n\n", siteName))
	sb.WriteString("sites:\n")
	sb.WriteString(fmt.Sprintf("  %s:\n", siteName))
	sb.WriteString(fmt.Sprintf("    url: %q\n", siteURL))
	sb.WriteString(fmt.Sprintf("    oauth_client_id: %q\n", clientID))
	if clientSecret != "" {
		sb.WriteString(fmt.Sprintf("    oauth_client_secret: %q\n", clientSecret))
	}
	sb.WriteString(fmt.Sprintf("    access_token: %q\n", tokens.AccessToken))
	sb.WriteString(fmt.Sprintf("    refresh_token: %q\n", tokens.RefreshToken))
	sb.WriteString(fmt.Sprintf("    token_expiry: %d\n", expiry))

	return os.WriteFile(path, []byte(sb.String()), 0o600)
}

// saveOAuthTokens updates only the OAuth token fields for a specific site in
// the existing config file, preserving comments and other settings.
func saveOAuthTokens(cfgPath, siteName string, tokens *client.OAuthTokens) error {
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("parsing config: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return fmt.Errorf("unexpected config format")
	}

	if err := updateSiteTokens(root.Content[0], siteName, tokens); err != nil {
		return err
	}

	out, err := yaml.Marshal(root.Content[0])
	if err != nil {
		return fmt.Errorf("serialising config: %w", err)
	}

	return os.WriteFile(cfgPath, []byte(preserveHeader(raw, out)), 0o600)
}

// updateSiteTokens finds sites.<siteName> in the YAML mapping and updates
// access_token, refresh_token, and token_expiry.
func updateSiteTokens(mapping *yaml.Node, siteName string, tokens *client.OAuthTokens) error {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != "sites" {
			continue
		}
		sitesMap := mapping.Content[i+1]
		if sitesMap.Kind != yaml.MappingNode {
			return fmt.Errorf("sites is not a mapping node")
		}
		for j := 0; j+1 < len(sitesMap.Content); j += 2 {
			if sitesMap.Content[j].Value != siteName {
				continue
			}
			siteMap := sitesMap.Content[j+1]
			if siteMap.Kind != yaml.MappingNode {
				return fmt.Errorf("site %q is not a mapping node", siteName)
			}
			expiry := int64(0)
			if tokens.ExpiresIn > 0 {
				expiry = time.Now().Unix() + int64(tokens.ExpiresIn)
			}
			setScalarNode(siteMap, "access_token", tokens.AccessToken, "!!str")
			if tokens.RefreshToken != "" {
				setScalarNode(siteMap, "refresh_token", tokens.RefreshToken, "!!str")
			}
			setScalarNode(siteMap, "token_expiry", fmt.Sprintf("%d", expiry), "!!int")
			return nil
		}
		return fmt.Errorf("site %q not found in config", siteName)
	}
	return fmt.Errorf("sites section not found in config")
}

// setScalarNode sets or appends a scalar key/value in a YAML mapping node.
func setScalarNode(mapping *yaml.Node, key, value, tag string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].Value = value
			mapping.Content[i+1].Tag = tag
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value, Tag: tag},
	)
}

// ─── Config helpers (used by site add / init) ────────────────────────────────

// buildAPIKeySiteYAML returns the YAML block for an API-key-authenticated site.
func buildAPIKeySiteYAML(siteURL, apiKey, apiSecret string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "url: %q\n", siteURL)
	fmt.Fprintf(&sb, "api_key: %q\n", apiKey)
	fmt.Fprintf(&sb, "api_secret: %q\n", apiSecret)
	return sb.String()
}

// buildOAuthSiteYAML returns the YAML block for an OAuth-authenticated site.
func buildOAuthSiteYAML(siteURL, clientID, clientSecret string, tokens *client.OAuthTokens) string {
	expiry := int64(0)
	if tokens.ExpiresIn > 0 {
		expiry = time.Now().Unix() + int64(tokens.ExpiresIn)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "url: %q\n", siteURL)
	fmt.Fprintf(&sb, "oauth_client_id: %q\n", clientID)
	if clientSecret != "" {
		fmt.Fprintf(&sb, "oauth_client_secret: %q\n", clientSecret)
	}
	fmt.Fprintf(&sb, "access_token: %q\n", tokens.AccessToken)
	fmt.Fprintf(&sb, "refresh_token: %q\n", tokens.RefreshToken)
	fmt.Fprintf(&sb, "token_expiry: %d\n", expiry)
	return sb.String()
}

// upsertSiteInConfig adds or replaces a site entry in the config file.
// siteYAML is the YAML content for the site's fields (without the site name key).
// Leading comments and other top-level settings are preserved.
func upsertSiteInConfig(cfgPath, siteName, siteYAML string) error {
	// Parse the new site node from the YAML block.
	var siteDoc yaml.Node
	if err := yaml.Unmarshal([]byte(siteYAML), &siteDoc); err != nil {
		return fmt.Errorf("building site node: %w", err)
	}
	if siteDoc.Kind != yaml.DocumentNode || len(siteDoc.Content) == 0 {
		return fmt.Errorf("invalid site YAML")
	}
	newSiteNode := siteDoc.Content[0]

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("parsing config: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return fmt.Errorf("unexpected config format")
	}
	mapping := root.Content[0]

	// Find or create the sites mapping node.
	sitesIdx := -1
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "sites" {
			sitesIdx = i + 1
			break
		}
	}
	if sitesIdx < 0 {
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "sites", Tag: "!!str"},
			&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"},
		)
		sitesIdx = len(mapping.Content) - 1
	}
	sitesMap := mapping.Content[sitesIdx]

	// Replace existing entry or append a new one.
	for j := 0; j+1 < len(sitesMap.Content); j += 2 {
		if sitesMap.Content[j].Value == siteName {
			sitesMap.Content[j+1] = newSiteNode
			out, err := yaml.Marshal(mapping)
			if err != nil {
				return fmt.Errorf("serialising config: %w", err)
			}
			return os.WriteFile(cfgPath, []byte(preserveHeader(raw, out)), 0o600)
		}
	}
	sitesMap.Content = append(sitesMap.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: siteName, Tag: "!!str"},
		newSiteNode,
	)

	out, err := yaml.Marshal(mapping)
	if err != nil {
		return fmt.Errorf("serialising config: %w", err)
	}
	return os.WriteFile(cfgPath, []byte(preserveHeader(raw, out)), 0o600)
}

// removeSiteFromConfig removes a site entry from the config file.
func removeSiteFromConfig(cfgPath, siteName string) error {
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("parsing config: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return fmt.Errorf("unexpected config format")
	}
	mapping := root.Content[0]

	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != "sites" {
			continue
		}
		sitesMap := mapping.Content[i+1]
		for j := 0; j+1 < len(sitesMap.Content); j += 2 {
			if sitesMap.Content[j].Value != siteName {
				continue
			}
			sitesMap.Content = append(sitesMap.Content[:j], sitesMap.Content[j+2:]...)
			out, err := yaml.Marshal(mapping)
			if err != nil {
				return fmt.Errorf("serialising config: %w", err)
			}
			return os.WriteFile(cfgPath, []byte(preserveHeader(raw, out)), 0o600)
		}
		return fmt.Errorf("site %q not found in config", siteName)
	}
	return fmt.Errorf("sites section not found in config")
}

// setDefaultSite updates the default_site field in the config file.
func setDefaultSite(cfgPath, siteName string) error {
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("reading config: %w", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("parsing config: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return fmt.Errorf("unexpected config format")
	}
	updateYAMLValue(&root, "default_site", siteName)
	out, err := yaml.Marshal(root.Content[0])
	if err != nil {
		return fmt.Errorf("serialising config: %w", err)
	}
	return os.WriteFile(cfgPath, []byte(preserveHeader(raw, out)), 0o600)
}

// ─── Background token refresh ────────────────────────────────────────────────

// tryRefreshOAuthToken silently refreshes an expired OAuth access token before
// any command runs. It loads the current site config, checks expiry, refreshes
// via the Frappe token endpoint, and writes the new tokens back to disk.
// All errors are swallowed — commands will handle 401s themselves if refresh fails.
func tryRefreshOAuthToken() {
	cfg, err := config.Load(siteName, configPath)
	if err != nil {
		return
	}
	if !cfg.IsOAuth() || !cfg.IsTokenExpired() || cfg.RefreshToken == "" {
		return
	}

	tokens, err := client.RefreshOAuthToken(cfg.URL, cfg.OAuthClientID, cfg.OAuthClientSecret, cfg.RefreshToken)
	if err != nil {
		return
	}

	cfgPath := configPath
	if cfgPath == "" {
		cfgPath, err = config.DefaultConfigPath()
		if err != nil {
			return
		}
	}

	_ = saveOAuthTokens(cfgPath, cfg.Name, tokens)
}
