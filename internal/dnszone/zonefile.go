package dnszone

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/miekg/dns"
)

// entry is one logical line of a zone file (parentheses joined, comments
// dropped) with the number of the line it starts on.
type entry struct {
	line int
	text string
}

// ParseZonefile parses a BIND zone file with Hetzner's import rules
// ($ORIGIN must be zone + ".", $TTL becomes ttl (0: none), class IN only,
// SOA present, the three Nameservers as apex NS, comments dropped). Errors
// are *FieldError on field "zonefile" naming the line.
func ParseZonefile(zone, text string) (ttl int, sets []RRSet, err error) {
	var rrEntries []entry
	ttlSeen := false
	for _, e := range splitEntries(text) {
		fields := strings.Fields(e.text)
		if !strings.HasPrefix(fields[0], "$") {
			rrEntries = append(rrEntries, e)
			continue
		}
		switch strings.ToUpper(fields[0]) {
		case "$ORIGIN":
			if len(fields) < 2 || !strings.EqualFold(fields[1], zone+".") {
				return 0, nil, lineErr(e.line, "$ORIGIN must be "+zone+".")
			}
		case "$TTL":
			if len(fields) < 2 {
				return 0, nil, lineErr(e.line, "$TTL needs a value")
			}
			v, ok := parseTTL(fields[1])
			if !ok {
				return 0, nil, lineErr(e.line, "$TTL has an invalid value "+quote(fields[1]))
			}
			if !ttlSeen {
				ttl, ttlSeen = v, true
			}
		default:
			return 0, nil, lineErr(e.line, fields[0]+" is not supported")
		}
	}

	zp := dns.NewZoneParser(strings.NewReader(text), zone+".", "")
	var rrs []dns.RR
	for rr, ok := zp.Next(); ok; rr, ok = zp.Next() {
		rrs = append(rrs, rr)
	}
	if perr := zp.Err(); perr != nil {
		return 0, nil, &FieldError{Field: "zonefile", Message: perr.Error()}
	}

	index := map[string]int{}
	var lines [][]int // per set: the line of each record
	for i, rr := range rrs {
		line := 0
		if i < len(rrEntries) {
			line = rrEntries[i].line
		}
		h := rr.Header()
		typ := dns.TypeToString[h.Rrtype]
		if h.Class != dns.ClassINET {
			return 0, nil, lineErr(line, "only class IN is supported")
		}
		name, ok := relativeOwner(zone, h.Name)
		if !ok {
			return 0, nil, lineErr(line, "name "+quote(h.Name)+" is outside the zone")
		}
		name = strings.ToLower(name)
		explicitTTL := false
		if i < len(rrEntries) {
			pre, rdata := splitRR(rrEntries[i].text, typ)
			for _, tok := range pre {
				if _, isTTL := parseTTL(tok); isTTL {
					explicitTTL = true
				}
			}
			if typ == "TXT" {
				if qerr := checkQuoted(rdata); qerr != nil {
					return 0, nil, lineErr(line, qerr.Error())
				}
			}
		}
		key := name + "/" + typ
		n, found := index[key]
		if !found {
			n = len(sets)
			index[key] = n
			set := RRSet{Name: name, Type: typ}
			if explicitTTL {
				t := int(h.Ttl)
				set.TTL = &t
			}
			sets = append(sets, set)
			lines = append(lines, nil)
		}
		sets[n].Records = append(sets[n].Records, Record{Value: relativeRdata(zone, rr)})
		lines[n] = append(lines[n], line)
	}

	for n, set := range sets {
		if cerr := CheckRRSet(zone, set); cerr != nil {
			line := lines[n][0]
			var fe *FieldError
			if errors.As(cerr, &fe) {
				var i int
				if _, serr := fmt.Sscanf(fe.Field, "records[%d].value", &i); serr == nil && i < len(lines[n]) {
					line = lines[n][i]
				}
			}
			return 0, nil, lineErr(line, cerr.Error())
		}
	}
	if err := checkApex(sets); err != nil {
		return 0, nil, err
	}
	if cerr := CheckConflicts(sets); cerr != nil {
		return 0, nil, &FieldError{Field: "zonefile", Message: cerr.Error()}
	}
	return ttl, sets, nil
}

// checkApex checks that sets hold a SOA and the assigned nameservers as
// apex NS records.
func checkApex(sets []RRSet) error {
	hasSOA := false
	ns := map[string]bool{}
	for _, s := range sets {
		if s.Name != "@" {
			continue
		}
		switch s.Type {
		case "SOA":
			hasSOA = true
		case "NS":
			for _, r := range s.Records {
				ns[strings.ToLower(r.Value)] = true
			}
		}
	}
	if !hasSOA {
		return &FieldError{Field: "zonefile", Message: "zonefile must contain a SOA record"}
	}
	for _, n := range Nameservers {
		if !ns[n+"."] {
			return &FieldError{Field: "zonefile", Message: "zonefile must contain the NS records " + strings.Join(Nameservers, ", ")}
		}
	}
	return nil
}

func lineErr(line int, msg string) error {
	return &FieldError{Field: "zonefile", Message: fmt.Sprintf("line %d: %s", line, msg)}
}

