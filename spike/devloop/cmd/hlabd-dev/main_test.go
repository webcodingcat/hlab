package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestAuthorizedRejectsWrongToken(t *testing.T) {
	handler := authorized("secret", func(w http.ResponseWriter, _ *http.Request) {})
	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest("POST", "/_lab/shutdown", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d", recorder.Code)
	}
}

// TestFailWhenAsked lets the loop check that a failing test's exit code reaches WSL.
func TestFailWhenAsked(t *testing.T) {
	if os.Getenv("SPIKE_FAIL") == "1" {
		t.Fatal("failing on purpose (SPIKE_FAIL=1)")
	}
}
