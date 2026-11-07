package web

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
)

// maxBody is the largest request body the API reads; Hetzner's largest
// bodies (user data, certificates) are far smaller.
const maxBody = 1 << 20

// DecodeJSON decodes the request body into body, a struct of the request's
// fields. A body that is not valid JSON is a json_error; a field of the wrong
// type is an invalid_input error for that field. An empty body leaves body as
// it is. Fields that body lacks are ignored, as Hetzner ignores them.
func DecodeJSON(req *Request, body any) error {
	data, err := io.ReadAll(io.LimitReader(req.Body, maxBody+1))
	if err != nil || len(data) > maxBody {
		return JSONError()
	}
	if len(data) == 0 {
		return nil
	}
	err = json.Unmarshal(data, body)
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &typeErr) && typeErr.Field != "":
		return InvalidFields(Field{Name: typeErr.Field, Messages: []string{"must be of type " + jsonType(typeErr.Type)}})
	case err != nil:
		return JSONError() // also a body that is no object
	}
	return nil
}

// jsonType returns the JSON Schema type of a Go type, as the spec names
// the types of fields.
func jsonType(goType reflect.Type) string {
	for goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}
	switch goType.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	default:
		return "object"
	}
}

// PathID returns an ID of the path ("id", "action_id"). The API has checked
// it before the handler runs: a value that is no ID matches no route.
func PathID(req *Request, name string) int64 {
	id, _ := strconv.ParseInt(req.PathValue(name), 10, 64)
	return id
}
