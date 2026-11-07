package web_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/web"
)

// The expressions of Hetzner's label selector language, as its docs list
// them.
func TestSelector(t *testing.T) {
	labels := map[string]string{"env": "production", "type": "web", "example.com/my": "label", "empty": ""}
	for selector, want := range map[string]bool{
		"":                            true,
		"env=production":              true,
		"env==production":             true,
		"env=testing":                 false,
		"env!=testing":                true,
		"env!=production":             false,
		"missing!=x":                  true,
		"type":                        true,
		"missing":                     false,
		"!missing":                    true,
		"!type":                       false,
		"env in (testing,production)": true,
		"env in (testing, staging)":   false,
		"missing in (a)":              false,
		"env notin (testing,staging)": true,
		"env notin (production)":      false,
		"missing notin (a)":           true,
		"env=production,type!=db":     true,
		"env=production,!type":        false,
		"env in (a,production),type":  true,
		"example.com/my=label":        true,
		" env = production , type ":   true,
	} {
		parsed, err := web.ParseSelector(selector)
		if err != nil {
			t.Errorf("%q: %v", selector, err)
			continue
		}
		if got := parsed.Matches(labels); got != want {
			t.Errorf("%q matches: %v, want %v", selector, got, want)
		}
	}
}

// Hetzner's answers to selectors it cannot parse (measured 2026-11-07): 422
// invalid_input, details {}, and these messages after "invalid
// label_selector: ".
func TestSelectorRefusesWhatItCannotParse(t *testing.T) {
	long := strings.Repeat("k", 70)
	for selector, want := range map[string]string{
		"=":            "invalid syntax: '=' operator requires value before",
		"=x":           "invalid syntax: '=' operator requires value before",
		"k=":           "invalid syntax: '=' operator requires value afterwards",
		"k==":          "invalid syntax: '==' operator requires value afterwards",
		"k!=":          "invalid syntax: '!=' operator requires value afterwards",
		"k=v=":         "invalid syntax: '=' operator requires value afterwards",
		"!":            "invalid syntax: '!' operator requires value afterwards",
		"k!":           "invalid syntax: '!' operator must be in front of value",
		"k=v!":         "invalid syntax: '!' operator must be in front of value",
		"a b":          "invalid syntax: only one key allowed",
		"kin (a)":      "invalid syntax: only one key allowed",
		",":            "invalid syntax: no key given",
		"a,,b":         "invalid syntax: no key given",
		"k=v,,":        "invalid syntax: no key given",
		"k in a":       "invalid syntax: missing brackets for group",
		"k notin a":    "invalid syntax: missing brackets for group",
		"k in (a,b":    "invalid syntax: missing brackets for group",
		"k in (a,(b))": "invalid syntax: missing brackets for group",
		"k$=v":         "key contains invalid characters or is malformed",
		"env)":         "key contains invalid characters or is malformed",
		"(k)":          "key contains invalid characters or is malformed",
		"-k=v":         "key contains invalid characters or is malformed",
		"a/b/c=d":      "key contains invalid characters or is malformed",
		long:           "key contains invalid characters or is malformed",
		"k in ()":      "key contains invalid characters or is malformed",
		"k=-v":         "value contains invalid characters or is malformed",
		"k=(v)":        "value contains invalid characters or is malformed",
		"k=" + long:    "value contains invalid characters or is malformed",
		"k in (a b)":   "value contains invalid characters or is malformed",
		"k in (a,)":    "value contains invalid characters or is malformed",
	} {
		_, err := web.ParseSelector(selector)
		var apiErr *web.Error
		if !errors.As(err, &apiErr) || apiErr.Status != 422 || apiErr.Code != "invalid_input" ||
			apiErr.Message != "invalid label_selector: "+want {
			t.Errorf("%q: %v, want %q", selector, err, want)
			continue
		}
		if details, _ := json.Marshal(apiErr.Details); string(details) != "{}" {
			t.Errorf("%q: the details are %s, want {}", selector, details)
		}
	}
}

// Selectors Hetzner takes (measured 2026-11-07) beyond those of its docs.
func TestSelectorTakesWhatHetznerTakes(t *testing.T) {
	for _, selector := range []string{"k=v,", "! k", "k in ( a , b )", "k in (a),j", "k=a,b", "hetzner.cloud/x=y"} {
		if _, err := web.ParseSelector(selector); err != nil {
			t.Errorf("%q: %v", selector, err)
		}
	}
}
