package api

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/store"
)

// codes lists every error code with its HTTP status in a fixed order. It is
// the error table of the frozen OpenAPI description (info.description),
// plus token_readonly, which the API answers to writes with a read-only
// token, followed by the operation-specific codes of single endpoints.
var codes = []CodeStatus{
	{"json_error", http.StatusBadRequest},
	{"unauthorized", http.StatusUnauthorized},
	{"forbidden", http.StatusForbidden},
	{"maintenance", http.StatusForbidden},
	{"resource_limit_exceeded", http.StatusForbidden},
	{"token_readonly", http.StatusForbidden},
	{"not_found", http.StatusNotFound},
	{"method_not_allowed", http.StatusMethodNotAllowed},
	{"uniqueness_error", http.StatusConflict},
	{"conflict", http.StatusConflict},
	{"deprecated_api_endpoint", http.StatusGone},
	{"resource_unavailable", http.StatusPreconditionFailed},
	{"invalid_input", http.StatusUnprocessableEntity},
	{"service_error", http.StatusUnprocessableEntity},
	{"unsupported_error", http.StatusUnprocessableEntity},
	{"locked", http.StatusLocked},
	{"protected", http.StatusLocked},
	{"rate_limit_exceeded", http.StatusTooManyRequests},
	{"server_error", http.StatusInternalServerError},
	{"bad_gateway", http.StatusBadGateway},
	{"unavailable", http.StatusServiceUnavailable},
	{"timeout", http.StatusGatewayTimeout},

	// Operation-specific codes of single endpoints.
	{"placement_error", http.StatusUnprocessableEntity},
	{"server_not_stopped", http.StatusUnprocessableEntity},
	{"invalid_server_type", http.StatusUnprocessableEntity},
	{"already_in_placement_group", http.StatusUnprocessableEntity},
	{"volume_already_attached", http.StatusUnprocessableEntity},

	// Codes of networks, primary IPs and floating IPs.
	{"must_be_unassigned", http.StatusUnprocessableEntity},
	{"primary_ip_assigned", http.StatusUnprocessableEntity},
	{"primary_ip_already_assigned", http.StatusUnprocessableEntity},
	{"primary_ip_datacenter_mismatch", http.StatusUnprocessableEntity},
	{"primary_ip_version_mismatch", http.StatusPreconditionFailed},
	{"server_has_ipv4", http.StatusUnprocessableEntity},
	{"server_has_ipv6", http.StatusUnprocessableEntity},
	{"floating_ip_assigned", http.StatusUnprocessableEntity},
	{"server_already_attached", http.StatusUnprocessableEntity},
	{"ip_not_available", http.StatusUnprocessableEntity},
	{"no_subnet_available", http.StatusUnprocessableEntity},
	{"networks_overlap", http.StatusUnprocessableEntity},

	// Codes of firewalls.
	{"resource_in_use", http.StatusUnprocessableEntity},
	{"firewall_already_applied", http.StatusUnprocessableEntity},
	{"firewall_managed_by_label_selector", http.StatusUnprocessableEntity},
	{"private_net_only_server", http.StatusUnprocessableEntity},
	{"server_already_added", http.StatusUnprocessableEntity},
	// POST /firewalls answers it with 422 instead.
	{"firewall_resource_not_found", http.StatusNotFound},

	// Codes of zones.
	{"incorrect_zone_mode", http.StatusUnprocessableEntity},

	// Codes of load balancers and the primary IPs they hold.
	{"load_balancer_already_attached", http.StatusUnprocessableEntity},
	{"load_balancer_not_attached_to_network", http.StatusUnprocessableEntity},
	{"targets_without_use_private_ip", http.StatusUnprocessableEntity},
	{"load_balancer_public_interface_disabled", http.StatusUnprocessableEntity},
	{"resolve_cloud_private_targets_error", http.StatusUnprocessableEntity},
	{"resolve_cloud_public_targets_error", http.StatusUnprocessableEntity},
	{"target_already_defined", http.StatusUnprocessableEntity},
	{"invalid_load_balancer_type", http.StatusUnprocessableEntity},
	{"primary_ip_blocked", http.StatusUnprocessableEntity},
	{"primary_ip_location_mismatch", http.StatusUnprocessableEntity},
	// add_service answers 412; update_service answers 422 through
	// FieldError.Status.
	{"source_port_already_used", http.StatusPreconditionFailed},
}

// CodeStatus is an error code with its HTTP status.
type CodeStatus struct {
	Code   string `json:"code"`
	Status int    `json:"status"`
}

// statuses maps every error code of codes to its HTTP status.
var statuses = func() map[string]int {
	m := make(map[string]int, len(codes))
	for _, c := range codes {
		m[c.Code] = c.Status
	}
	return m
}()

// Codes returns every error code of Status in a fixed order: the general
// Hetzner error table first (json_error ... timeout, as in the frozen
// description), then the operation-specific codes, each group in the
// order of the source table.
func Codes() []CodeStatus {
	return slices.Clone(codes)
}

// Status returns the HTTP status of an error code; unknown codes give 500.
func Status(code string) int {
	if s, ok := statuses[code]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// HasStatus reports whether code is in the status table.
func HasStatus(code string) bool {
	_, ok := statuses[code]
	return ok
}

// Error writes Hetzner's error object with the status of code. details nil
// is written as an empty object.
func Error(w http.ResponseWriter, code, message string, details any) {
	writeError(w, Status(code), code, message, details)
}

// writeError writes Hetzner's error object with status.
func writeError(w http.ResponseWriter, status int, code, message string, details any) {
	if details == nil {
		details = map[string]any{}
	}
	JSON(w, status, map[string]any{"error": map[string]any{
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
// invalid_input, uniqueness_error or an operation-specific code.
type FieldError struct {
	Code    string // default "invalid_input"
	Field   string // "" = no details.fields
	Message string
	// Status overrides the HTTP status of Code for this one answer; 0
	// keeps Status(Code).
	Status int
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
// with its code, a *FieldError with its code (and its Status, when set) and
// details.fields, anything
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
		status := fe.Status
		if status == 0 {
			status = Status(fe.code())
		}
		writeError(w, status, fe.code(), message, details)
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
