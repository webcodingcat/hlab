package web

import (
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Selector is a parsed label selector: the resources whose labels match every
// one of its terms.
type Selector struct {
	terms []term
}

// term is one expression of a selector.
type term struct {
	key    string
	op     string // "=", "!=", "exists", "!exists", "in", "notin", "any"
	values []string
}

// setTerm matches "k in …" and "k notin …"; parseSetTerm checks the group.
var setTerm = regexp.MustCompile(`^(\S+)\s+(in|notin)(\s.*)?$`)

// operators are the comparisons of a term, the longer before the shorter at
// the same place.
var operators = []string{"!=", "==", "="}

// ParseSelector parses Hetzner's label selector language: k=v, k==v, k!=v,
// k, !k, k in (v1,v2), k notin (v1,v2), joined by commas. An empty text
// selects everything. A text it cannot parse is Hetzner's invalid_input with
// Hetzner's reason (measured 2026-11-07, compat/requirements.md).
func ParseSelector(text string) (Selector, error) {
	var selector Selector
	if strings.TrimSpace(text) == "" {
		return selector, nil
	}
	parts, ok := splitTerms(text)
	if !ok {
		return Selector{}, invalidSyntax("missing brackets for group")
	}
	for i, part := range parts {
		part = strings.TrimSpace(part)
		// Hetzner takes one comma at the end ("k=v,"), not two.
		if part == "" && i > 0 && i == len(parts)-1 {
			break
		}
		parsed, err := parseTerm(part)
		if err != nil {
			return Selector{}, err
		}
		selector.terms = append(selector.terms, parsed)
	}
	return selector, nil
}

// splitTerms splits a selector at the commas outside parentheses. A group
// that is not closed, or one inside another, cannot be split. A closing
// bracket without its opening one is left in its term, whose key or value
// then is malformed, as Hetzner reports it.
func splitTerms(text string) ([]string, bool) {
	var parts []string
	depth, start := 0, 0
	for i, char := range text {
		switch {
		case char == '(':
			depth++
		case char == ')' && depth > 0:
			depth--
		case char == ',' && depth == 0:
			parts = append(parts, text[start:i])
			start = i + 1
		}
		if depth > 1 {
			return nil, false
		}
	}
	return append(parts, text[start:]), depth == 0
}

// parseTerm parses one expression of a selector, with Hetzner's reasons for
// what it cannot parse.
func parseTerm(text string) (term, error) {
	if text == "" {
		return term{}, invalidSyntax("no key given")
	}
	if match := setTerm.FindStringSubmatch(text); match != nil {
		return parseSetTerm(match[1], match[2], strings.TrimSpace(match[3]))
	}
	if bangAfterWord(text) {
		return term{}, invalidSyntax("'!' operator must be in front of value")
	}
	rest := strings.TrimSpace(strings.TrimLeft(text, "!"))
	if op, at := firstOperator(rest); at >= 0 {
		// Hetzner ignores a "!" in front of a comparison: "!k=v" selects
		// what "k=v" selects (measured).
		return parseComparison(rest, op, at)
	}
	if rest != text {
		if rest == "" {
			return term{}, invalidSyntax("'!' operator requires value afterwards")
		}
		// "!!k" is "!k" (measured)
		return term{key: rest, op: "!exists"}, checkSelectorKey(rest)
	}
	if strings.ContainsFunc(text, unicode.IsSpace) {
		return term{}, invalidSyntax("only one key allowed")
	}
	return term{key: text, op: "exists"}, checkSelectorKey(text)
}

// bangAfterWord tells whether a "!" ends a word ("k!", "k=v!"), which
// Hetzner refuses; a "!" in front of a word or of "=" is taken.
func bangAfterWord(text string) bool {
	for i := 1; i < len(text); i++ {
		if text[i] == '!' && (i == len(text)-1 || text[i+1] == ' ' || text[i+1] == ',') {
			return true
		}
	}
	return false
}

// parseComparison parses "k=v", "k==v" or "k!=v", where op is found at the
// index at.
func parseComparison(text, op string, at int) (term, error) {
	key, value := strings.TrimSpace(text[:at]), strings.TrimSpace(text[at+len(op):])
	if key == "" {
		return term{}, invalidSyntax("'" + op + "' operator requires value before")
	}
	if value == "" {
		return term{}, invalidSyntax("'" + op + "' operator requires value afterwards")
	}
	// "k=v=" is a second comparison without a value; Hetzner says so.
	if next, nextAt := firstOperator(value); nextAt >= 0 && strings.TrimSpace(value[nextAt+len(next):]) == "" {
		return term{}, invalidSyntax("'" + next + "' operator requires value afterwards")
	}
	if err := checkSelectorKey(key); err != nil {
		return term{}, err
	}
	if op == "==" {
		op = "="
	}
	switch {
	case strings.HasPrefix(value, "!"):
		// Hetzner selects everything for "k=!v" and "k!=!v" (measured).
		return term{key: key, op: "any"}, nil
	case strings.Contains(value, "="):
		// Hetzner takes "k=a=b" and selects nothing (measured): no label
		// value has a "=", so the value is kept as it is.
		return term{key: key, op: op, values: []string{value}}, nil
	case !validLabelValue(value):
		return term{}, malformedValue()
	}
	return term{key: key, op: op, values: []string{value}}, nil
}

// parseSetTerm parses the group of "k in (…)" or "k notin (…)".
func parseSetTerm(key, op, group string) (term, error) {
	inner, found := strings.CutPrefix(group, "(")
	inner, closed := strings.CutSuffix(inner, ")")
	if !found || !closed {
		return term{}, invalidSyntax("missing brackets for group")
	}
	// Hetzner blames the key for an empty group (measured).
	if strings.TrimSpace(inner) == "" {
		return term{}, malformedKey()
	}
	if err := checkSelectorKey(key); err != nil {
		return term{}, err
	}
	var values []string
	for value := range strings.SplitSeq(inner, ",") {
		value = strings.TrimSpace(value)
		if value == "" || !validLabelValue(value) {
			return term{}, malformedValue()
		}
		values = append(values, value)
	}
	return term{key: key, op: op, values: values}, nil
}

// firstOperator returns the operator that comes first in text and its index,
// or -1.
func firstOperator(text string) (string, int) {
	for i := range text {
		for _, op := range operators {
			if strings.HasPrefix(text[i:], op) {
				return op, i
			}
		}
	}
	return "", -1
}

// checkSelectorKey returns Hetzner's error for a key that is no label key.
func checkSelectorKey(key string) error {
	if !validLabelKey(key) {
		return malformedKey()
	}
	return nil
}

// invalidSyntax returns Hetzner's error for a selector it cannot parse.
func invalidSyntax(reason string) *Error {
	return invalidSelector("invalid syntax: " + reason)
}

// malformedKey returns Hetzner's error for a key that breaks the label rules.
func malformedKey() *Error {
	return invalidSelector("key contains invalid characters or is malformed")
}

// malformedValue returns Hetzner's error for a value that breaks the label
// rules.
func malformedValue() *Error {
	return invalidSelector("value contains invalid characters or is malformed")
}

// invalidSelector returns Hetzner's 422 for a label selector: invalid_input
// with details {} and the reason in the message.
func invalidSelector(reason string) *Error {
	return &Error{
		Status:  http.StatusUnprocessableEntity,
		Code:    "invalid_input",
		Message: "invalid label_selector: " + reason,
		Details: map[string]any{},
	}
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
			// unlike notin, != needs the label (measured 2026-11-07)
			matches = has && value != t.values[0]
		case "any":
			matches = true
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
