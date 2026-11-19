package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/store"
)

// statuses maps every error code to its HTTP status. It is the error table
// of the frozen OpenAPI description (info.description), plus
// token_readonly, which the API answers to writes with a read-only token,
// and the operation-specific codes of single endpoints.
var statuses = map[string]int{
	"json_error":              http.StatusBadRequest,
	"unauthorized":            http.StatusUnauthorized,
	"forbidden":               http.StatusForbidden,
	"maintenance":             http.StatusForbidden,
	"resource_limit_exceeded": http.StatusForbidden,
	"token_readonly":          http.StatusForbidden,
	"not_found":               http.StatusNotFound,
	"method_not_allowed":      http.StatusMethodNotAllowed,
	"uniqueness_error":        http.StatusConflict,
	"conflict":                http.StatusConflict,
	"deprecated_api_endpoint": http.StatusGone,
	"resource_unavailable":    http.StatusPreconditionFailed,
	"invalid_input":           http.StatusUnprocessableEntity,
	"service_error":           http.StatusUnprocessableEntity,
	"unsupported_error":       http.StatusUnprocessableEntity,
	"locked":                  http.StatusLocked,
	"protected":               http.StatusLocked,
	"rate_limit_exceeded":     http.StatusTooManyRequests,
	"server_error":            http.StatusInternalServerError,
	"bad_gateway":             http.StatusBadGateway,
	"unavailable":             http.StatusServiceUnavailable,
	"timeout":                 http.StatusGatewayTimeout,

	// Operation-specific codes of single endpoints.
	"placement_error":            http.StatusUnprocessableEntity,
	"server_not_stopped":         http.StatusUnprocessableEntity,
	"invalid_server_type":        http.StatusUnprocessableEntity,
	"already_in_placement_group": http.StatusUnprocessableEntity,
	"volume_already_attached":    http.StatusUnprocessableEntity,

	// Codes of networks, primary IPs and floating IPs.
	"must_be_unassigned":             http.StatusUnprocessableEntity,
	"primary_ip_assigned":            http.StatusUnprocessableEntity,
	"primary_ip_already_assigned":    http.StatusUnprocessableEntity,
	"primary_ip_datacenter_mismatch": http.StatusUnprocessableEntity,
	"primary_ip_version_mismatch":    http.StatusPreconditionFailed,
	"server_has_ipv4":                http.StatusUnprocessableEntity,
	"server_has_ipv6":                http.StatusUnprocessableEntity,
	"floating_ip_assigned":           http.StatusUnprocessableEntity,
	"server_already_attached":        http.StatusUnprocessableEntity,
	"ip_not_available":               http.StatusUnprocessableEntity,
	"no_subnet_available":            http.StatusUnprocessableEntity,
	"networks_overlap":               http.StatusUnprocessableEntity,

	// Codes of firewalls.
	"resource_in_use":                    http.StatusUnprocessableEntity,
	"firewall_already_applied":           http.StatusUnprocessableEntity,
	"firewall_managed_by_label_selector": http.StatusUnprocessableEntity,
	"private_net_only_server":            http.StatusUnprocessableEntity,
	"server_already_added":               http.StatusUnprocessableEntity,
	// POST /firewalls answers it with 422 instead.
	"firewall_resource_not_found": http.StatusNotFound,

	// Codes of zones.
	"incorrect_zone_mode": http.StatusUnprocessableEntity,
}

// Status returns the HTTP status of an error code; unknown codes give 500.
func Status(code string) int {
	if s, ok := statuses[code]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// Error writes Hetzner's error object with the status of code. details nil
// is written as an empty object.
func Error(w http.ResponseWriter, code, message string, details any) {
	if details == nil {
		details = map[string]any{}
	}
	JSON(w, Status(code), map[string]any{"error": map[string]any{
		"code":    code,
		"message": message,
		"details": details,
	}})
}

// InvalidInput writes Hetzner's invalid_input error for one field.
func InvalidInput(w http.ResponseWriter, field, message string) {
	Fail(w, &FieldError{Field: field, Message: message})
}

// FieldError is an error a handler returns from its transaction for
// invalid_input, uniqueness_error or an operation-specific 422 code.
type FieldError struct {
	Code    string // default "invalid_input"
	Field   string // "" = no details.fields
	Message string
}

func (e *FieldError) Error() string {
	if e.Field == "" {
		return e.code() + ": " + e.Message
	}
	return e.code() + ": " + e.Field + ": " + e.Message
}

func (e *FieldError) code() string {
	if e.Code == "" {
		return "invalid_input"
	}
	return e.Code
}

// Fail answers err: store.ErrNotFound with not_found, an *actions.Error
// with its code, a *FieldError with its code and details.fields, anything
// else with server_error (logged).
func Fail(w http.ResponseWriter, err error) {
	var ae *actions.Error
	var fe *FieldError
	switch {
	case errors.Is(err, store.ErrNotFound):
		NotFound(w)
	case errors.As(err, &ae):
		Error(w, ae.Code, ae.Message, nil)
	case errors.As(err, &fe):
		var details any
		message := fe.Message
		if fe.Field != "" {
			details = map[string]any{
				"fields": []any{map[string]any{"name": fe.Field, "messages": []string{fe.Message}}},
			}
			if fe.code() == "invalid_input" {
				message = "invalid input in field '" + fe.Field + "'"
			}
		}
		Error(w, fe.code(), message, details)
	default:
		ServerError(w, err)
	}
}

// ServerError logs err and writes Hetzner's server_error.
func ServerError(w http.ResponseWriter, err error) {
	slog.Error("api request failed", "err", err)
	Error(w, "server_error", "internal error", nil)
}

// NotFound writes Hetzner's not_found error.
func NotFound(w http.ResponseWriter) {
	Error(w, "not_found", "the requested resource was not found", nil)
}
