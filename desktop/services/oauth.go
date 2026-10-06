package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/sitesetup"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
)

// signInState is the browser sign-in in progress (at most one).
type signInState struct {
	cancel  context.CancelFunc
	authURL string
}

func (s *SitesService) progress(step, msg string) {
	s.host.Emit(EventSignInProgress, SignInProgress{Step: step, Message: msg})
}

// SignInWithBrowser adds a site through the OAuth browser sign-in: it binds
// the local callback, finds or registers the OAuth client, opens the site's
// sign-in page in the browser, waits for the user (at most 5 minutes), and
// saves the site with its tokens. Progress goes out as "signin:progress"
// events. CancelSignIn, or cancelling the call, stops it. Each call is a new
// flow, so a retry after a failure starts clean.
//
// When the site cannot register a client (Frappe v15, or registration
// turned off), the error has code "no_registration" and the redirect URI to
// register on a client created by hand; the UI asks for its ID and calls
// again with ClientID set.
func (s *SitesService) SignInWithBrowser(ctx context.Context, req BrowserSignInRequest) (AddedSite, error) {
	name, siteURL, exists, err := s.prepare(req.Name, req.URL, req.Replace)
	if err != nil {
		return AddedSite{}, err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	st := &signInState{cancel: cancel}
	s.mu.Lock()
	if s.signIn != nil {
		s.signIn.cancel() // a new attempt replaces the old one
	}
	s.signIn = st
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.signIn == st {
			s.signIn = nil
		}
		s.mu.Unlock()
	}()

	s.progress("starting", "Getting ready…")
	flow, err := s.startFlow()
	if err != nil {
		return AddedSite{}, newError(CodeFailed, "The sign-in could not start on this computer.", err)
	}
	defer flow.Close()
	if s.flowTimeout > 0 {
		flow.Timeout = s.flowTimeout
	}

	manual := sitesetup.OAuthApp{ID: strings.TrimSpace(req.ClientID), Secret: strings.TrimSpace(req.ClientSecret)}
	if manual.ID == "" {
		s.progress("registering", "Setting up a secure connection with your site…")
	}
	app, err := s.resolveApp(ctx, siteURL, flow.RedirectURI(), manual)
	var nr *sitesetup.NoRegistrationError
	switch {
	case ctx.Err() != nil:
		return AddedSite{}, newError(CodeCancelled, "The sign-in was cancelled.", nil)
	case errors.As(err, &nr):
		e := newError(CodeNoRegistration, "This site can't set up browser sign-in for the app by itself.", err)
		e.Unsupported = nr.Unsupported
		e.RedirectURI = flow.RedirectURI()
		if !nr.Unsupported {
			e.Message = "Setting up browser sign-in with this site failed. You can try again, or use an OAuth Client created by hand."
		}
		return AddedSite{}, e
	case err != nil:
		return AddedSite{}, siteError("Setting up the sign-in", err)
	}

	site, user, err := flow.Login(ctx, siteURL, app, sitesetup.LoginHooks{
		OpenBrowser: func(authURL string) error {
			s.mu.Lock()
			st.authURL = authURL
			s.mu.Unlock()
			ev := SignInProgress{Step: "browser", Message: "Continue in your browser…", AuthURL: authURL}
			if err := s.host.OpenURL(authURL); err != nil {
				ev.BrowserError = text.Sanitize(err.Error())
			}
			s.host.Emit(EventSignInProgress, ev)
			return nil
		},
		Step: func(title string, run func()) error {
			s.progress("finishing", stepMessage(title))
			run()
			return nil
		},
	})
	switch {
	case ctx.Err() != nil:
		return AddedSite{}, newError(CodeCancelled, "The sign-in was cancelled.", nil)
	case err != nil && strings.Contains(err.Error(), "timed out"):
		return AddedSite{}, newError(CodeFailed, fmt.Sprintf("The sign-in took too long. Try again, and finish it in your browser within %d minutes.", int(flow.Timeout.Minutes())), err)
	case err != nil:
		return AddedSite{}, siteError("Signing in", err)
	}

	s.progress("saving", "Saving the site…")
	added := AddedSite{Name: name, URL: siteURL, Auth: AuthOAuth, User: user, Replaced: exists, Registered: app.Registered}
	if err := s.save(name, site, &added); err != nil {
		return AddedSite{}, err
	}
	s.progress("done", "Signed in")
	return added, nil
}

func stepMessage(title string) string {
	switch {
	case strings.Contains(title, "Exchanging"):
		return "Completing the sign-in…"
	case strings.Contains(title, "user"):
		return "Checking who signed in…"
	}
	return strings.TrimSuffix(title, "...") + "…"
}

// CancelSignIn stops the browser sign-in in progress, if any.
func (s *SitesService) CancelSignIn() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.signIn != nil {
		s.signIn.cancel()
	}
}

// ReopenSignInPage opens the sign-in page of the sign-in in progress again,
// for when the browser tab was closed or did not open.
func (s *SitesService) ReopenSignInPage() error {
	s.mu.Lock()
	u := ""
	if s.signIn != nil {
		u = s.signIn.authURL
	}
	s.mu.Unlock()
	if u == "" {
		return &Error{Code: CodeUnavailable, Message: "No sign-in is waiting for the browser."}
	}
	if err := s.host.OpenURL(u); err != nil {
		return newError(CodeFailed, "The browser could not be opened.", err)
	}
	return nil
}
