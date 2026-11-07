package web

import (
	"regexp"
	"slices"
	"strings"
)

// Selector is a parsed label selector: the resources whose labels match every
// one of its terms.
type Selector struct {
	terms []term
}

// term is one expression of a selector.
type term struct {
	key    string
	op     string // "=", "!=", "exists", "!exists", "in", "notin"
	values []string
}

// setTerm matches "k in (a,b)" and "k notin (a,b)".
var setTerm = regexp.MustCompile(`^(\S+)\s+(in|notin)\s*\(([^()]*)\)$`)

// ParseSelector parses Hetzner's label selector language: k=v, k==v, k!=v,
// k, !k, k in (v1,v2), k notin (v1,v2), joined by commas. An empty text
// selects everything. A text it cannot parse is an invalid_input error for
// the field label_selector.
func ParseSelector(text string) (Selector, error) {
	var selector Selector
	if strings.TrimSpace(text) == "" {
		return selector, nil
	}
	parts, ok := splitTerms(text)
	if !ok {
		return Selector{}, InvalidField("label_selector", "unbalanced parentheses")
	}
	for _, part := range parts {
		parsed, ok := parseTerm(strings.TrimSpace(part))
		if !ok {
			return Selector{}, InvalidField("label_selector", "invalid expression '"+strings.TrimSpace(part)+"'")
		}
		selector.terms = append(selector.terms, parsed)
	}
	return selector, nil
}

// splitTerms splits a selector at the commas outside parentheses.
func splitTerms(text string) ([]string, bool) {
	var parts []string
	depth, start := 0, 0
	for i, char := range text {
		switch char {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, text[start:i])
				start = i + 1
			}
		}
		if depth < 0 || depth > 1 {
			return nil, false
		}
	}
	return append(parts, text[start:]), depth == 0
}

// parseTerm parses one expression of a selector.
func parseTerm(text string) (term, bool) {
	if match := setTerm.FindStringSubmatch(text); match != nil {
		var values []string
		for value := range strings.SplitSeq(match[3], ",") {
			values = append(values, strings.TrimSpace(value))
		}
		return term{key: match[1], op: match[2], values: values}, true
	}
	if key, found := strings.CutPrefix(text, "!"); found {
		return term{key: strings.TrimSpace(key), op: "!exists"}, validKey(strings.TrimSpace(key))
	}
	for _, op := range []string{"!=", "==", "="} {
		if key, value, found := strings.Cut(text, op); found {
			key, value = strings.TrimSpace(key), strings.TrimSpace(value)
			if op == "==" {
				op = "="
			}
			return term{key: key, op: op, values: []string{value}}, validKey(key) && !strings.ContainsAny(value, "=!")
		}
	}
	return term{key: text, op: "exists"}, validKey(text)
}

// validKey tells whether a key can be a label key at all.
func validKey(key string) bool {
	return key != "" && !strings.ContainsAny(key, " =!(),")
}

// Matches tells whether labels match every term of the selector.
func (s Selector) Matches(labels map[string]string) bool {
	for _, t := range s.terms {
		value, has := labels[t.key]
		var matches bool
		switch t.op {
		case "=":
			matches = has && value == t.values[0]
		case "!=":
			matches = !has || value != t.values[0]
		case "exists":
			matches = has
		case "!exists":
			matches = !has
		case "in":
			matches = has && slices.Contains(t.values, value)
		case "notin":
			matches = !has || !slices.Contains(t.values, value)
		}
		if !matches {
			return false
		}
	}
	return true
}
