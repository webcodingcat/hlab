package apitest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/pb33f/libopenapi"
	validator "github.com/pb33f/libopenapi-validator"

	"github.com/webcodingcat/hlab/internal/api"
)

// MissingDescription is the message a test fails with when a frozen
// OpenAPI description is not in the user cache folder.
const MissingDescription = "OpenAPI description not cached: run `task openapi`"

// DescriptionPath returns where `task openapi` caches the frozen Hetzner
// Cloud OpenAPI description: <user cache dir>/hlab/openapi/cloud.spec.json.
func DescriptionPath() (string, error) {
	return cachePath("cloud.spec.json")
}

// HetznerDescriptionPath returns where `task openapi` caches the frozen
// Hetzner API description (Storage Boxes):
// <user cache dir>/hlab/openapi/hetzner.spec.json.
func HetznerDescriptionPath() (string, error) {
	return cachePath("hetzner.spec.json")
}

func cachePath(name string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hlab", "openapi", name), nil
}

// description is one frozen OpenAPI description and its validator, built
// once per test binary.
type description struct {
	pathOf func() (string, error)
	patch  func([]byte) ([]byte, error)

	once sync.Once
	val  validator.Validator
	err  error
	mu   sync.Mutex // the validator is shared by parallel tests
}

var (
	cloudDescription   = &description{pathOf: DescriptionPath, patch: lbAssignee}
	hetznerDescription = &description{pathOf: HetznerDescriptionPath, patch: hetznerServer}
)

// descriptionFor returns the description a request path belongs to.
func descriptionFor(path string) *description {
	if path == api.HetznerPrefix || strings.HasPrefix(path, api.HetznerPrefix+"/") {
		return hetznerDescription
	}
	return cloudDescription
}

// load builds the validator once.
func (d *description) load() (validator.Validator, error) {
	d.once.Do(func() {
		path, err := d.pathOf()
		if err != nil {
			d.err = fmt.Errorf("%s (%v)", MissingDescription, err)
			return
		}
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			d.err = fmt.Errorf("%s (%s missing)", MissingDescription, filepath.Base(path))
			return
		}
		if err != nil {
			d.err = fmt.Errorf("read %s: %w", path, err)
			return
		}
		data, err = d.patch(data)
		if err != nil {
			d.err = fmt.Errorf("patch %s: %w", path, err)
			return
		}
		doc, err := libopenapi.NewDocument(data)
		if err != nil {
			d.err = fmt.Errorf("parse %s: %w", path, err)
			return
		}
		// ValidateDocument() is deliberately not called: the Hetzner
		// descriptions do not pass the 3.1.2 meta-schema (EXPERIMENTS.md E8).
		v, errs := validator.NewValidator(doc)
		if len(errs) > 0 {
			d.err = fmt.Errorf("build validator from %s: %w", path, errors.Join(errs...))
			return
		}
		d.val = v
	})
	return d.val, d.err
}

// RequireDescription fails the test at once when a frozen OpenAPI
// description (cloud.spec.json or hetzner.spec.json) is not cached or
// cannot be loaded.
func RequireDescription(t testingT) {
	t.Helper()
	for _, d := range []*description{cloudDescription, hetznerDescription} {
		if _, err := d.load(); err != nil {
			t.Fatal(err.Error())
		}
	}
}

// testingT is the part of testing.TB the helpers need.
type testingT interface {
	Helper()
	Errorf(format string, args ...any)
	Fatal(args ...any)
	Fatalf(format string, args ...any)
}

// ValidateResponse checks one response to req against the frozen OpenAPI
// description of its path (hetzner.spec.json under api.HetznerPrefix,
// cloud.spec.json otherwise) and reports every violation on t. The
// response body stays readable for the caller.
func ValidateResponse(t testingT, req *http.Request, resp *http.Response) {
	t.Helper()
	d := descriptionFor(req.URL.Path)
	v, err := d.load()
	if err != nil {
		t.Fatal(err.Error())
		return
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
		return
	}
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))

	check := *resp
	check.Body = io.NopCloser(bytes.NewReader(body))
	d.mu.Lock()
	ok, verrs := v.ValidateHttpResponse(req, &check)
	d.mu.Unlock()
	if ok {
		return
	}
	var b strings.Builder
	for _, e := range verrs {
		fmt.Fprintf(&b, "\n  - %s: %s", e.Message, e.Reason)
		for _, s := range e.SchemaValidationErrors {
			fmt.Fprintf(&b, "\n      %s: %s", s.FieldPath, s.Reason)
		}
	}
	t.Errorf("%s %s: response %d does not match the OpenAPI description:%s\n  body: %s",
		req.Method, req.URL.RequestURI(), resp.StatusCode, b.String(), truncate(body, 2000))
}

// hetznerServerURL is the server URL of the Hetzner API description.
var hetznerServerURL = []byte(`"url": "https://api.hetzner.com/v1"`)

// hetznerServer rewrites the server URL of the Hetzner API description to
// the relative "/hetzner/v1": the validator strips only the server URL's
// path from a request path, and hlab serves that API under /hetzner/v1.
func hetznerServer(data []byte) ([]byte, error) {
	if !bytes.Contains(data, hetznerServerURL) {
		return nil, errors.New("server URL https://api.hetzner.com/v1 not found")
	}
	return bytes.ReplaceAll(data, hetznerServerURL, []byte(`"url": "`+api.HetznerPrefix+`"`)), nil
}

// assigneeEnum matches the start of the enum of primary_ip.assignee_type
// in the response schemas, which describe it as the type of resource the
// Primary IP "is assigned to".
var assigneeEnum = regexp.MustCompile(`("assignee_type":\s*\{\s*"description":\s*"Type of resource the [^"]*is assigned to\.[^"]*",\s*"type":\s*"string",\s*"enum":\s*\[)`)

// lbAssignee adds "load_balancer" to the enum of primary_ip.assignee_type
// in the responses: hlab shows a Primary IP held by a load balancer that
// way, the one documented deviation in a response shape.
func lbAssignee(data []byte) ([]byte, error) {
	if !assigneeEnum.Match(data) {
		return nil, errors.New("primary_ip.assignee_type enum not found")
	}
	return assigneeEnum.ReplaceAll(data, []byte(`$1"load_balancer",`)), nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
