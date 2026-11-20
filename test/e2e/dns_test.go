//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	// nrptTag is the Comment of every NRPT rule the dev profile creates.
	nrptTag = "hlab-dev"
	// applianceDNS is the appliance address every hlab NRPT rule points at.
	applianceDNS = "198.19.0.1"
	// dnsWait is how long a lookup may take to give the expected answer.
	dnsWait = 2 * time.Minute
)

// zoneName is the run's zone for a row; it lies under .test, so it never
// shadows a public domain.
func zoneName(suffix string) string {
	return harness.runID + "-" + suffix + ".test"
}

// createZone creates a primary zone with rrsets, waits for its action and
// deletes it when the row ends unless the row deleted it already.
func (e *env) createZone(t *testing.T, name string, rrsets []map[string]any) int64 {
	t.Helper()
	var body struct {
		Zone struct {
			ID int64 `json:"id"`
		} `json:"zone"`
		Action *struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	req := map[string]any{"name": name, "mode": "primary"}
	if len(rrsets) > 0 {
		req["rrsets"] = rrsets
	}
	e.post(t, "/zones", req, &body)
	id := body.Zone.ID
	if id == 0 {
		t.Fatalf("POST /zones %s returned no zone id", name)
	}
	t.Cleanup(func() {
		if err := e.deleteZone(id); err != nil {
			t.Errorf("delete zone %s: %v", name, err)
		}
	})
	if body.Action == nil {
		t.Fatalf("POST /zones %s returned no action", name)
	}
	if err := e.waitAction(body.Action.ID, 5*time.Minute); err != nil {
		t.Fatalf("POST /zones %s: %v", name, err)
	}
	return id
}

// deleteZone deletes the zone and waits for its action; a zone already gone
// is no error.
func (e *env) deleteZone(id int64) error {
	path := fmt.Sprintf("/zones/%d", id)
	var body struct {
		Action *struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	status, aerr, err := e.apiDo(context.Background(), http.MethodDelete, path, nil, &body)
	switch {
	case err != nil:
		return fmt.Errorf("DELETE %s: %w", path, err)
	case status == http.StatusNotFound:
		return nil
	case status < 200 || status > 299:
		return fmt.Errorf("DELETE %s: status %d %s: %s", path, status, aerr.Code, aerr.Message)
	}
	if body.Action != nil {
		return e.waitAction(body.Action.ID, 5*time.Minute)
	}
	return nil
}

// hostLookup asks the host resolver (through NRPT) for the A records of a
// name or, for an IP address, its PTR names.
func hostLookup(query string, ptr bool) ([]string, error) {
	q := strings.ReplaceAll(query, "'", "''")
	script := "ConvertTo-Json -Compress -InputObject @(Resolve-DnsName -Name '" + q + "' -Type A -DnsOnly -ErrorAction Stop |" +
		" ForEach-Object { $_.IP4Address } | Where-Object { $_ })"
	if ptr {
		script = "ConvertTo-Json -Compress -InputObject @(Resolve-DnsName -Name '" + q + "' -Type PTR -DnsOnly -ErrorAction Stop |" +
			" ForEach-Object { $_.NameHost } | Where-Object { $_ })"
	}
	var got []string
	err := powershellJSON(context.Background(), script, &got)
	return got, err
}

// getentFields parses `getent -s dns hosts` output: the address followed by
// the names. Only DNS is asked, so /etc/hosts cannot answer.
func getentFields(r result) []string {
	return strings.Fields(strings.ToLower(strings.TrimSpace(r.Stdout)))
}

// getentMatches reports whether getent output answers the query: for a name
// the address is want, for an address one of the names is want.
func getentMatches(fields []string, want string, ptr bool) bool {
	if len(fields) < 2 {
		return false
	}
	if ptr {
		return slices.Contains(fields[1:], strings.TrimSuffix(want, "."))
	}
	return fields[0] == want
}

// resolvesEverywhere checks that query resolves to want from the host, from
// the WSL2 distro and from the server, each within dnsWait. With ptr the
// query is an IPv4 address and want its PTR name.
func resolvesEverywhere(t *testing.T, e *env, srv *server, w *wslEnv, query, want string, ptr bool) {
	t.Helper()
	var hostGot []string
	var hostErr error
	if err := waitFor(dnsWait, 3*time.Second, func() (bool, error) {
		hostGot, hostErr = hostLookup(query, ptr)
		if hostErr != nil {
			return false, nil
		}
		for _, g := range hostGot {
			if strings.TrimSuffix(strings.ToLower(g), ".") == want {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		t.Errorf("host: Resolve-DnsName %s gave %v (%v), want %s: %v", query, hostGot, hostErr, want, err)
	}

	cmd := "getent -s dns hosts " + query
	var wslLast result
	if err := waitFor(dnsWait, 3*time.Second, func() (bool, error) {
		r, err := w.run(context.Background(), time.Minute, nil, cmd+"\n")
		wslLast = r
		return err == nil && getentMatches(getentFields(r), want, ptr), nil
	}); err != nil {
		t.Errorf("WSL2 %s: %s did not give %s: %v\n%s", w.distro, cmd, want, err, wslLast)
	}

	var srvLast result
	if err := waitFor(dnsWait, 3*time.Second, func() (bool, error) {
		r, err := e.ssh(srv, time.Minute, "", cmd)
		srvLast = r
		return err == nil && getentMatches(getentFields(r), want, ptr), nil
	}); err != nil {
		t.Errorf("server %s: %s did not give %s: %v\n%s", srv.Name, cmd, want, err, srvLast)
	}
}

// testDNS02: an A record in a new zone resolves from the host, from WSL2
// and from a server.
func testDNS02(t *testing.T) {
	e, srv := sharedServer(t)
	_, w := sharedWSL(t)
	const addr = "198.51.100.7"
	zone := zoneName("dns02")
	e.createZone(t, zone, []map[string]any{{
		"name": "www", "type": "A", "records": []map[string]any{{"value": addr}},
	}})
	resolvesEverywhere(t, e, srv, w, "www."+zone, addr, false)
}

// nrptRule is an NRPT rule with the profile's tag.
type nrptRule struct {
	Namespace   []string `json:"Namespace"`
	NameServers []string `json:"NameServers"`
}

// taggedNRPTRules reads the NRPT rules whose Comment is the profile's tag
// with Get-DnsClientNrptRule (its NameServers are IPAddress objects, turned
// into strings here); where that is refused it reads the same rules
// from the registry (decision D16). It never changes a rule.
func taggedNRPTRules() ([]nrptRule, error) {
	script := `$tag = '` + nrptTag + `'
try {
  $rules = @(Get-DnsClientNrptRule | Where-Object { $_.Comment -eq $tag } |
    ForEach-Object { [pscustomobject]@{ Namespace = @($_.Namespace | ForEach-Object { [string]$_ }); NameServers = @($_.NameServers | ForEach-Object { [string]$_ }) } })
} catch {
  $key = 'HKLM:\SYSTEM\CurrentControlSet\Services\Dnscache\Parameters\DnsPolicyConfig'
  $rules = @(Get-ChildItem -Path $key | ForEach-Object { Get-ItemProperty -Path $_.PSPath } |
    Where-Object { $_.Comment -eq $tag } |
    ForEach-Object { [pscustomobject]@{ Namespace = @($_.Name); NameServers = @(([string]$_.GenericDNSServers) -split ';' | Where-Object { $_ }) } })
}
ConvertTo-Json -Compress -Depth 3 -InputObject $rules`
	var rules []nrptRule
	err := powershellJSON(context.Background(), script, &rules)
	return rules, err
}

// rulesFor returns the tagged rules whose namespace list holds ns.
func rulesFor(rules []nrptRule, ns string) []nrptRule {
	var out []nrptRule
	for _, r := range rules {
		for _, n := range r.Namespace {
			if strings.EqualFold(n, ns) {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// testDNS03: after creating a zone exactly one NRPT rule with the profile's
// tag has the zone's namespace and points at the appliance; after deleting
// the zone none has it.
func testDNS03(t *testing.T) {
	e := sharedEnv(t)
	zone := zoneName("dns03")
	ns := "." + zone
	id := e.createZone(t, zone, nil)

	rules, err := taggedNRPTRules()
	if err != nil {
		t.Fatalf("read NRPT rules: %v", err)
	}
	got := rulesFor(rules, ns)
	if len(got) != 1 {
		t.Fatalf("%d NRPT rules tagged %s have namespace %s after creating the zone, want 1: %+v", len(got), nrptTag, ns, got)
	}
	if !slices.Equal(got[0].NameServers, []string{applianceDNS}) {
		t.Errorf("NRPT rule for %s points at %v, want [%s]", ns, got[0].NameServers, applianceDNS)
	}

	if err := e.deleteZone(id); err != nil {
		t.Fatalf("delete zone %s: %v", zone, err)
	}
	rules, err = taggedNRPTRules()
	if err != nil {
		t.Fatalf("read NRPT rules: %v", err)
	}
	if got := rulesFor(rules, ns); len(got) != 0 {
		t.Errorf("%d NRPT rules tagged %s still have namespace %s after deleting the zone: %+v", len(got), nrptTag, ns, got)
	}
}

// testRDNS02: a PTR set on the shared server's IPv4 resolves from the host,
// from WSL2 and from the server.
func testRDNS02(t *testing.T) {
	e, srv := sharedServer(t)
	_, w := sharedWSL(t)
	ptr := "rdns02." + harness.runID + ".example.com"
	path := fmt.Sprintf("/servers/%d/actions/change_dns_ptr", srv.ID)

	// The shared server gets its default PTR back when the row ends.
	// Other rows act on the shared server too, so both posts retry while it
	// is locked, like a real client.
	t.Cleanup(func() {
		if err := e.postAction(path, map[string]any{"ip": srv.IPv4, "dns_ptr": nil}, 5*time.Minute); err != nil {
			t.Errorf("reset PTR of %s: %v", srv.IPv4, err)
		}
	})
	e.runLockedAction(t, path, map[string]any{"ip": srv.IPv4, "dns_ptr": ptr})

	resolvesEverywhere(t, e, srv, w, srv.IPv4, ptr, true)
}
