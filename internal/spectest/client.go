package spectest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// Client sends requests to an API in the test's process and checks each
// answer against a spec; an answer that does not match fails the test.
type Client struct {
	t       testing.TB
	handler http.Handler
	spec    *Spec
	// Token is sent as the bearer token; empty sends none.
	Token string
}

// New returns a client of handler whose answers are checked against the
// spec of that name (Cloud, Hetzner).
func New(t testing.TB, handler http.Handler, specName string) *Client {
	t.Helper()
	spec, err := Load(specName)
	if err != nil {
		t.Fatal(err)
	}
	return &Client{t: t, handler: handler, spec: spec}
}

// Response is an answer of the API.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Do sends a request; body, if not nil, goes as JSON. The path may carry a
// query ("/v1/actions?id=1").
func (c *Client) Do(method, path string, body any) *Response {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	request := httptest.NewRequest(method, path, reader)
	if c.Token != "" {
		request.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, request)

	response := &Response{Status: recorder.Code, Header: recorder.Header(), Body: recorder.Body.Bytes()}
	target, err := url.Parse(path)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := c.spec.Check(method, target.Path, response.Status, response.Body); err != nil {
		c.t.Errorf("the answer does not match the spec: %v", err)
	}
	return response
}

// Get sends a GET request.
func (c *Client) Get(path string) *Response {
	c.t.Helper()
	return c.Do(http.MethodGet, path, nil)
}

// Decode decodes the answer's JSON body into value.
func (r *Response) Decode(t testing.TB, value any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, value); err != nil {
		t.Fatalf("decoding %s: %v", r.Body, err)
	}
}
