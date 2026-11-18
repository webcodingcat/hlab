package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// JSON writes v as a JSON response with status.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write response", "err", err)
	}
}

// Error writes Hetzner's error object. details may be nil.
func Error(w http.ResponseWriter, status int, code, message string, details any) {
	JSON(w, status, map[string]any{"error": map[string]any{
		"code":    code,
		"message": message,
		"details": details,
	}})
}

// defaultPerPage is Hetzner's default page size.
const defaultPerPage = 25

// List writes items under key as one page with meta.pagination.
func List[T any](w http.ResponseWriter, key string, items []T) {
	if items == nil {
		items = []T{}
	}
	perPage := max(defaultPerPage, len(items))
	JSON(w, http.StatusOK, map[string]any{
		key: items,
		"meta": map[string]any{"pagination": map[string]any{
			"page":          1,
			"per_page":      perPage,
			"previous_page": nil,
			"next_page":     nil,
			"last_page":     1,
			"total_entries": len(items),
		}},
	})
}

// InvalidInput writes Hetzner's invalid_input error for one field.
func InvalidInput(w http.ResponseWriter, field, message string) {
	Error(w, http.StatusBadRequest, "invalid_input", "invalid input in field '"+field+"'", map[string]any{
		"fields": []any{map[string]any{"name": field, "messages": []string{message}}},
	})
}

// ServerError logs err and writes Hetzner's server_error.
func ServerError(w http.ResponseWriter, err error) {
	slog.Error("api request failed", "err", err)
	Error(w, http.StatusInternalServerError, "server_error", "internal error", nil)
}
