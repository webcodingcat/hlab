// Package labels validates Hetzner Cloud labels and evaluates label
// selectors, as the frozen API description defines them.
package labels

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const reservedPrefix = "hetzner.cloud/"

var (
	// name and value: alphanumerics at start and end, -_. and alphanumerics between.
	namePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`)
	// one DNS label of a DNS subdomain.
	dnsLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	// key and value tokens inside a selector; full validation is not needed to match.
	selectorKeyPattern   = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	selectorValuePattern = regexp.MustCompile(`^[A-Za-z0-9._-]*$`)
	setPattern           = regexp.MustCompile(`^(\S+)\s+(in|notin)\s*\((.*)\)$`)
)

// Validate checks every key and value against the Hetzner label rules.
func Validate(l map[string]string) error {
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := validateKey(k); err != nil {
			return err
		}
		if v := l[k]; v != "" && (len(v) > 63 || !namePattern.MatchString(v)) {
			return fmt.Errorf("label %q: invalid value %q", k, v)
		}
	}
	return nil
}

func validateKey(k string) error {
	if strings.HasPrefix(k, reservedPrefix) {
		return fmt.Errorf("label %q: the prefix %q is reserved", k, reservedPrefix)
	}
	name := k
	if i := strings.IndexByte(k, '/'); i >= 0 {
		prefix := k[:i]
		name = k[i+1:]
		if !validDNSSubdomain(prefix) {
			return fmt.Errorf("label %q: the prefix must be a DNS subdomain of at most 253 characters", k)
		}
	}
	if len(name) == 0 || len(name) > 63 || !namePattern.MatchString(name) {
		return fmt.Errorf("label %q: invalid key name", k)
	}
	return nil
}

func validDNSSubdomain(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if len(part) > 63 || !dnsLabelPattern.MatchString(part) {
			return false
		}
	}
	return true
}

type op int

const (
	opEqual op = iota
	opNotEqual
	opExists
	opNotExists
	opIn
	opNotIn
)

type term struct {
	key    string
	op     op
	values []string
}

// Selector is a parsed label selector; its terms are AND-ed.
type Selector struct {
	terms []term
}

// ParseSelector parses a Hetzner label selector: k=v, k==v, k!=v, k, !k,
// k in (a,b), k notin (a,b), with commas joining terms.
func ParseSelector(s string) (Selector, error) {
	parts, err := splitTerms(s)
	if err != nil {
		return Selector{}, err
	}
	var sel Selector
	for _, p := range parts {
		t, err := parseTerm(strings.TrimSpace(p))
		if err != nil {
			return Selector{}, fmt.Errorf("label selector %q: %w", s, err)
		}
		sel.terms = append(sel.terms, t)
	}
	return sel, nil
}

// splitTerms splits at commas outside parentheses.
func splitTerms(s string) ([]string, error) {
	var parts []string
	depth, start := 0, 0
	for i, c := range s {
		switch c {
		case '(':
			if depth > 0 {
				return nil, fmt.Errorf("label selector %q: nested parenthesis", s)
			}
			depth++
		case ')':
			if depth == 0 {
				return nil, fmt.Errorf("label selector %q: unmatched parenthesis", s)
			}
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("label selector %q: unclosed parenthesis", s)
	}
	return append(parts, s[start:]), nil
}

func parseTerm(p string) (term, error) {
	if m := setPattern.FindStringSubmatch(p); m != nil {
		t := term{key: m[1], op: opIn}
		if m[2] == "notin" {
			t.op = opNotIn
		}
		for _, v := range strings.Split(m[3], ",") {
			v = strings.TrimSpace(v)
			if !selectorValuePattern.MatchString(v) {
				return term{}, fmt.Errorf("invalid value %q", v)
			}
			t.values = append(t.values, v)
		}
		return t, checkKey(t)
	}
	var t term
	switch {
	case strings.HasPrefix(p, "!"):
		t = term{key: strings.TrimSpace(p[1:]), op: opNotExists}
	case strings.Contains(p, "!="):
		k, v, _ := strings.Cut(p, "!=")
		t = term{key: strings.TrimSpace(k), op: opNotEqual, values: []string{strings.TrimSpace(v)}}
	case strings.Contains(p, "=="):
		k, v, _ := strings.Cut(p, "==")
		t = term{key: strings.TrimSpace(k), op: opEqual, values: []string{strings.TrimSpace(v)}}
	case strings.Contains(p, "="):
		k, v, _ := strings.Cut(p, "=")
		t = term{key: strings.TrimSpace(k), op: opEqual, values: []string{strings.TrimSpace(v)}}
	default:
		t = term{key: p, op: opExists}
	}
	for _, v := range t.values {
		if !selectorValuePattern.MatchString(v) {
			return term{}, fmt.Errorf("invalid value %q", v)
		}
	}
	return t, checkKey(t)
}

func checkKey(t term) error {
	if !selectorKeyPattern.MatchString(t.key) {
		return fmt.Errorf("invalid key %q", t.key)
	}
	return nil
}

// Matches reports whether the label set satisfies every term.
func (s Selector) Matches(l map[string]string) bool {
	for _, t := range s.terms {
		v, ok := l[t.key]
		var match bool
		switch t.op {
		case opEqual:
			match = ok && v == t.values[0]
		case opNotEqual:
			match = !ok || v != t.values[0]
		case opExists:
			match = ok
		case opNotExists:
			match = !ok
		case opIn:
			match = ok && contains(t.values, v)
		case opNotIn:
			match = !ok || !contains(t.values, v)
		}
		if !match {
			return false
		}
	}
	return true
}

func contains(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}
