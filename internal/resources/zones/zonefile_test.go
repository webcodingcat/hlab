package zones_test

import (
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
)

// zonefileFor returns a zone file for zone with $TTL ttl, the SOA, the
// Hetzner NS and the extra lines.
func zonefileFor(zone string, ttl int, extra ...string) string {
	lines := []string{
		"$ORIGIN " + zone + ".",
		fmt.Sprintf("$TTL %d", ttl),
		"@\tIN\tSOA\thydrogen.ns.hetzner.com. dns.hetzner.com. 2024010100 86400 10800 3600000 3600",
		"@\tIN\t10800\tNS\thydrogen.ns.hetzner.com.",
		"@\tIN\t10800\tNS\toxygen.ns.hetzner.com.",
		"@\tIN\t10800\tNS\thelium.ns.hetzner.de.",
	}
	return strings.Join(append(lines, extra...), "\n") + "\n"
}

// exportZone returns the zone file of zone id.
func exportZone(t *testing.T, e *zoneEnv, id int64) string {
	t.Helper()
	resp := e.Do(http.MethodGet, zonePath(id)+"/zonefile", nil)
	if resp.StatusCode != http.StatusOK {
		wantError(t, resp, http.StatusOK, "", "GET zonefile")
		t.FailNow()
	}
	var b struct {
		Zonefile string `json:"zonefile"`
	}
	apitest.Decode(t, resp, &b)
	return b.Zonefile
}

// rrsetState returns the RRSets of zone id by ID as "ttl values"; the
// SOA only with its TTL, since hlab manages its serial.
func rrsetState(t *testing.T, e *zoneEnv, id int64) map[string]string {
	t.Helper()
	state := map[string]string{}
	for _, r := range listRRSets(t, e, id, "") {
		ttl := "zone"
		if r.TTL != nil {
			ttl = fmt.Sprint(*r.TTL)
		}
		if r.Type == "SOA" {
			state[r.ID] = ttl
			continue
		}
		state[r.ID] = ttl + " " + strings.Join(r.values(), " | ")
	}
	return state
}

// normalized returns the lines of text with runs of white space made one
// space.
func normalized(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if f := strings.Fields(l); len(f) > 0 {
			out = append(out, strings.Join(f, " "))
		}
	}
	return out
}

