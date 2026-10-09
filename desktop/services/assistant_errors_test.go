package services

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nasroykh/foxmayn_frappe_cli/desktop/llm"
)

// A turn that ended without a usable answer is shown with the reason.
func TestServiceErrorExplainsNoAnswer(t *testing.T) {
	err := fmt.Errorf("turn: %w", &llm.APIError{Status: 502, Category: "malformed_function_call", Message: "the model produced a tool call that could not be read"})
	se := toServiceError(err)
	if se.Code != CodeFailed || !strings.Contains(se.Message, "could not be read") {
		t.Fatalf("got %#v", se)
	}
	plain := toServiceError(&llm.APIError{Status: 500, Message: "boom"})
	if plain.Message != "The AI provider returned an error." {
		t.Fatalf("plain %#v", plain)
	}
}

func TestRedactSecretsGoogleKey(t *testing.T) {
	key := "AIza" + strings.Repeat("Xy9_-", 7)
	got := redactSecrets("key " + key + " refused")
	if strings.Contains(got, key) || !strings.Contains(got, "[hidden]") {
		t.Fatalf("got %q", got)
	}
	if redactSecrets("AIzaShort") != "AIzaShort" {
		t.Fatal("short AIza text redacted")
	}
}
