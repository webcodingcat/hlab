package web_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/web"
)

// postJSON returns a POST request with a body, as a handler gets it.
func postJSON(body string) *web.Request {
	return &web.Request{Request: httptest.NewRequest("POST", "/v1/ssh_keys", strings.NewReader(body)), Header: http.Header{}}
}

func TestDecodeJSON(t *testing.T) {
	var body struct {
		Name string `json:"name"`
	}
	if err := web.DecodeJSON(postJSON(`{"name":"admin"}`), &body); err != nil || body.Name != "admin" {
		t.Errorf("decoded %+v, %v", body, err)
	}
	if err := web.DecodeJSON(postJSON(""), &body); err != nil {
		t.Errorf("an empty body: %v", err)
	}
}

// Hetzner's API (measured 2026-11-07): JSON that does not fit the request is
// "failed to parse json" without fields; JSON that is broken is a json_error
// with the decoder's message. Both are 422 with the details {}.
func TestDecodeJSONErrors(t *testing.T) {
	for body, want := range map[string]struct{ code, message string }{
		`{"name":5}`:              {"invalid_input", "failed to parse json"},
		`{"labels":["a"]}`:        {"invalid_input", "failed to parse json"},
		`{"labels":{"env":true}}`: {"invalid_input", "failed to parse json"},
		`[1]`:                     {"invalid_input", "failed to parse json"},
		`{"name":`:                {"json_error", "unexpected EOF"},
	} {
		var typed struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		}
		err := web.DecodeJSON(postJSON(body), &typed)
		var apiErr *web.Error
		if !errors.As(err, &apiErr) || apiErr.Status != 422 || apiErr.Code != want.code || apiErr.Message != want.message || details(t, err) != "{}" {
			t.Errorf("%s: %+v, want %s %q", body, err, want.code, want.message)
		}
	}
	var body struct{}
	err := web.DecodeJSON(postJSON(strings.Repeat(" ", 1<<20+1)), &body)
	var apiErr *web.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "json_error" {
		t.Errorf("a body over 1 MiB: %v, want json_error", err)
	}
}
