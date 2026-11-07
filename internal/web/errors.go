package web

import (
	"encoding/json"
	"net/http"
)

// Error is an error answer of the API: its status, Hetzner's error code, the
// message and the details, whose shape depends on the code. The messages and
// details follow what Hetzner's API answers (compat/requirements.md).
type Error struct {
	Status  int
	Code    string
	Message string
	// Details is sent as it is: nil as null, an empty map as {}; Hetzner
	// answers either, depending on the error.
	Details any
	// noDetails leaves the details out, as Hetzner's 401 answers do.
	noDetails bool
	// plain sends the message as plain text instead of the JSON format.
	plain bool
}

func (e *Error) Error() string { return e.Message }

// errorBody is an error as the API sends it.
type errorBody struct {
	Error struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Details json.RawMessage `json:"details,omitempty"`
	} `json:"error"`
}

// body returns the error as the API sends it.
func (e *Error) body() errorBody {
	var body errorBody
	body.Error.Code, body.Error.Message = e.Code, e.Message
	if !e.noDetails {
		body.Error.Details, _ = json.Marshal(e.Details) // details are plain data: they encode
	}
	return body
}

// Field is one field of an invalid_input error, with what is wrong with it.
type Field struct {
	Name     string   `json:"name"`
	Messages []string `json:"messages"`
}

// InvalidInput returns a 422 invalid_input error. With fields, the details
// list them; without, they are null, as Hetzner's "invalid integer value".
func InvalidInput(message string, fields ...Field) *Error {
	err := &Error{Status: http.StatusUnprocessableEntity, Code: "invalid_input", Message: message}
	if len(fields) > 0 {
		err.Details = map[string][]Field{"fields": fields}
	}
	return err
}

// NotFound returns a 404 not_found error for a resource that is not there
// ("server").
func NotFound(what string) *Error {
	return &Error{Status: http.StatusNotFound, Code: "not_found", Message: what + " not found"}
}

// PlainNotFound returns the 404 that Hetzner's actions endpoints answer for
// an action that is not there: the plain text "Not Found", not JSON
// (measured 2026-11-07).
func PlainNotFound() *Error {
	return &Error{Status: http.StatusNotFound, Code: "not_found", Message: "Not Found", plain: true}
}

// InvalidInteger returns Hetzner's answer to a query value that should be an
// integer and is not: invalid_input, but with the status 406 (measured
// 2026-11-07).
func InvalidInteger() *Error {
	return &Error{Status: http.StatusNotAcceptable, Code: "invalid_input", Message: "invalid integer value"}
}

// RouteNotFound returns Hetzner's 404 for a path that no route takes.
func RouteNotFound() *Error {
	return &Error{Status: http.StatusNotFound, Code: "not_found", Message: "api route not found"}
}

// Deprecated returns Hetzner's 410 for functionality it removed, with the
// changelog entry that announced it.
func Deprecated(announcement string) *Error {
	return &Error{
		Status:  http.StatusGone,
		Code:    "deprecated_api_endpoint",
		Message: "API functionality was removed",
		Details: map[string]string{"announcement": announcement},
	}
}

// JSONError returns a 400 json_error: the request body is not valid JSON.
func JSONError() *Error {
	return &Error{Status: http.StatusBadRequest, Code: "json_error", Message: "request body is not valid JSON"}
}

// unauthorized returns a 401 unauthorized error; Hetzner's has no details.
func unauthorized(message string) *Error {
	return &Error{Status: http.StatusUnauthorized, Code: "unauthorized", Message: message, noDetails: true}
}

// Forbidden returns Hetzner's 403 forbidden: a read token asks for a change.
func Forbidden() *Error {
	return &Error{Status: http.StatusForbidden, Code: "forbidden", Message: "permission denied", Details: map[string]any{}}
}

// MethodNotAllowed returns Hetzner's 405 method_not_allowed.
func MethodNotAllowed() *Error {
	return &Error{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Message: "method not allowed"}
}

// Locked returns a 423 locked error: an action already works on the resource.
func Locked(message string) *Error {
	return &Error{Status: http.StatusLocked, Code: "locked", Message: message}
}

// RateLimitExceeded returns a 429 rate_limit_exceeded error.
func RateLimitExceeded() *Error {
	return &Error{Status: http.StatusTooManyRequests, Code: "rate_limit_exceeded", Message: "limit of requests per hour reached"}
}

// ServerError returns a 500 server_error: the lab itself failed.
func ServerError(message string) *Error {
	return &Error{Status: http.StatusInternalServerError, Code: "server_error", Message: message}
}

// faultCodes are Hetzner's error codes of the statuses that an injected fault
// may answer with; any other status gets server_error.
var faultCodes = map[int]string{
	http.StatusTooManyRequests:     "rate_limit_exceeded",
	http.StatusInternalServerError: "server_error",
	http.StatusBadGateway:          "bad_gateway",
	http.StatusServiceUnavailable:  "unavailable",
	http.StatusGatewayTimeout:      "timeout",
}

// faultError returns the error an injected API fault answers with.
func faultError(status int) *Error {
	code, found := faultCodes[status]
	if !found {
		code = "server_error"
	}
	return &Error{Status: status, Code: code, Message: "injected fault"}
}
