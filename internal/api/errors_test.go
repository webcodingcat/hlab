package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/resource"
)

// errorModule answers GET /ssh_keys with an error chosen by the query:
// code=<c> writes api.Error with code c; field=<f> writes
// api.InvalidInput for field f; fail=<f> passes an *api.FieldError for
// field f to api.Fail.
type errorModule struct{}

func (errorModule) Type() string { return "ssh_keys" }

func (errorModule) Routes(r resource.Registrar) {
	r.Handle("GET /ssh_keys", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		switch {
		case q.Has("field"):
			api.InvalidInput(w, q.Get("field"), "invalid "+q.Get("field"))
		case q.Has("fail"):
			api.Fail(w, &api.FieldError{Field: q.Get("fail"), Message: "invalid " + q.Get("fail")})
		default:
			api.Error(w, q.Get("code"), "message for "+q.Get("code"), nil)
		}
	}))
}

// errorBody is Hetzner's error object with details kept raw.
type errorBody struct {
	Error *struct {
		Code    *string         `json:"code"`
		Message *string         `json:"message"`
		Details json.RawMessage `json:"details"`
	} `json:"error"`
}

// testErrors is TestAPI/API-09.
func testErrors(t *testing.T) {
	env := apitest.New(t, errorModule{})

	// The error table of the frozen OpenAPI description (info.description).
	table := []struct {
		code   string
		status int
	}{
		{"json_error", 400},
		{"unauthorized", 401},
		{"forbidden", 403},
		{"maintenance", 403},
		{"resource_limit_exceeded", 403},
		{"not_found", 404},
		{"method_not_allowed", 405},
		{"uniqueness_error", 409},
		{"conflict", 409},
		{"deprecated_api_endpoint", 410},
		{"resource_unavailable", 412},
		{"invalid_input", 422},
		{"service_error", 422},
		{"unsupported_error", 422},
		{"locked", 423},
		{"protected", 423},
		{"rate_limit_exceeded", 429},
		{"server_error", 500},
		{"bad_gateway", 502},
		{"unavailable", 503},
		{"timeout", 504},
	}
	for _, c := range table {
		t.Run(c.code, func(t *testing.T) {
			resp := env.For(t).Do(http.MethodGet, "/ssh_keys?code="+url.QueryEscape(c.code), nil)
			if resp.StatusCode != c.status {
				t.Errorf("status %d, want %d", resp.StatusCode, c.status)
			}
			var body errorBody
			apitest.Decode(t, resp, &body)
			checkErrorObject(t, body, c.code, "message for "+c.code)
			var details map[string]any
			if err := json.Unmarshal(body.Error.Details, &details); err != nil || details == nil {
				t.Errorf("details %s, want an object", body.Error.Details)
			}
		})
	}

	fieldCases := []struct {
		name  string
		query string
		field string
	}{
		{"InvalidInput", "field=name", "name"},
		{"Fail with FieldError", "fail=labels", "labels"},
	}
	for _, c := range fieldCases {
		t.Run(c.name, func(t *testing.T) {
			resp := env.For(t).Do(http.MethodGet, "/ssh_keys?"+c.query, nil)
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Errorf("status %d, want 422", resp.StatusCode)
			}
			var body errorBody
			apitest.Decode(t, resp, &body)
			checkErrorObject(t, body, "invalid_input", "")
			var details struct {
				Fields []struct {
					Name     string   `json:"name"`
					Messages []string `json:"messages"`
				} `json:"fields"`
			}
			if err := json.Unmarshal(body.Error.Details, &details); err != nil {
				t.Fatalf("details %s: %v", body.Error.Details, err)
			}
			if len(details.Fields) != 1 || details.Fields[0].Name != c.field || len(details.Fields[0].Messages) == 0 {
				t.Errorf("details.fields %+v, want one entry %q with messages", details.Fields, c.field)
			}
		})
	}
}

// checkErrorObject checks error.code and that error.message is present;
// message "" accepts any message.
func checkErrorObject(t *testing.T, body errorBody, code, message string) {
	t.Helper()
	if body.Error == nil {
		t.Fatal("no error object in the body")
	}
	if body.Error.Code == nil || *body.Error.Code != code {
		t.Errorf("error.code %v, want %q", body.Error.Code, code)
	}
	if body.Error.Message == nil || (message != "" && *body.Error.Message != message) {
		t.Errorf("error.message %v, want %q", body.Error.Message, message)
	}
	if len(body.Error.Details) == 0 {
		t.Error("error.details missing")
	}
}
