package client

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestTransportErrorTimeout checks that a timeout says what to do about it
// and keeps its cause.
func TestTransportErrorTimeout(t *testing.T) {
	err := error(&TransportError{Err: context.DeadlineExceeded})
	if !strings.Contains(err.Error(), "raise --timeout") || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("timeout: %v", err)
	}
	if err := (&TransportError{Err: errors.New("connection refused")}).Error(); strings.Contains(err, "--timeout") {
		t.Errorf("not a timeout: %s", err)
	}
}
