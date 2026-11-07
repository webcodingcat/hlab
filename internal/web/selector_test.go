package web_test

import (
	"errors"
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
		"empty=":                      true,
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

func TestSelectorRefusesWhatItCannotParse(t *testing.T) {
	for _, selector := range []string{"env in (a,b", "env)", "=x", "a,,b", "env=a=b", "!", "a b"} {
		_, err := web.ParseSelector(selector)
		var apiErr *web.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "invalid_input" {
			t.Errorf("%q: %v, want invalid_input", selector, err)
		}
	}
}
