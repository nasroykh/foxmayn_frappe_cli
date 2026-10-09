package llm

import (
	"fmt"
	"testing"
)

func TestErrorKinds(t *testing.T) {
	wrap := func(s int) error { return fmt.Errorf("stream: %w", &APIError{Status: s, Message: "m"}) }
	if !IsAuth(wrap(401)) || !IsAuth(wrap(403)) || IsAuth(wrap(429)) || IsAuth(fmt.Errorf("x")) {
		t.Fatal("IsAuth")
	}
	if !IsRateLimit(wrap(429)) || IsRateLimit(wrap(500)) || IsRateLimit(nil) {
		t.Fatal("IsRateLimit")
	}
	for status, want := range map[int]bool{400: false, 401: false, 429: true, 500: true, 503: true, 529: true} {
		if IsRetryable(wrap(status)) != want {
			t.Fatalf("IsRetryable(%d) != %v", status, want)
		}
	}
	if IsRetryable(&APIError{Message: "offline"}) || IsRetryable(nil) {
		t.Fatal("IsRetryable on status 0 or nil")
	}
	if got := (&APIError{Status: 429, Message: "slow"}).Error(); got != "provider error (HTTP 429): slow" {
		t.Fatalf("Error() = %q", got)
	}
	if got := (&APIError{Message: "offline"}).Error(); got != "offline" {
		t.Fatalf("Error() = %q", got)
	}
}
