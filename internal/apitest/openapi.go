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
	"strings"
	"sync"

	"github.com/pb33f/libopenapi"
	validator "github.com/pb33f/libopenapi-validator"
)

// MissingDescription is the message a test fails with when the frozen
// OpenAPI description is not in the user cache folder.
const MissingDescription = "OpenAPI description not cached: run `task openapi`"

// DescriptionPath returns where `task openapi` caches the frozen Hetzner
// OpenAPI description: <user cache dir>/hlab/openapi/cloud.spec.json.
func DescriptionPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hlab", "openapi", "cloud.spec.json"), nil
}

var (
	specOnce  sync.Once
	specVal   validator.Validator
	specErr   error
	specMutex sync.Mutex // the validator is shared by parallel tests
)

// errMissing marks a missing cache file.
var errMissing = errors.New(MissingDescription)

// loadValidator builds the validator once per test binary.
func loadValidator() (validator.Validator, error) {
	specOnce.Do(func() {
		path, err := DescriptionPath()
		if err != nil {
			specErr = fmt.Errorf("%s (%v)", MissingDescription, err)
			return
		}
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			specErr = errMissing
			return
		}
		if err != nil {
			specErr = fmt.Errorf("read %s: %w", path, err)
			return
		}
		doc, err := libopenapi.NewDocument(upperRangeKeys(data))
		if err != nil {
			specErr = fmt.Errorf("parse %s: %w", path, err)
			return
		}
		// ValidateDocument() is deliberately not called: the Hetzner
		// description does not pass the 3.1.2 meta-schema (EXPERIMENTS.md E8).
		v, errs := validator.NewValidator(doc)
		if len(errs) > 0 {
			specErr = fmt.Errorf("build validator from %s: %w", path, errors.Join(errs...))
			return
		}
		specVal = v
	})
	return specVal, specErr
}

// RequireDescription fails the test at once when the frozen OpenAPI
// description is not cached or cannot be loaded.
func RequireDescription(t testingT) {
	t.Helper()
	if _, err := loadValidator(); err != nil {
		t.Fatal(err.Error())
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
// description and reports every violation on t. The response body stays
// readable for the caller.
func ValidateResponse(t testingT, req *http.Request, resp *http.Response) {
	t.Helper()
	v, err := loadValidator()
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
	specMutex.Lock()
	ok, verrs := v.ValidateHttpResponse(req, &check)
	specMutex.Unlock()
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

// upperRangeKeys rewrites the response range keys "4xx" and "5xx" of the
// Hetzner description to "4XX" and "5XX": OpenAPI defines them upper case
// and libopenapi-validator matches only that form, so without this every
// error response would be reported as undocumented.
func upperRangeKeys(data []byte) []byte {
	for _, k := range []string{"1", "2", "3", "4", "5"} {
		data = bytes.ReplaceAll(data, []byte(`"`+k+`xx":`), []byte(`"`+k+`XX":`))
	}
	return data
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
