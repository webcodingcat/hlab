// Package spectest checks the API's answers against Hetzner's OpenAPI specs in
// compat/specs, less the deviations in compat/spec-deviations.yaml. The API's
// unit tests send every request through its Client, so that every answer is
// checked.
package spectest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
)

// The specs, by the names compat/spec-deviations.yaml uses.
const (
	Cloud   = "cloud"   // the Cloud API
	Hetzner = "hetzner" // the Hetzner API (Storage Boxes)
)

// Spec is one of Hetzner's specs, ready to check answers against.
type Spec struct {
	base       string // the path of its server URL: "/v1"
	operations []operation
}

// operation is an operation of the spec with its path template, split into
// segments; a parameter ("{id}") is an empty segment.
type operation struct {
	method   string
	segments []string
	value    *openapi3.Operation
}

var (
	loadedMu sync.Mutex
	loaded   = map[string]*Spec{}
)

// Load returns the spec of that name (Cloud, Hetzner) from compat/ with its
// deviations applied; it is read once per test binary.
func Load(name string) (*Spec, error) {
	loadedMu.Lock()
	defer loadedMu.Unlock()
	if spec, found := loaded[name]; found {
		return spec, nil
	}
	compat, err := compatDir()
	if err != nil {
		return nil, err
	}
	deviations, err := readDeviations(filepath.Join(compat, "spec-deviations.yaml"))
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(compat, "specs", name+".spec.json"))
	if err != nil {
		return nil, err
	}
	spec, err := parse(name, data, deviations)
	if err != nil {
		return nil, fmt.Errorf("spec %s: %w", name, err)
	}
	loaded[name] = spec
	return spec, nil
}

// compatDir finds compat/ in the repository: the go.mod above the working
// directory, which `go test` sets to the package's folder.
func compatDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "compat"), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod above the working directory")
		}
		dir = parent
	}
}

// parse reads a spec, applies the deviations of its name and indexes its
// operations.
func parse(name string, data []byte, deviations []Deviation) (*Spec, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	for _, deviation := range deviations {
		if deviation.Spec != name {
			continue
		}
		if err := deviation.apply(raw); err != nil {
			return nil, err
		}
	}
	changed, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(changed)
	if err != nil {
		return nil, err
	}
	if len(doc.Servers) == 0 {
		return nil, errors.New("the spec names no server")
	}
	serverURL, err := url.Parse(doc.Servers[0].URL)
	if err != nil {
		return nil, err
	}
	spec := &Spec{base: strings.TrimSuffix(serverURL.Path, "/")}
	for template, item := range doc.Paths.Map() {
		var segments []string
		for _, segment := range strings.Split(strings.Trim(template, "/"), "/") {
			if strings.HasPrefix(segment, "{") {
				segment = ""
			}
			segments = append(segments, segment)
		}
		for method, value := range item.Operations() {
			spec.operations = append(spec.operations, operation{method: method, segments: segments, value: value})
		}
	}
	return spec, nil
}

// find returns the operation of a method and a request path. A literal
// segment wins over a parameter, as /servers/actions does over
// /servers/{id}.
func (s *Spec) find(method, path string) *operation {
	rest, found := strings.CutPrefix(path, s.base+"/")
	if !found {
		return nil
	}
	segments := strings.Split(rest, "/")
	var best *operation
	bestLiterals := -1
	for i := range s.operations {
		candidate := &s.operations[i]
		if candidate.method != method || len(candidate.segments) != len(segments) {
			continue
		}
		literals, matches := 0, true
		for j, segment := range candidate.segments {
			switch segment {
			case "":
			case segments[j]:
				literals++
			default:
				matches = false
			}
		}
		if matches && literals > bestLiterals {
			best, bestLiterals = candidate, literals
		}
	}
	return best
}

// Check checks an answer against the spec: its status must be one the
// operation lists, its content type one the status lists, and a JSON body
// must match the schema. An error answer of a path the spec lacks (404 for an
// unknown route) must still be in Hetzner's error format.
func (s *Spec) Check(method, path string, status int, contentType string, body []byte) error {
	op := s.find(method, path)
	if op == nil {
		if status < 400 {
			return fmt.Errorf("%s %s: answered %d, but the spec has no such operation", method, path, status)
		}
		if status == http.StatusMethodNotAllowed && len(body) == 0 {
			// Hetzner's 405 for another method than GET on a single
			// action of networks, firewalls, Floating IPs and Primary IPs
			// has no body (measured 2026-11-09)
			return nil
		}
		return checkErrorFormat(body)
	}
	response := op.value.Responses.Value(fmt.Sprint(status))
	for _, key := range []string{fmt.Sprintf("%dXX", status/100), fmt.Sprintf("%dxx", status/100), "default"} {
		if response == nil {
			response = op.value.Responses.Value(key)
		}
	}
	if response == nil {
		return fmt.Errorf("%s %s: the status %d is not in the spec", method, path, status)
	}
	mediaType, _, _ := strings.Cut(contentType, ";")
	if mediaType = strings.TrimSpace(mediaType); len(body) > 0 && mediaType != "application/json" {
		// a body that is not JSON has no schema to match, only its type
		if response.Value.Content.Get(mediaType) == nil {
			return fmt.Errorf("%s %s %d: the content type %q is not in the spec", method, path, status, contentType)
		}
		return nil
	}
	media := response.Value.Content.Get("application/json")
	if media == nil || media.Schema == nil {
		if len(bytes.TrimSpace(body)) > 0 {
			return fmt.Errorf("%s %s %d: the spec has no body, the answer has one", method, path, status)
		}
		return nil
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return fmt.Errorf("%s %s %d: the body is not JSON: %w", method, path, status, err)
	}
	if err := media.Schema.Value.VisitJSON(value, openapi3.EnableJSONSchema2020(), openapi3.MultiErrors()); err != nil {
		return fmt.Errorf("%s %s %d: %w", method, path, status, err)
	}
	return nil
}

// checkErrorFormat checks that a body is Hetzner's error format, for answers
// that no operation of the spec describes.
func checkErrorFormat(body []byte) error {
	var answer struct {
		Error *struct {
			Code    *string         `json:"code"`
			Message *string         `json:"message"`
			Details json.RawMessage `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return fmt.Errorf("the error answer is not JSON: %w", err)
	}
	if answer.Error == nil || answer.Error.Code == nil || answer.Error.Message == nil {
		return fmt.Errorf("the error answer lacks error.code or error.message: %s", body)
	}
	details := bytes.TrimSpace(answer.Error.Details)
	if len(details) > 0 && details[0] != '{' && string(details) != "null" {
		return fmt.Errorf("error.details is neither an object nor null: %s", details)
	}
	return nil
}
