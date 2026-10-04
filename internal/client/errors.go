package client

// The error types below let callers tell failures apart (the CLI maps them
// to exit codes) without parsing messages. Their Error() text is what users
// see; keep it stable.

// APIError is an error response (HTTP status >= 400) from the site.
type APIError struct {
	Status  int    // HTTP status code
	ExcType string // Frappe exception class, e.g. "DoesNotExistError"; "" when the body was not Frappe JSON
	Message string // user-facing message, including any hint
}

func (e *APIError) Error() string { return e.Message }

// TransportError is a request that got no HTTP response: connection refused,
// DNS or TLS failure, timeout or cancellation. Err keeps the cause, so
// errors.Is(err, context.Canceled) and context.DeadlineExceeded work.
type TransportError struct {
	Err error
}

func (e *TransportError) Error() string { return "HTTP request failed: " + e.Err.Error() }
func (e *TransportError) Unwrap() error { return e.Err }

// AuthError is a username/password login that did not produce a session:
// wrong credentials, two-factor authentication, or an unexpected reply.
type AuthError struct {
	Status  int // HTTP status of the login response; 0 when not applicable
	Message string
}

func (e *AuthError) Error() string { return e.Message }
