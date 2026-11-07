package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"
)

// Error is an error answer of the API: its status, Hetzner's error code, the
// message and the details, whose shape depends on the code.
type Error struct {
	Status  int
	Code    string
	Message string
	Details any // nil is sent as null, as in the spec's example
}

func (e *Error) Error() string { return e.Message }

// body returns the error as the API sends it.
func (e *Error) body() schema.ErrorResponse {
	details, _ := json.Marshal(e.Details) // details are plain data: they encode
	return schema.ErrorResponse{Error: schema.Error{Code: e.Code, Message: e.Message, DetailsRaw: details}}
}

// Field is one field of an invalid_input error, with what is wrong with it.
type Field struct {
	Name     string   `json:"name"`
	Messages []string `json:"messages"`
}

// InvalidInput returns a 422 invalid_input error for fields; the message
// names the first field and its first message, as Hetzner's does.
func InvalidInput(fields ...Field) *Error {
	first := fields[0]
	return &Error{
		Status:  http.StatusUnprocessableEntity,
		Code:    "invalid_input",
		Message: fmt.Sprintf("invalid input in field '%s': %s", first.Name, first.Messages[0]),
		Details: map[string][]Field{"fields": fields},
	}
}

// InvalidField returns a 422 invalid_input error for one field.
func InvalidField(name, message string) *Error {
	return InvalidInput(Field{Name: name, Messages: []string{message}})
}

// NotFound returns a 404 not_found error for what is not there ("action").
func NotFound(what string) *Error {
	return &Error{Status: http.StatusNotFound, Code: "not_found", Message: what + " not found"}
}

// JSONError returns a 400 json_error: the request body is not valid JSON.
func JSONError() *Error {
	return &Error{Status: http.StatusBadRequest, Code: "json_error", Message: "request body is not valid JSON"}
}

// Unauthorized returns a 401 unauthorized error with Hetzner's message.
func Unauthorized() *Error {
	return &Error{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "unable to authenticate"}
}

// Forbidden returns a 403 forbidden error: a read token asks for a change.
func Forbidden() *Error {
	return &Error{Status: http.StatusForbidden, Code: "forbidden", Message: "insufficient permissions for this request"}
}

// MethodNotAllowed returns a 405 method_not_allowed error.
func MethodNotAllowed() *Error {
	return &Error{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Message: "the request method is not allowed"}
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
