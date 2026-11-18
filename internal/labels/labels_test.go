package labels_test

import (
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/labels"
)

func TestLabels(t *testing.T) {
	t.Run("API-06", func(t *testing.T) {
		set := map[string]string{"env": "prod", "tier": "web"}

		cases := []struct {
			selector string
			match    bool
		}{
			{"env=prod", true},
			{"env=dev", false},
			{"env==prod", true},
			{"env==dev", false},
			{"env!=dev", true},
			{"env!=prod", false},
			{"env", true},
			{"missing", false},
			{"!missing", true},
			{"!env", false},
			{"env in (prod,dev)", true},
			{"env in (dev,test)", false},
			{"env notin (dev,test)", true},
			{"env notin (prod,dev)", false},
			{"env=prod,tier=web", true},
			{"env=prod,tier=db", false},
			{"env=prod,!missing", true},
			{"env in (prod,dev),tier notin (web)", false},
		}
		for _, c := range cases {
			t.Run(c.selector, func(t *testing.T) {
				s, err := labels.ParseSelector(c.selector)
				if err != nil {
					t.Fatalf("ParseSelector(%q): %v", c.selector, err)
				}
				if got := s.Matches(set); got != c.match {
					t.Errorf("%q matches %v: %v, want %v", c.selector, set, got, c.match)
				}
			})
		}

		for _, bad := range []string{"!!!==", "env in (prod", "env in (", "env notin (prod,dev"} {
			t.Run("malformed "+bad, func(t *testing.T) {
				if _, err := labels.ParseSelector(bad); err == nil {
					t.Errorf("ParseSelector(%q) succeeded, want an error", bad)
				}
			})
		}
	})

	t.Run("API-07", func(t *testing.T) {
		name63 := strings.Repeat("a", 63)
		// A DNS subdomain of 253 characters: three labels of 63 and one of 61.
		prefix253 := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." +
			strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)

		valid := []struct{ name, key, value string }{
			{"plain", "key", "value"},
			{"with prefix", "example.com/my", "label"},
			{"empty value", "just-a-key", ""},
			{"upper case and digits", "Key9", "Value9"},
			{"special characters inside the name", "a-b_c.d", "v"},
			{"special characters inside the value", "k", "a-b_c.d"},
			{"name of 63", name63, "v"},
			{"prefix of 253", prefix253 + "/k", "v"},
			{"value of 63", "k", strings.Repeat("v", 63)},
			{"single characters", "k", "v"},
		}
		for _, c := range valid {
			t.Run("valid "+c.name, func(t *testing.T) {
				if err := labels.Validate(map[string]string{c.key: c.value}); err != nil {
					t.Errorf("Validate(%q=%q): %v, want nil", c.key, c.value, err)
				}
			})
		}

		invalid := []struct{ name, key, value string }{
			{"empty key", "", "v"},
			{"empty name after prefix", "example.com/", "v"},
			{"name of 64", strings.Repeat("a", 64), "v"},
			{"prefix of 254", prefix253 + "d/k", "v"},
			{"name starts with dash", "-key", "v"},
			{"name ends with dash", "key-", "v"},
			{"name starts with dot", ".key", "v"},
			{"name ends with underscore", "key_", "v"},
			{"space in name", "ke y", "v"},
			{"invalid character in name", "ke@y", "v"},
			{"two slashes", "a/b/c", "v"},
			{"prefix not a DNS subdomain", "exa_mple.com/key", "v"},
			{"prefix starts with dash", "-example.com/key", "v"},
			{"reserved prefix", "hetzner.cloud/key", "v"},
			{"value of 64", "k", strings.Repeat("v", 64)},
			{"value starts with dash", "k", "-v"},
			{"value ends with dot", "k", "v."},
			{"space in value", "k", "a b"},
			{"invalid character in value", "k", "a/b"},
		}
		for _, c := range invalid {
			t.Run("invalid "+c.name, func(t *testing.T) {
				if err := labels.Validate(map[string]string{c.key: c.value}); err == nil {
					t.Errorf("Validate(%q=%q) succeeded, want an error", c.key, c.value)
				}
			})
		}
	})
}
