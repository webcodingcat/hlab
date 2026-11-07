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
	for body, want := range map[string]string{
		`{"name":5}`:              `{"fields":[{"name":"name","messages":["must be of type string"]}]}`,
		`{"labels":["a"]}`:        `{"fields":[{"name":"labels","messages":["must be of type object"]}]}`,
		`{"labels":{"env":true}}`: `{"fields":[{"name":"labels.env","messages":["must be of type string"]}]}`,
	} {
		var typed struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		}
		err := web.DecodeJSON(postJSON(body), &typed)
		var apiErr *web.Error
		if !errors.As(err, &apiErr) || apiErr.Status != 422 || details(t, err) != want {
			t.Errorf("%s: %v, want invalid_input with %s", body, err, want)
		}
	}
	for _, broken := range []string{`{"name":`, `[1]`, strings.Repeat(" ", 1<<20+1)} {
		err := web.DecodeJSON(postJSON(broken), &body)
		var apiErr *web.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "json_error" || apiErr.Status != 400 {
			t.Errorf("a body of %d bytes: %v, want json_error", len(broken), err)
		}
	}
}
