package dnszone

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// types are the RRSet types Hetzner supports.
var types = map[string]bool{
	"A": true, "AAAA": true, "CAA": true, "CNAME": true, "DS": true, "HINFO": true,
	"HTTPS": true, "MX": true, "NS": true, "PTR": true, "RP": true, "SOA": true,
	"SRV": true, "SVCB": true, "TLSA": true, "TXT": true,
}

const maxRecords = 50

// CheckRRSet validates one RRSet of zone as at Hetzner: name, type, ttl
// (nil or 60..2147483647), 1..50 records with distinct values, each value
// parsed by miekg/dns for its type plus Hetzner's rules (TXT quoted, each
// string at most 255 characters). Fields "name", "type", "ttl", "records",
// "records[i].value".
func CheckRRSet(zone string, set RRSet) error {
	if err := checkName(zone, set.Name); err != nil {
		return err
	}
	if !types[set.Type] {
		return fieldErr("type", "unsupported type "+quote(set.Type))
	}
	if set.TTL != nil && (*set.TTL < 60 || *set.TTL > math.MaxInt32) {
		return fieldErr("ttl", "ttl must be between 60 and 2147483647")
	}
	switch {
	case len(set.Records) == 0:
		return fieldErr("records", "records must not be empty")
	case len(set.Records) > maxRecords:
		return fieldErr("records", fmt.Sprintf("at most %d records are allowed", maxRecords))
	}
	seen := map[string]bool{}
	for i, r := range set.Records {
		if seen[r.Value] {
			return fieldErr("records", "duplicate record value "+quote(r.Value))
		}
		seen[r.Value] = true
		if _, err := parse(zone, set.Name, set.Type, 3600, r.Value); err != nil {
			return fieldErr(fmt.Sprintf("records[%d].value", i), err.Error())
		}
	}
	return nil
}

// checkName validates a relative owner name of zone.
func checkName(zone, name string) error {
	switch {
	case name == "@":
		return nil
	case name == "":
		return fieldErr("name", "name is required")
	case name != strings.ToLower(name):
		return fieldErr("name", "name must be lower case")
	case strings.HasSuffix(name, "."):
		return fieldErr("name", "name must not end with a dot")
	case name == zone || strings.HasSuffix(name, "."+zone):
		return fieldErr("name", "name must not end with the zone name")
	}
	for _, l := range strings.Split(name, ".") {
		if !validLabel(l, true) {
			return fieldErr("name", "name has an invalid label "+quote(l))
		}
	}
	return nil
}

// parse parses one record value of type typ at name in zone with Hetzner's
// extra rules; names in value without trailing dot are relative to zone.
func parse(zone, name, typ string, ttl int, value string) (dns.RR, error) {
	if strings.ContainsAny(value, "\n\r") {
		return nil, fmt.Errorf("value must be on one line")
	}
	switch typ {
	case "TXT":
		if err := checkQuoted(value); err != nil {
			return nil, err
		}
	case "RP":
		if strings.Contains(value, "@") {
			return nil, errors.New("RP mailbox must be in DNS form (admin.example.com.), not an email address")
		}
	}
	line := fmt.Sprintf("%s %d IN %s %s", name, ttl, typ, value)
	zp := dns.NewZoneParser(strings.NewReader(line), zone+".", "")
	rr, ok := zp.Next()
	if err := zp.Err(); err != nil {
		return nil, fmt.Errorf("invalid %s value: %v", typ, err)
	}
	if !ok || rr == nil || dns.TypeToString[rr.Header().Rrtype] != typ {
		return nil, fmt.Errorf("invalid %s value", typ)
	}
	if zp.Comment() != "" {
		return nil, fmt.Errorf("value must not contain a comment")
	}
	return rr, nil
}

// checkQuoted checks that value consists of quoted strings of at most 255
// characters each, separated by spaces.
func checkQuoted(value string) error {
	s := strings.TrimSpace(value)
	if s == "" {
		return fmt.Errorf("TXT value must not be empty")
	}
	for s != "" {
		if s[0] != '"' {
			return fmt.Errorf("TXT value must consist of quoted strings")
		}
		n, end := 0, -1
		for i := 1; i < len(s); i++ {
			switch s[i] {
			case '\\':
				if i+3 < len(s) && isDigit(s[i+1]) && isDigit(s[i+2]) && isDigit(s[i+3]) {
					i += 3
				} else {
					i++
				}
			case '"':
				end = i
			}
			if end >= 0 {
				break
			}
			n++
		}
		if end < 0 {
			return fmt.Errorf("TXT value has an unterminated quoted string")
		}
		if n > 255 {
			return fmt.Errorf("each TXT string must have at most 255 characters")
		}
		rest := s[end+1:]
		s = strings.TrimLeft(rest, " \t")
		if s != "" && len(s) == len(rest) {
			return fmt.Errorf("TXT strings must be separated by spaces")
		}
	}
	return nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// CheckConflicts checks a zone's RRSets together: a CNAME is alone at its
// name and has one record. Field "type", message
// "(<name>, CNAME) conflicts with (<name>, <type>)".
func CheckConflicts(sets []RRSet) error {
	for _, c := range sets {
		if c.Type != "CNAME" {
			continue
		}
		other := ""
		if len(c.Records) > 1 {
			other = "CNAME"
		}
		for _, s := range sets {
			if other == "" && s.Name == c.Name && s.Type != "CNAME" {
				other = s.Type
			}
		}
		if other == "" && c.Name == "@" {
			other = "SOA"
		}
		if other != "" {
			return fieldErr("type", fmt.Sprintf("(%s, CNAME) conflicts with (%s, %s)", c.Name, c.Name, other))
		}
	}
	return nil
}

// ToRR returns the records of set as RRs with absolute names; zoneTTL
// applies when set.TTL is nil.
func ToRR(zone string, zoneTTL int, set RRSet) ([]dns.RR, error) {
	ttl := zoneTTL
	if set.TTL != nil {
		ttl = *set.TTL
	}
	out := make([]dns.RR, 0, len(set.Records))
	for _, r := range set.Records {
		rr, err := parse(zone, set.Name, set.Type, ttl, r.Value)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", set.Name, set.Type, err)
		}
		out = append(out, rr)
	}
	return out, nil
}

// DefaultSets are the SOA and NS RRSets of a new zone.
func DefaultSets(zone string, serial uint32) []RRSet {
	soa := fmt.Sprintf("%s. dns.hetzner.com. %d 86400 10800 3600000 3600", Nameservers[0], serial)
	ns := make([]Record, len(Nameservers))
	for i, n := range Nameservers {
		ns[i] = Record{Value: n + "."}
	}
	return []RRSet{
		{Name: "@", Type: "SOA", Records: []Record{{Value: soa}}},
		{Name: "@", Type: "NS", Records: ns},
	}
}

// NextSerial returns the next YYYYMMDDnn serial after old for day now (UTC).
func NextSerial(old uint32, now time.Time) uint32 {
	day, _ := strconv.ParseUint(now.UTC().Format("20060102"), 10, 32)
	base := uint32(day) * 100
	if old >= base {
		return old + 1
	}
	return base
}
