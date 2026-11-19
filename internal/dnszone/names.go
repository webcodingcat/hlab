// Package dnszone holds the data rules of DNS zones as Hetzner applies them:
// zone names, RRSets and their records, and zone files.
package dnszone

import (
	"strings"

	"golang.org/x/net/publicsuffix"
)

// RRSet is an RRSet in Hetzner's shape without API bookkeeping.
type RRSet struct {
	Name    string   // relative, lower case, "@" for the apex
	Type    string   // A, AAAA, CAA, CNAME, DS, HINFO, HTTPS, MX, NS, PTR, RP, SOA, SRV, SVCB, TLSA, TXT
	TTL     *int     // nil: the zone's default TTL
	Records []Record // order kept as given
}

// Record is one record of an RRSet.
type Record struct {
	Value   string // presentation format; names without trailing dot are relative to the zone
	Comment string
}

// FieldError is a validation error on one request field; the zones module
// turns it into api.FieldError (code "" means invalid_input).
type FieldError struct{ Code, Field, Message string }

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

func fieldErr(field, msg string) error { return &FieldError{Field: field, Message: msg} }

// Nameservers are the assigned authoritative nameservers of every zone.
var Nameservers = []string{"hydrogen.ns.hetzner.com", "oxygen.ns.hetzner.com", "helium.ns.hetzner.de"}

// privateSuffixes are the private-use suffixes zones may lie under.
var privateSuffixes = []string{"internal", "test", "home.arpa"}

// CheckZoneName accepts lower case names of at most 255 characters without
// trailing dot that are exactly one label below an ICANN public suffix, or
// exactly one label below internal, test or home.arpa; it refuses
// hetzner.internal, local and everything under arpa except home.arpa.
// Error field "name".
func CheckZoneName(name string) error {
	switch {
	case name == "":
		return fieldErr("name", "name is required")
	case len(name) > 255:
		return fieldErr("name", "name must have at most 255 characters")
	case strings.HasSuffix(name, "."):
		return fieldErr("name", "name must not end with a dot")
	case name != strings.ToLower(name):
		return fieldErr("name", "name must be lower case")
	}
	for _, l := range strings.Split(name, ".") {
		if !validLabel(l, false) {
			return fieldErr("name", "name has an invalid label "+quote(l))
		}
	}
	if name == "hetzner.internal" {
		return fieldErr("name", "hetzner.internal is reserved")
	}
	_, parent, ok := strings.Cut(name, ".")
	if !ok {
		return fieldErr("name", "a public suffix is no zone")
	}
	for _, s := range privateSuffixes {
		if parent == s {
			return nil
		}
		if name == s || strings.HasSuffix(name, "."+s) {
			return fieldErr("name", "subdomains are not supported")
		}
	}
	if name == "arpa" || strings.HasSuffix(name, ".arpa") {
		return fieldErr("name", "zones under arpa are not supported")
	}
	suffix := icannSuffix(name)
	if suffix == "" {
		return fieldErr("name", "unknown TLD")
	}
	if parent != suffix {
		if name == suffix || !strings.HasSuffix(name, "."+suffix) {
			return fieldErr("name", "a public suffix is no zone")
		}
		return fieldErr("name", "subdomains are not supported")
	}
	return nil
}

// icannSuffix returns the public suffix of name in the ICANN section of the
// Public Suffix List, "" when there is none.
func icannSuffix(name string) string {
	s := name
	for {
		ps, icann := publicsuffix.PublicSuffix(s)
		if icann {
			return ps
		}
		_, rest, ok := strings.Cut(ps, ".")
		if !ok {
			return ""
		}
		s = rest
	}
}

// PrivateUse reports whether a zone lies under internal, test or home.arpa.
func PrivateUse(zone string) bool {
	for _, s := range privateSuffixes {
		if strings.HasSuffix(zone, "."+s) {
			return true
		}
	}
	return false
}

// validLabel reports whether l is a lower case DNS label of letters, digits
// and hyphens; owner names also allow underscores and a "*" label.
func validLabel(l string, owner bool) bool {
	if l == "" || len(l) > 63 {
		return false
	}
	if owner && l == "*" {
		return true
	}
	for _, c := range l {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
		case owner && c == '_':
		default:
			return false
		}
	}
	return true
}

func quote(s string) string { return `"` + s + `"` }
