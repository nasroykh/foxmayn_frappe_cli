package services

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/nasroykh/foxmayn_frappe_cli/internal/client"
	"github.com/nasroykh/foxmayn_frappe_cli/internal/text"
)

// Error codes the frontend acts on.
const (
	CodeInvalid        = "invalid"         // the input is wrong; Field names it
	CodeExists         = "exists"          // a site with this name exists
	CodeNotFound       = "not_found"       // no such site
	CodeAuth           = "auth"            // the site refused the credentials
	CodeNetwork        = "network"         // the site could not be reached
	CodeCancelled      = "cancelled"       // the user cancelled
	CodeNoRegistration = "no_registration" // OAuth: no client could be registered
	CodeFFCMissing     = "ffc_missing"     // the ffc binary is not installed
	CodeUnavailable    = "unavailable"     // not possible here (yet)
	CodeFailed         = "failed"          // anything else
)

// Error is an error the UI shows and acts on. Wails marshals it as the
// call's "cause", so the frontend reads code, message and the rest.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Detail is the underlying error text, for a "details" disclosure.
	Detail string `json:"detail,omitempty"`
	// Field is the input the error is about ("name", "url", ...).
	Field string `json:"field,omitempty"`
	// Unsupported (no_registration): the site does not offer registration
	// at all, as opposed to a failed attempt.
	Unsupported bool `json:"unsupported,omitempty"`
	// RedirectURI (no_registration): the callback to register on a manual
	// OAuth Client.
	RedirectURI string `json:"redirectURI,omitempty"`
	// Key names the message in the page's catalogs (errors.<Key>), so the
	// page shows it in its language; Args fill its {{placeholders}}. Message
	// is the English text of the same key (errorKeys), so a page without the
	// key, logs and Error() still read it.
	Key  string            `json:"key,omitempty"`
	Args map[string]string `json:"args,omitempty"`
}

func (e *Error) Error() string {
	if e.Detail != "" && e.Detail != e.Message {
		return e.Message + ": " + e.Detail
	}
	return e.Message
}

func newError(code, msg string, cause error) *Error {
	e := &Error{Code: code, Message: msg}
	if cause != nil {
		e.Detail = text.Sanitize(cause.Error())
	}
	return e
}

// keyed is newError with a message from errorKeys; args are name, value
// pairs for its {{name}} placeholders.
func keyed(code, key string, cause error, args ...string) *Error {
	e := newError(code, errorText(key, args...), cause)
	e.Key = key
	if len(args) > 1 {
		e.Args = make(map[string]string, len(args)/2)
		for i := 0; i+1 < len(args); i += 2 {
			e.Args[args[i]] = args[i+1]
		}
	}
	return e
}

// keyedInvalid is invalid with a message from errorKeys.
func keyedInvalid(field, key string, args ...string) *Error {
	e := keyed(CodeInvalid, key, nil, args...)
	e.Field = field
	return e
}

// errorText fills the English text of key with args (name, value pairs).
// An unknown key gives the key itself (TestErrorKeysUsed keeps that from
// shipping).
func errorText(key string, args ...string) string {
	s, ok := errorKeys[key]
	if !ok {
		return key
	}
	for i := 0; i+1 < len(args); i += 2 {
		s = strings.ReplaceAll(s, "{{"+args[i]+"}}", args[i+1])
	}
	return s
}

func invalid(field, msg string) *Error {
	return &Error{Code: CodeInvalid, Message: msg, Field: field}
}

// Steps of siteError: what was attempted. Each has the keys
// site.<step>Cancelled and site.<step>Failed.
const (
	stepEngine     = "engine"     // starting the assistant engine
	stepOAuthSetup = "oauthSetup" // setting up the sign-in
	stepSignIn     = "signIn"     // signing in
	stepAPIKey     = "apiKey"     // checking the API key
	stepCheck      = "check"      // checking the connection
	stepRenew      = "renew"      // renewing the sign-in
	stepNewURL     = "newURL"     // checking the new address
)

// siteError turns an error from a request to a site into an *Error with a
// message people understand. step says what was attempted (stepCheck, ...).
func siteError(step string, err error) error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, context.Canceled) {
		return keyed(CodeCancelled, "site."+step+"Cancelled", nil)
	}
	var ae *client.APIError
	var authErr *client.AuthError
	var te *client.TransportError
	switch {
	case errors.As(err, &authErr):
		return keyed(CodeAuth, "site.refused", err)
	case errors.As(err, &ae) && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden):
		return keyed(CodeAuth, "site.refused", err)
	case errors.As(err, &te), errors.Is(err, context.DeadlineExceeded):
		return keyed(CodeNetwork, "site.unreachable", err)
	case errors.As(err, &ae):
		return keyed(CodeFailed, "site.status", err, "status", strconv.Itoa(ae.Status))
	}
	return keyed(CodeFailed, "site."+step+"Failed", err)
}
