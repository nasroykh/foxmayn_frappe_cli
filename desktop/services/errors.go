package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"

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

func invalid(field, msg string) *Error {
	return &Error{Code: CodeInvalid, Message: msg, Field: field}
}

// siteError turns an error from a request to a site into an *Error with a
// message people understand. what says what was attempted ("Checking the
// connection").
func siteError(what string, err error) error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, context.Canceled) {
		return newError(CodeCancelled, what+" was cancelled.", nil)
	}
	var ae *client.APIError
	var authErr *client.AuthError
	var te *client.TransportError
	switch {
	case errors.As(err, &authErr):
		return newError(CodeAuth, "The site did not accept these sign-in details.", err)
	case errors.As(err, &ae) && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden):
		return newError(CodeAuth, "The site did not accept these sign-in details.", err)
	case errors.As(err, &te), errors.Is(err, context.DeadlineExceeded):
		return newError(CodeNetwork, "The site could not be reached. Check the address and your internet connection.", err)
	case errors.As(err, &ae):
		return newError(CodeFailed, fmt.Sprintf("The site answered with an error (%d).", ae.Status), err)
	}
	return newError(CodeFailed, what+" failed.", err)
}
