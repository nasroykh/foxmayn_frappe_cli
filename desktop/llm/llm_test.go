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
	if got := (&APIError{Status: 429, Message: "slow"}).Error(); got != "provider error (HTTP 429): slow" {
		t.Fatalf("Error() = %q", got)
	}
	if got := (&APIError{Message: "offline"}).Error(); got != "offline" {
		t.Fatalf("Error() = %q", got)
	}
}
