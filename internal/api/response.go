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
