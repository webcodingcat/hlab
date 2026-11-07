package web

import (
	"encoding/json"
	"io"
	"strconv"
)

// maxBody is the largest request body the API reads; Hetzner's largest
// bodies (user data, certificates) are far smaller.
const maxBody = 1 << 20

// DecodeJSON decodes the request body into body; a body that is not valid
// JSON is a json_error. An empty body leaves body as it is.
func DecodeJSON(req *Request, body any) error {
	data, err := io.ReadAll(io.LimitReader(req.Body, maxBody+1))
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

// PathID returns an ID of the path ("id", "action_id"). The API has checked
// it before the handler runs: a value that is no ID matches no route.
func PathID(req *Request, name string) int64 {
	id, _ := strconv.ParseInt(req.PathValue(name), 10, 64)
	return id
}
