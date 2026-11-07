package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

// maxBody is the largest request body the API reads, so that one request
// cannot fill the memory. Hetzner took a body of 64 MiB (measured
// 2026-11-07) and named no limit; its largest real bodies (user data,
// certificates) are far smaller.
const maxBody = 64 << 20

// DecodeJSON decodes the request body into body, a struct of the request's
// fields, as Hetzner's API does: a body that is not valid JSON is a
// json_error with the decoder's message; valid JSON that is no object, or a
// field of the wrong type, is "failed to parse json". An empty body leaves
// body as it is. Fields that body lacks are ignored, as Hetzner ignores them,
// and so is whatever follows the first JSON value (measured 2026-11-07:
// `{…} garbage` and `{…} {…}` created the key of the first object). It takes
// an http.Request, so that the lab control decodes its bodies the same way.
func DecodeJSON(req *http.Request, body any) error {
	data, err := io.ReadAll(io.LimitReader(req.Body, maxBody+1))
	if err != nil {
		return JSONError(err.Error())
	}
	if len(data) > maxBody {
		return JSONError("http: request body too large")
	}
	if len(data) == 0 {
		return nil
	}
	err = json.NewDecoder(bytes.NewReader(data)).Decode(body)
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &typeErr):
		return ParseFailed()
	case err != nil:
		return JSONError(err.Error())
	}
	return nil
}

// PathID returns an ID of the path ("id", "action_id"). The API has checked
// it before the handler runs: a value that is no ID matches no route.
func PathID(req *Request, name string) int64 {
	id, _ := strconv.ParseInt(req.PathValue(name), 10, 64)
	return id
}
