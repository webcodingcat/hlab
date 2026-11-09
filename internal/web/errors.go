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
	// bare sends the status alone, without a body.
	bare bool
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

// NotFound returns Hetzner's 404 not_found for a resource that is not there
// ("SSH key not found"), with the details {} that most resources send
// (measured 2026-11-07).
func NotFound(what string) *Error {
	return &Error{Status: http.StatusNotFound, Code: "not_found", Message: what + " not found", Details: map[string]any{}}
}

// NotFoundNull returns the 404 not_found of the resources whose details are
// null; their messages differ ("location with ID 9 not found", "load
// balancer type not found"), so the caller gives the whole message (measured
// 2026-11-07).
func NotFoundNull(message string) *Error {
	return &Error{Status: http.StatusNotFound, Code: "not_found", Message: message}
}

// InvalidValue returns Hetzner's 422 for a query parameter whose value the
// endpoint does not know ("invalid type value"), details {} (measured
// 2026-11-07 on GET /images).
func InvalidValue(parameter string) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "invalid_input", Message: "invalid " + parameter + " value", Details: map[string]any{}}
}

// ServiceError returns Hetzner's 422 service_error, which it answers where
// its own code fails on a request it should refuse (GET /server_types with
// an unknown sort value: "internal error", measured 2026-11-07).
func ServiceError(message string) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "service_error", Message: message, Details: map[string]any{}}
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

// JSONError returns Hetzner's answer to a body that is not valid JSON: 422
// json_error with the decoder's message ("unexpected EOF"), details {}
// (measured 2026-11-07; Hetzner's API decodes with Go too).
func JSONError(message string) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "json_error", Message: message, Details: map[string]any{}}
}

// JSONDocumentRequired returns the 400 json_error without details that
// some actions answer for a field of the wrong type, where others answer
// ParseFailed (measured 2026-11-08 on a Floating IP's assign with the server
// as a string).
func JSONDocumentRequired() *Error {
	return &Error{Status: http.StatusBadRequest, Code: "json_error", Message: "A valid JSON document is required.", noDetails: true}
}

// ParseFailed returns Hetzner's answer to valid JSON that does not fit the
// request: no object, or a field of the wrong type. It names no field
// (measured 2026-11-07).
func ParseFailed() *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "invalid_input", Message: "failed to parse json", Details: map[string]any{}}
}

// unauthorized returns a 401 unauthorized error; Hetzner's has no details.
func unauthorized(message string) *Error {
	return &Error{Status: http.StatusUnauthorized, Code: "unauthorized", Message: message, noDetails: true}
}

// TokenInvalid returns Hetzner's 401 for a token it does not know; handlers
// answer it when the caller's project went away after its token was checked.
func TokenInvalid() *Error {
	return unauthorized("the token you have provided is invalid")
}

// Forbidden returns Hetzner's 403 forbidden: a read token asks for a change.
func Forbidden() *Error {
	return &Error{Status: http.StatusForbidden, Code: "forbidden", Message: "permission denied", Details: map[string]any{}}
}

// ActionMethodNotAllowed returns the 405 that Hetzner answers for another
// method than GET on a single action of networks, firewalls, Floating IPs
// and Primary IPs, which includes a command they do not have: no body,
// Allow "GET, HEAD" (measured 2026-11-09). The handler sets Allow.
func ActionMethodNotAllowed() *Error {
	return &Error{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Message: "method not allowed", bare: true}
}

// MethodNotAllowed returns Hetzner's 405 method_not_allowed.
func MethodNotAllowed() *Error {
	return &Error{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Message: "method not allowed"}
}

// Locked returns a 423 locked error: an action already works on the resource.
func Locked(message string) *Error {
	return &Error{Status: http.StatusLocked, Code: "locked", Message: message}
}

// Refusal returns one of Hetzner's errors that refuse a request for a
// reason of its own, such as 423 protected or 412 resource_unavailable,
// with the details {} that the server endpoints send. The caller names
// Hetzner's status, code and message as measured.
func Refusal(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message, Details: map[string]any{}}
}

// RefusalNull is Refusal with the details null, as the Primary IP
// endpoints send them (measured 2026-11-08).
func RefusalNull(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// LimitExceeded returns Hetzner's 403 resource_limit_exceeded whose
// details name the limit, as the firewalls' limits send it ("firewall
// limit exceeded", limit "firewalls_per_server"; measured 2026-11-08).
func LimitExceeded(message, limit string) *Error {
	return &Error{
		Status:  http.StatusForbidden,
		Code:    "resource_limit_exceeded",
		Message: message,
		Details: map[string][]map[string]string{"limits": {{"name": limit}}},
	}
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