func testZonefile(t *testing.T) {
	e := newZoneEnv(t)
	z := createZone(t, e, map[string]any{
		"name": "example.com", "mode": "primary", "ttl": 3600,
		"rrsets": []map[string]any{
			{"name": "www", "type": "A", "ttl": 300, "records": records("198.51.100.10")},
			{"name": "mail", "type": "A", "records": records("198.51.100.25")},
			{"name": "@", "type": "MX", "records": records("10 mail")},
			{"name": "txt", "type": "TXT", "records": records(`"v=spf1 -all"`)},
		},
	})

	lines := normalized(exportZone(t, e, z.ID))
	for _, want := range []string{"$ORIGIN example.com.", "$TTL 3600", "www IN 300 A 198.51.100.10", "mail IN A 198.51.100.25"} {
		if !slices.Contains(lines, want) {
			t.Errorf("zone file %q has no line %q", lines, want)
		}
	}
	if !slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, "@ IN SOA ") }) {
		t.Errorf("zone file %q has no apex SOA line", lines)
	}

	runAction(t, e, zonePath(z.ID)+"/actions/import_zonefile",
		map[string]any{"zonefile": zonefileFor("example.com", 7200, "imported\tIN\tA\t198.51.100.40 ; a comment")}, "import_zonefile")
	ids := slices.Sorted(maps.Keys(rrsetState(t, e, z.ID)))
	if want := []string{"@/NS", "@/SOA", "imported/A"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("RRSets after import %v, want %v", ids, want)
	}
	if got := mustZone(t, e, z.ID); got.TTL != 7200 {
		t.Errorf("zone ttl after import %d, want 7200 from $TTL", got.TTL)
	}

	before := rrsetState(t, e, z.ID)
	noSOA := "$ORIGIN example.com.\n$TTL 600\n@\tIN\tNS\thydrogen.ns.hetzner.com.\n@\tIN\tNS\toxygen.ns.hetzner.com.\n@\tIN\tNS\thelium.ns.hetzner.de.\nother\tIN\tA\t198.51.100.41\n"
	wantFieldError(t, e.Do(http.MethodPost, zonePath(z.ID)+"/actions/import_zonefile", map[string]any{"zonefile": noSOA}),
		"zonefile", "import of a zone file without SOA")
	if after := rrsetState(t, e, z.ID); !reflect.DeepEqual(after, before) {
		t.Errorf("RRSets after refused import %v, want %v", after, before)
	}
	if got := mustZone(t, e, z.ID); got.TTL != 7200 {
		t.Errorf("zone ttl after refused import %d, want 7200", got.TTL)
	}

	imported := rrsetPath(z.ID, "imported", "A")
	runAction(t, e, imported+"/actions/change_protection", map[string]any{"change": true}, "change_rrset_protection")
	wantError(t, e.Do(http.MethodPost, zonePath(z.ID)+"/actions/import_zonefile",
		map[string]any{"zonefile": zonefileFor("example.com", 3600)}),
		http.StatusLocked, "protected", "import over a change-protected RRSet")
	if _, ok := getRRSet(t, e, z.ID, "imported", "A"); !ok {
		t.Errorf("protected RRSet gone after refused import")
	}

	org := createZone(t, e, map[string]any{
		"name": "example.org", "mode": "primary",
		"zonefile": zonefileFor("example.org", 3600, "www\tIN\tA\t198.51.100.50"),
	})
	if got := mustRRSet(t, e, org.ID, "www", "A").values(); !reflect.DeepEqual(got, []string{"198.51.100.50"}) {
		t.Errorf("www/A of a zone created from a zone file %v, want [198.51.100.50]", got)
	}
	wantFieldError(t, postZone(e, map[string]any{
		"name": "example.net", "mode": "primary",
		"zonefile": zonefileFor("example.net", 3600),
		"rrsets":   []map[string]any{{"name": "www", "type": "A", "records": records("198.51.100.60")}},
	}), "zonefile", "create with zonefile and rrsets")

	rt := createZone(t, e, map[string]any{
		"name": "example.net", "mode": "primary", "ttl": 1800,
		"rrsets": []map[string]any{
			{"name": "www", "type": "A", "ttl": 300, "records": records("198.51.100.70", "198.51.100.71")},
			{"name": "v6", "type": "AAAA", "records": records("2001:db8::70")},
			{"name": "alias", "type": "CNAME", "records": records("www")},
			{"name": "@", "type": "MX", "records": records("10 www", "20 backup.example.org.")},
			{"name": "@", "type": "TXT", "records": records(`"v=spf1 -all"`, `"hello " "world"`)},
			{"name": "@", "type": "CAA", "records": records(`0 issue "letsencrypt.org"`)},
			{"name": "_sip._tcp", "type": "SRV", "records": records("10 5 5060 www")},
		},
	})
	want := rrsetState(t, e, rt.ID)
	text := exportZone(t, e, rt.ID)
	waitSuccess(t, e, startAction(t, e, http.MethodDelete, zonePath(rt.ID), nil, http.StatusCreated), "delete example.net")
	again := createZone(t, e, map[string]any{"name": "example.net", "mode": "primary"})
	runAction(t, e, zonePath(again.ID)+"/actions/import_zonefile", map[string]any{"zonefile": text}, "import_zonefile")
	if got := rrsetState(t, e, again.ID); !reflect.DeepEqual(got, want) {
		t.Errorf("RRSets after export, delete and import\n%v\nwant\n%v", got, want)
	}
}
