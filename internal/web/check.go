package web

import (
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Check collects what is wrong with the fields of a request body, so that one
// answer names every field at once, as Hetzner's does.
//
//	check := web.Check{}               // a POST; web.Check{Update: true} for a PUT
//	check.Required("name", body.Name != nil)
//	check.Labels(body.Labels)
//	if err := check.Err(); err != nil { ... }
type Check struct {
	// Update gives the texts of Hetzner's PUT requests, which differ from
	// those of its POST requests (measured 2026-11-07 on SSH keys).
	Update bool
	fields []Field
}

// Fail notes that a field is invalid, with Hetzner's message.
func (c *Check) Fail(name, message string) {
	for i := range c.fields {
		if c.fields[i].Name == name {
			if !slices.Contains(c.fields[i].Messages, message) {
				c.fields[i].Messages = append(c.fields[i].Messages, message)
			}
			return
		}
	}
	c.fields = append(c.fields, Field{Name: name, Messages: []string{message}})
}

// Required notes a required field that the body lacks or gives as null.
func (c *Check) Required(name string, given bool) {
	if !given {
		c.Fail(name, "Missing data for required field.")
	}
}

// Length notes a text field whose length is out of its bounds, as names are
// ("Length must be between 1 and 128.").
func (c *Check) Length(name, value string, least, most int) {
	if length := len([]rune(value)); length < least || length > most {
		c.Fail(name, "Length must be between "+strconv.Itoa(least)+" and "+strconv.Itoa(most)+".")
	}
}

// Label rules of Hetzner's documentation ("Labels"): a key is an optional
// prefix, a DNS subdomain, and a name; a value may be empty. The prefix
// hetzner.cloud/ is accepted (measured 2026-11-07), though the documentation
// reserves it.
var (
	labelName   = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9_.-]{0,61}[a-zA-Z0-9])?$`)
	labelValue  = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9_.-]{0,61}[a-zA-Z0-9])?)?$`)
	labelPrefix = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
)

// Labels notes, as the field labels, the labels that break Hetzner's rules,
// with the texts of a POST or a PUT.
func (c *Check) Labels(labels map[string]string) {
	keyMessage, valueMessage := "invalid label key", "invalid label value"
	if c.Update {
		keyMessage, valueMessage = "key contains invalid characters or is malformed", "value contains invalid characters or is malformed"
	}
	for key, value := range labels {
		prefix, name, hasPrefix := strings.Cut(key, "/")
		if !hasPrefix {
			prefix, name = "", key
		}
		if (hasPrefix && (len(prefix) > 253 || !labelPrefix.MatchString(prefix))) || !labelName.MatchString(name) {
			c.Fail("labels", keyMessage)
		}
		if !labelValue.MatchString(value) {
			c.Fail("labels", valueMessage)
		}
	}
}

// Err returns Hetzner's 422 invalid_input for the fields noted, or nil. The
// details list every field with its messages; the message names the fields,
// as a POST does ("invalid input in fields 'name', 'public_key'", even for
// one field) or as a PUT does ("invalid input in field 'name'", measured
// with one field).
func (c *Check) Err() error {
	if len(c.fields) == 0 {
		return nil
	}
	if c.Update {
		return InvalidInput("invalid input in field '"+c.fields[0].Name+"'", c.fields...)
	}
	names := make([]string, len(c.fields))
	for i, field := range c.fields {
		names[i] = "'" + field.Name + "'"
	}
	return InvalidInput("invalid input in fields "+strings.Join(names, ", "), c.fields...)
}

// Uniqueness returns Hetzner's 409 uniqueness_error: another resource of the
// project has the value of the field already ("SSH key not unique"). Its
// details name the field without messages (measured 2026-11-07).
func Uniqueness(message, field string) *Error {
	return &Error{
		Status:  http.StatusConflict,
		Code:    "uniqueness_error",
		Message: message,
		Details: map[string][]map[string]string{"fields": {{"name": field}}},
	}
}
