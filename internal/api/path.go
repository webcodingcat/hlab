package api

import (
	"net/http"
	"strconv"
)

// NotFound writes Hetzner's not_found error.
func NotFound(w http.ResponseWriter) {
	Error(w, http.StatusNotFound, "not_found", "the requested resource was not found", nil)
}

// PathID returns the path value "id" as a resource ID. When it is not a
// positive integer it writes not_found and returns false.
func PathID(w http.ResponseWriter, req *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		NotFound(w)
		return 0, false
	}
	return id, true
}
