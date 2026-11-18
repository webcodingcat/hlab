package api

import (
	"net/http"
	"strconv"
)

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
