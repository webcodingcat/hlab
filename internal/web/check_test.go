package web_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/web"
)

// details returns the details of an *web.Error as JSON.
func details(t *testing.T, err error) string {
	t.Helper()
	var apiErr *web.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("%v is no *web.Error", err)
	}
	data, _ := json.Marshal(apiErr.Details)
	return string(data)
}

func TestCheckCollectsEveryField(t *testing.T) {
	var check web.Check
	if check.Err() != nil {
		t.Error("a check without failures has an error")
	}
	check.Required("name", false)
	check.Required("public_key", true)
	check.Fail("name", "is too long")
	check.Fail("labels", "label key 'x y' is invalid")
	err := check.Err()
	if err.Error() != "invalid input in field 'name': is required" ||
		details(t, err) != `{"fields":[{"name":"name","messages":["is required","is too long"]},{"name":"labels","messages":["label key 'x y' is invalid"]}]}` {
		t.Errorf("the error is %q with %s", err, details(t, err))
	}
}

// Hetzner's label rules ("Labels" in its documentation).
func TestCheckLabels(t *testing.T) {
	for labels, valid := range map[string]bool{
		"env=prod":                          true,
		"env=":                              true,
		"example.com/env=prod":              true,
		"a.b-c_d=x.y-z_0":                   true,
		"-env=prod":                         false,
		"env=-prod":                         false,
		"bad key=x":                         false,
		"Example.com/env=x":                 false,
		"hetzner.cloud/env=x":               false,
		"x.hetzner.cloud/env=x":             false,
		strings.Repeat("k", 64) + "=x":      false,
		"env=" + strings.Repeat("v", 64):    false,
		strings.Repeat("k", 63) + "=" + "x": true,
	} {
		key, value, _ := strings.Cut(labels, "=")
		var check web.Check
		check.Labels(map[string]string{key: value})
		if (check.Err() == nil) != valid {
			t.Errorf("%q: valid is %v, want %v (%v)", labels, check.Err() == nil, valid, check.Err())
		}
	}
}

// Hetzner's API: a name or fingerprint taken already, as in the error
// example of its documentation.
func TestUniqueness(t *testing.T) {
	err := web.Uniqueness("SSH key with the same fingerprint already exists", "public_key")
	if err.Status != 409 || err.Code != "uniqueness_error" || details(t, err) != `{"fields":[{"name":"public_key"}]}` {
		t.Errorf("the error is %+v with %s", err, details(t, err))
	}
}
