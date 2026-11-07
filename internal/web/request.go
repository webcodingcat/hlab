package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

// maxBody is the largest request body the API reads; Hetzner's largest
// bodies (user data, certificates) are far smaller.
const maxBody = 1 << 20

// DecodeJSON decodes the request body into body; a body that is not valid
// JSON is a json_error. An empty body leaves body as it is.
func DecodeJSON(r *http.Request, body any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(data) > maxBody {
		return JSONError()
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, body); err != nil {
		return JSONError()
	}
	return nil
}

// PathID returns the path value "id" as an ID; a value that is no ID is a
// not_found error for what the path names ("action").
func PathID(r *http.Request, what string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, NotFound(what)
	}
	return id, nil
}