// splitEntries splits text into logical entries: comments are dropped,
// lines inside parentheses joined, blank entries skipped. Leading
// whitespace is kept, as it means the owner of the previous entry.
func splitEntries(text string) []entry {
	var out []entry
	var b strings.Builder
	line, start := 1, 0
	inQuote, inComment, depth := false, false, 0
	flush := func() {
		if start > 0 {
			out = append(out, entry{line: start, text: b.String()})
		}
		b.Reset()
		start = 0
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '\n' {
			inComment = false
			if depth == 0 && !inQuote {
				flush()
			} else {
				b.WriteByte(' ')
			}
			line++
			continue
		}
		if inComment {
			continue
		}
		switch {
		case c == '\\' && i+1 < len(text) && text[i+1] != '\n':
			b.WriteByte(c)
			i++
			c = text[i]
		case c == '"':
			inQuote = !inQuote
		case c == ';' && !inQuote:
			inComment = true
			continue
		case (c == '(' || c == ')') && !inQuote:
			if c == '(' {
				depth++
			} else if depth > 0 {
				depth--
			}
			c = ' '
		}
		if start == 0 && c != ' ' && c != '\t' && c != '\r' {
			start = line
		}
		b.WriteByte(c)
	}
	flush()
	return out
}

// parseTTL parses a TTL in BIND form: seconds, or numbers with the units
// s, m, h, d and w (1h30m).
func parseTTL(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	if v, err := strconv.ParseUint(s, 10, 31); err == nil {
		return int(v), true
	}
	units := map[byte]uint64{'s': 1, 'm': 60, 'h': 3600, 'd': 86400, 'w': 604800}
	var total, num uint64
	digits := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			num = num*10 + uint64(c-'0')
			digits = true
		case units[c|0x20] > 0 && digits:
			total += num * units[c|0x20]
			num, digits = 0, false
		default:
			return 0, false
		}
		if num > math.MaxInt32 || total > math.MaxInt32 {
			return 0, false
		}
	}
	if digits {
		return 0, false
	}
	return int(total), true
}

// splitRR splits an entry of type typ as written into the tokens between
// owner and type (TTL and class) and the rdata after the type token; the
// owner is skipped when the entry has one.
func splitRR(text, typ string) (pre []string, rdata string) {
	s := text
	if s != "" && s[0] != ' ' && s[0] != '\t' {
		if i := strings.IndexAny(s, " \t"); i >= 0 {
			s = s[i:]
		} else {
			s = ""
		}
	}
	for {
		s = strings.TrimLeft(s, " \t\r")
		if s == "" {
			return pre, ""
		}
		tok, rest := s, ""
		if i := strings.IndexAny(s, " \t\r"); i >= 0 {
			tok, rest = s[:i], s[i:]
		}
		if strings.EqualFold(tok, typ) {
			return pre, strings.TrimSpace(rest)
		}
		pre = append(pre, tok)
		s = rest
	}
}

// relativeOwner returns the owner name abs relative to zone, "@" for the
// apex; ok is false when abs lies outside the zone.
func relativeOwner(zone, abs string) (string, bool) {
	r := relative(zone, abs)
	return r, r != abs
}

// relative returns name relative to zone when it lies in the zone, "@" for
// the apex, and name unchanged otherwise.
func relative(zone, name string) string {
	l := strings.ToLower(name)
	switch {
	case l == zone+".":
		return "@"
	case strings.HasSuffix(l, "."+zone+"."):
		return name[:len(name)-len(zone)-2]
	}
	return name
}

// relativeRdata returns the rdata of rr in presentation format with the
// names that lie in zone made relative.
func relativeRdata(zone string, rr dns.RR) string {
	switch r := rr.(type) {
	case *dns.CNAME:
		r.Target = relative(zone, r.Target)
	case *dns.MX:
		r.Mx = relative(zone, r.Mx)
	case *dns.NS:
		r.Ns = relative(zone, r.Ns)
	case *dns.PTR:
		r.Ptr = relative(zone, r.Ptr)
	case *dns.RP:
		r.Mbox = relative(zone, r.Mbox)
		r.Txt = relative(zone, r.Txt)
	case *dns.SRV:
		r.Target = relative(zone, r.Target)
	case *dns.SVCB:
		r.Target = relative(zone, r.Target)
	case *dns.HTTPS:
		r.Target = relative(zone, r.Target)
	case *dns.SOA:
		r.Ns = relative(zone, r.Ns)
		r.Mbox = relative(zone, r.Mbox)
	}
	return strings.TrimPrefix(rr.String(), rr.Header().String())
}

// FormatZonefile writes $ORIGIN, $TTL and one line per record
// ("<name>\tIN\t[<ttl>\t]<TYPE>\t<value>[ ; <comment>]"), SOA first, then
// NS, then by name and type; ParseZonefile reads it back unchanged.
func FormatZonefile(zone string, ttl int, sets []RRSet) string {
	sorted := append([]RRSet(nil), sets...)
	rank := func(s RRSet) int {
		switch s.Type {
		case "SOA":
			return 0
		case "NS":
			return 1
		}
		return 2
	}
	sortName := func(n string) string {
		if n == "@" {
			return ""
		}
		return n
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		if sortName(a.Name) != sortName(b.Name) {
			return sortName(a.Name) < sortName(b.Name)
		}
		return a.Type < b.Type
	})

	var b strings.Builder
	fmt.Fprintf(&b, "$ORIGIN\t%s.\n", zone)
	if ttl > 0 {
		fmt.Fprintf(&b, "$TTL\t%d\n", ttl)
	}
	for _, s := range sorted {
		col := ""
		if s.TTL != nil {
			col = strconv.Itoa(*s.TTL) + "\t"
		}
		for _, r := range s.Records {
			fmt.Fprintf(&b, "%s\tIN\t%s%s\t%s", s.Name, col, s.Type, r.Value)
			if r.Comment != "" {
				b.WriteString(" ; " + strings.NewReplacer("\r", " ", "\n", " ").Replace(r.Comment))
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}
