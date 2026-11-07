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

// Hetzner's API (measured 2026-11-07 on SSH keys): a POST names every
// invalid field in its message, "fields" even for one; a PUT names the
// first, "field".
func TestCheckCollectsEveryField(t *testing.T) {
	var check web.Check
	if check.Err() != nil {
		t.Error("a check without failures has an error")
	}
	check.Required("name", false)
	check.Required("public_key", true)
	check.Length("name", "", 1, 128)
	check.Fail("labels", "invalid label key")
	check.Fail("labels", "invalid label key") // once only
	err := check.Err()
	if err.Error() != "invalid input in fields 'name', 'labels'" ||
		details(t, err) != `{"fields":[{"name":"name","messages":["Missing data for required field.","Length must be between 1 and 128."]},{"name":"labels","messages":["invalid label key"]}]}` {
		t.Errorf("the error is %q with %s", err, details(t, err))
	}

	update := web.Check{Update: true}
	update.Length("name", strings.Repeat("x", 129), 1, 128)
	if err := update.Err(); err.Error() != "invalid input in field 'name'" {
		t.Errorf("the error of a PUT is %q", err)
	}
}

// Hetzner's label rules ("Labels" in its documentation); the prefix
// hetzner.cloud/ is accepted (measured 2026-11-07).
func TestCheckLabels(t *testing.T) {
	for labels, valid := range map[string]bool{
		"env=prod":                          true,
		"env=":                              true,
		"example.com/env=prod":              true,
		"a.b-c_d=x.y-z_0":                   true,
		"hetzner.cloud/env=x":               true,
		"-env=prod":                         false,
		"env=-prod":                         false,
		"env=bad value!":                    false,
		"bad key=x":                         false,
		"Example.com/env=x":                 false,
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

// Hetzner's API (measured 2026-11-07): the label messages of a POST and a
// PUT differ.
func TestCheckLabelMessages(t *testing.T) {
	for _, test := range []struct {
		update bool
		want   string
	}{
		{false, `{"fields":[{"name":"labels","messages":["invalid label key","invalid label value"]}]}`},
		{true, `{"fields":[{"name":"labels","messages":["key contains invalid characters or is malformed","value contains invalid characters or is malformed"]}]}`},
	} {
		check := web.Check{Update: test.update}
		check.Labels(map[string]string{"bad key": "bad value!"})
		if got := details(t, check.Err()); got != test.want {
			t.Errorf("update %v: %s", test.update, got)
		}
	}
}

// Hetzner's API (measured 2026-11-07): a taken name or fingerprint.
func TestUniqueness(t *testing.T) {
	err := web.Uniqueness("SSH key not unique", "public_key")
	if err.Status != 409 || err.Code != "uniqueness_error" || details(t, err) != `{"fields":[{"name":"public_key"}]}` {
		t.Errorf("the error is %+v with %s", err, details(t, err))
	}
}
