package web

import (
	"net/http"
	"regexp"
	"strings"
)

// Check collects what is wrong with the fields of a request body, so that one
// answer names every field at once, as Hetzner's does.
//
//	var check web.Check
//	check.Required("name", body.Name != "")
//	check.Labels(body.Labels)
//	if err := check.Err(); err != nil { ... }
type Check struct {
	fields []Field
}

// Fail notes that a field is invalid, with Hetzner's message ("is required").
func (c *Check) Fail(name, message string) {
	for i := range c.fields {
		if c.fields[i].Name == name {
			c.fields[i].Messages = append(c.fields[i].Messages, message)
			return
		}
	}
	c.fields = append(c.fields, Field{Name: name, Messages: []string{message}})
}

// Required notes a required field that the body lacks.
func (c *Check) Required(name string, given bool) {
	if !given {
		c.Fail(name, "is required")
	}
}

// Label rules of Hetzner's documentation ("Labels"): a key is an optional
// prefix, a DNS subdomain, and a name; a value may be empty.
var (
	labelName   = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9_.-]{0,61}[a-zA-Z0-9])?$`)
	labelValue  = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9_.-]{0,61}[a-zA-Z0-9])?)?$`)
	labelPrefix = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
)

// Labels notes the labels that break Hetzner's rules, as the field labels.
// The prefix hetzner.cloud/ is Hetzner's own.
func (c *Check) Labels(labels map[string]string) {
	for key, value := range labels {
		prefix, name, hasPrefix := strings.Cut(key, "/")
		if !hasPrefix {
			prefix, name = "", key
		}
		switch {
		case hasPrefix && (len(prefix) > 253 || !labelPrefix.MatchString(prefix)):
			c.Fail("labels", "label key '"+key+"' has an invalid prefix")
		case prefix == "hetzner.cloud" || strings.HasSuffix(prefix, ".hetzner.cloud"):
			c.Fail("labels", "label key '"+key+"' uses the reserved prefix hetzner.cloud/")
		case !labelName.MatchString(name):
			c.Fail("labels", "label key '"+key+"' is invalid")
		}
		if !labelValue.MatchString(value) {
			c.Fail("labels", "label value '"+value+"' of '"+key+"' is invalid")
		}
	}
}

// Err returns the invalid_input error of the fields noted, or nil.
func (c *Check) Err() error {
	if len(c.fields) == 0 {
		return nil
	}
	return InvalidFields(c.fields...)
}

// InvalidFields returns Hetzner's 422 invalid_input for invalid fields of a
// request body: the message names the first field and its first message
// ("invalid input in field 'name': is required"), the details list them all,
// as the error example of Hetzner's documentation shows.
func InvalidFields(fields ...Field) *Error {
	first := fields[0]
	return InvalidInput("invalid input in field '"+first.Name+"': "+first.Messages[0], fields...)
}

// Uniqueness returns Hetzner's 409 uniqueness_error: another resource of the
// project has the value of the field already. Its details name the field
// without messages, as the error example of Hetzner's documentation shows.
func Uniqueness(message, field string) *Error {
	return &Error{
		Status:  http.StatusConflict,
		Code:    "uniqueness_error",
		Message: message,
		Details: map[string][]map[string]string{"fields": {{"name": field}}},
	}
}
