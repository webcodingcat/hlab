//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func init() {
	registerRow(clientRow{Req: "CLI-01", Name: "zones", Run: testCLI01Zones})
	registerRow(clientRow{Req: "CLI-02", Name: "zones", Run: testCLI02Zones})
	registerRow(clientRow{Req: "CLI-03", Name: "zones", Run: testCLI03Zones})
}

type nameserver struct {
	Address string `json:"address"`
	Port    int    `json:"port,omitempty"`
}

// zoneDetail is what the zone rows check of GET /zones/{id}.
type zoneDetail struct {
	ID         int64             `json:"id"`
	Name       string            `json:"name"`
	Mode       string            `json:"mode"`
	TTL        int               `json:"ttl"`
	Labels     map[string]string `json:"labels"`
	Protection struct {
		Delete bool `json:"delete"`
	} `json:"protection"`
	PrimaryNameservers []nameserver `json:"primary_nameservers"`
}

// rrsetDetail is what the zone rows check of an RRSet.
type rrsetDetail struct {
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	TTL        *int              `json:"ttl"`
	Labels     map[string]string `json:"labels"`
	Protection struct {
		Change bool `json:"change"`
	} `json:"protection"`
	Records []struct {
		Value   string  `json:"value"`
		Comment *string `json:"comment"`
	} `json:"records"`
}

// values are the sorted record values of the RRSet.
func (r rrsetDetail) values() []string {
	var out []string
	for _, rec := range r.Records {
		out = append(out, rec.Value)
	}
	slices.Sort(out)
	return out
}

func (e *env) zoneInfo(t *testing.T, id int64) zoneDetail {
	t.Helper()
	var body struct {
		Zone zoneDetail `json:"zone"`
	}
	e.getOK(t, fmt.Sprintf("/zones/%d", id), &body)
	return body.Zone
}

// rrsetInfo is GET /zones/{id}/rrsets/{name}/{type}; ok is false on 404.
func (e *env) rrsetInfo(t *testing.T, zone int64, name, typ string) (rrsetDetail, bool) {
	t.Helper()
	var body struct {
		RRSet rrsetDetail `json:"rrset"`
	}
	path := fmt.Sprintf("/zones/%d/rrsets/%s/%s", zone, url.PathEscape(name), typ)
	status, aerr, err := e.apiDo(context.Background(), http.MethodGet, path, nil, &body)
	switch {
	case err != nil:
		t.Fatalf("GET %s: %v", path, err)
	case status == http.StatusNotFound:
		return rrsetDetail{}, false
	case status != http.StatusOK:
		t.Fatalf("GET %s: status %d %s: %s", path, status, aerr.Code, aerr.Message)
	}
	return body.RRSet, true
}

// wantValues fails unless the RRSet exists with exactly the values.
func wantValues(t *testing.T, e *env, zone int64, name, typ, step string, want ...string) rrsetDetail {
	t.Helper()
	rr, ok := e.rrsetInfo(t, zone, name, typ)
	slices.Sort(want)
	if !ok || !slices.Equal(rr.values(), want) {
		t.Errorf("%s: RRSet %s/%s exists %t with %v, want %v", step, name, typ, ok, rr.values(), want)
	}
	return rr
}

// testCLI01Zones: hcloud manages a zone and its RRSets through every zone
// and zone rrset command, including zone file import and export.
func testCLI01Zones(t *testing.T, c *rowCtx) {
	e := c.E
	zone, secondary := zoneName("hcloud"), zoneName("hcloud-secondary")
	t.Cleanup(func() {
		for _, z := range []string{zone, secondary} {
			_, _ = e.hcloudRun(context.Background(), 2*time.Minute, "zone", "disable-protection", z, "delete")
			_, _ = e.hcloudRun(context.Background(), 5*time.Minute, "zone", "delete", z)
		}
	})
	dir := t.TempDir()
	writeJSON := func(name string, v any) string {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(dir, name)
		if err := os.WriteFile(f, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return f
	}

	// zone create, list, describe
	e.hcloudOK(t, "zone", "create", "--name", zone, "--ttl", "3600", "--label", "row=cli-01-zones")
	id := e.zoneByName(t, zone)
	if z := e.zoneInfo(t, id); z.Mode != "primary" || z.TTL != 3600 || !maps.Equal(z.Labels, map[string]string{"row": "cli-01-zones"}) {
		t.Errorf("created zone %+v, want primary, ttl 3600, label row=cli-01-zones", z)
	}
	first := []nameserver{{Address: "192.0.2.53"}}
	e.hcloudOK(t, "zone", "create", "--name", secondary, "--mode", "secondary", "--label", "row=cli-01-zones",
		"--primary-nameservers-file", writeJSON("ns1.json", first))
	secID := e.zoneByName(t, secondary)
	if z := e.zoneInfo(t, secID); z.Mode != "secondary" || len(z.PrimaryNameservers) != 1 || z.PrimaryNameservers[0].Address != first[0].Address {
		t.Errorf("created secondary zone %+v, want primary nameserver %s", z, first[0].Address)
	}
	var listed []named
	e.hcloudJSON(t, &listed, "zone", "list", "-l", "row=cli-01-zones")
	if got, want := namedOf(listed), map[int64]string{id: zone, secID: secondary}; !maps.Equal(got, want) {
		t.Errorf("hcloud zone list -l row=cli-01-zones: %v, want %v", got, want)
	}
	var described zoneDetail
	e.hcloudJSON(t, &described, "zone", "describe", zone)
	if described.ID != id || described.TTL != 3600 {
		t.Errorf("hcloud zone describe %s: %+v", zone, described)
	}

	// change-primary-nameservers of the secondary zone
	second := []nameserver{{Address: "192.0.2.54", Port: 5353}}
	e.hcloudOK(t, "zone", "change-primary-nameservers", "--primary-nameservers-file", writeJSON("ns2.json", second), secondary)
	if z := e.zoneInfo(t, secID); len(z.PrimaryNameservers) != 1 || z.PrimaryNameservers[0] != second[0] {
		t.Errorf("primary nameservers after the change %+v, want %+v", z.PrimaryNameservers, second)
	}

	// zone change-ttl, labels, protection
	e.hcloudOK(t, "zone", "change-ttl", "--ttl", "7200", zone)
	e.hcloudOK(t, "zone", "add-label", zone, "stage=two")
	e.hcloudOK(t, "zone", "remove-label", zone, "row")
	if z := e.zoneInfo(t, id); z.TTL != 7200 || !maps.Equal(z.Labels, map[string]string{"stage": "two"}) {
		t.Errorf("zone after change-ttl and labels: ttl %d, labels %v; want 7200, stage=two", z.TTL, z.Labels)
	}
	e.hcloudOK(t, "zone", "enable-protection", zone, "delete")
	if !e.zoneInfo(t, id).Protection.Delete {
		t.Error("zone delete protection off after enable-protection")
	}
	e.hcloudOK(t, "zone", "disable-protection", zone, "delete")
	if e.zoneInfo(t, id).Protection.Delete {
		t.Error("zone delete protection on after disable-protection")
	}

	// zone add-records, set-records, remove-records
	e.hcloudOK(t, "zone", "add-records", "--record", "192.0.2.1", "--record", "192.0.2.2", zone, "www", "A")
	wantValues(t, e, id, "www", "A", "zone add-records", "192.0.2.1", "192.0.2.2")
	e.hcloudOK(t, "zone", "set-records", "--record", "192.0.2.3", zone, "www", "A")
	wantValues(t, e, id, "www", "A", "zone set-records", "192.0.2.3")
	e.hcloudOK(t, "zone", "add-records", "--record", "192.0.2.4", zone, "www", "A")
	e.hcloudOK(t, "zone", "remove-records", "--record", "192.0.2.3", zone, "www", "A")
	wantValues(t, e, id, "www", "A", "zone remove-records", "192.0.2.4")

	// rrset create, list, describe
	e.hcloudOK(t, "zone", "rrset", "create", "--name", "api", "--type", "A", "--record", "192.0.2.10", "--ttl", "600",
		"--label", "row=cli-01-zones", zone)
	rr := wantValues(t, e, id, "api", "A", "rrset create", "192.0.2.10")
	if rr.TTL == nil || *rr.TTL != 600 || !maps.Equal(rr.Labels, map[string]string{"row": "cli-01-zones"}) {
		t.Errorf("created RRSet ttl %v, labels %v; want 600, row=cli-01-zones", rr.TTL, rr.Labels)
	}
	var rrsets []rrsetDetail
	e.hcloudJSON(t, &rrsets, "zone", "rrset", "list", zone)
	if !slices.ContainsFunc(rrsets, func(r rrsetDetail) bool { return r.Name == "api" && r.Type == "A" }) ||
		!slices.ContainsFunc(rrsets, func(r rrsetDetail) bool { return r.Name == "www" && r.Type == "A" }) {
		t.Errorf("hcloud zone rrset list %s: %+v, want api/A and www/A", zone, rrsets)
	}
	var rrDescribed rrsetDetail
	e.hcloudJSON(t, &rrDescribed, "zone", "rrset", "describe", zone, "api", "A")
	if !slices.Equal(rrDescribed.values(), []string{"192.0.2.10"}) {
		t.Errorf("hcloud zone rrset describe %s api A: %+v", zone, rrDescribed)
	}

	// rrset change-ttl
	e.hcloudOK(t, "zone", "rrset", "change-ttl", "--ttl", "900", zone, "api", "A")
	if rr, _ := e.rrsetInfo(t, id, "api", "A"); rr.TTL == nil || *rr.TTL != 900 {
		t.Errorf("RRSet ttl after change-ttl %v, want 900", rr.TTL)
	}
	e.hcloudOK(t, "zone", "rrset", "change-ttl", "--unset", zone, "api", "A")
	if rr, _ := e.rrsetInfo(t, id, "api", "A"); rr.TTL != nil {
		t.Errorf("RRSet ttl after change-ttl --unset %d, want none", *rr.TTL)
	}

	// rrset add-records, set-records, remove-records
	e.hcloudOK(t, "zone", "rrset", "add-records", "--record", "192.0.2.11", zone, "api", "A")
	wantValues(t, e, id, "api", "A", "rrset add-records", "192.0.2.10", "192.0.2.11")
	e.hcloudOK(t, "zone", "rrset", "set-records", "--record", "192.0.2.12", zone, "api", "A")
	wantValues(t, e, id, "api", "A", "rrset set-records", "192.0.2.12")
	e.hcloudOK(t, "zone", "rrset", "add-records", "--record", "192.0.2.13", zone, "api", "A")
	e.hcloudOK(t, "zone", "rrset", "remove-records", "--record", "192.0.2.12", zone, "api", "A")
	wantValues(t, e, id, "api", "A", "rrset remove-records", "192.0.2.13")

	// rrset labels, protection, delete
	e.hcloudOK(t, "zone", "rrset", "add-label", zone, "api", "A", "stage=two")
	e.hcloudOK(t, "zone", "rrset", "remove-label", zone, "api", "A", "row")
	if rr, _ := e.rrsetInfo(t, id, "api", "A"); !maps.Equal(rr.Labels, map[string]string{"stage": "two"}) {
		t.Errorf("RRSet labels %v, want stage=two", rr.Labels)
	}
	e.hcloudOK(t, "zone", "rrset", "enable-protection", zone, "api", "A", "change")
	if rr, _ := e.rrsetInfo(t, id, "api", "A"); !rr.Protection.Change {
		t.Error("RRSet change protection off after enable-protection")
	}
	e.hcloudOK(t, "zone", "rrset", "disable-protection", zone, "api", "A", "change")
	if rr, _ := e.rrsetInfo(t, id, "api", "A"); rr.Protection.Change {
		t.Error("RRSet change protection on after disable-protection")
	}
	e.hcloudOK(t, "zone", "rrset", "delete", zone, "api", "A")
	if _, ok := e.rrsetInfo(t, id, "api", "A"); ok {
		t.Error("RRSet api/A still there after rrset delete")
	}

	// export-zonefile and import-zonefile
	exported := e.hcloudOK(t, "zone", "export-zonefile", zone).Stdout
	if !strings.Contains(exported, "192.0.2.4") {
		t.Errorf("exported zone file has no record 192.0.2.4:\n%s", exported)
	}
	zonefile := filepath.Join(dir, "zone.txt")
	content := strings.TrimRight(exported, "\n") + "\nimported." + zone + ". 300 IN A 192.0.2.50\n"
	if err := os.WriteFile(zonefile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	e.hcloudOK(t, "zone", "import-zonefile", "--zonefile", zonefile, zone)
	wantValues(t, e, id, "imported", "A", "import-zonefile", "192.0.2.50")
	wantValues(t, e, id, "www", "A", "import-zonefile", "192.0.2.4")
	if again := e.hcloudOK(t, "zone", "export-zonefile", zone).Stdout; !strings.Contains(again, "192.0.2.50") {
		t.Errorf("zone file exported after the import has no record 192.0.2.50:\n%s", again)
	}

	// delete
	e.hcloudOK(t, "zone", "delete", zone, secondary)
	assertGoneIDs(t, e, "/zones", "zones", id, secID)
}

// testCLI02Zones: OpenTofu manages hcloud_zone, hcloud_zone_rrset and
// hcloud_zone_record, updates them in place, reads hcloud_zone(s) and
// hcloud_zone_rrset(s), and destroys them.
func testCLI02Zones(t *testing.T, _ *rowCtx) {
	e := sharedTofu(t)
	d := e.newTofuDir(t, "zones")
	zone := zoneName("tofu")
	type version struct {
		ttl, rrTTL     int
		stage, www, cm string
	}
	main := func(v version) string {
		return fmt.Sprintf(`resource "hcloud_zone" "z" {
  name = %[1]q
  mode = "primary"
  ttl  = %[2]d
  labels = {
    row   = "cli-02-zones"
    stage = %[3]q
  }
}

resource "hcloud_zone_rrset" "www" {
  zone    = hcloud_zone.z.name
  name    = "www"
  type    = "A"
  ttl     = %[4]d
  records = [{ value = %[5]q }]
  labels = {
    stage = %[3]q
  }
}

resource "hcloud_zone_record" "api" {
  zone    = hcloud_zone.z.name
  name    = "api"
  type    = "A"
  value   = "192.0.2.10"
  comment = %[6]q
}

data "hcloud_zone" "by_name" {
  name       = hcloud_zone.z.name
  depends_on = [hcloud_zone.z]
}

data "hcloud_zones" "by_label" {
  with_selector = "row=cli-02-zones"
  depends_on    = [hcloud_zone.z]
}

data "hcloud_zone_rrset" "www" {
  zone       = hcloud_zone.z.name
  name       = "www"
  type       = "A"
  depends_on = [hcloud_zone_rrset.www]
}

data "hcloud_zone_rrsets" "all" {
  zone       = hcloud_zone.z.name
  depends_on = [hcloud_zone_rrset.www, hcloud_zone_record.api]
}

output "id" {
  value = hcloud_zone.z.id
}

output "ds_ttl" {
  value = data.hcloud_zone.by_name.ttl
}

output "ds_names" {
  value = jsonencode(data.hcloud_zones.by_label.zones[*].name)
}

output "ds_www" {
  value = jsonencode(sort([for r in data.hcloud_zone_rrset.www.records : r.value]))
}

output "ds_rrsets" {
  value = jsonencode(sort([for r in data.hcloud_zone_rrsets.all.rrsets : "${r.name}/${r.type}"]))
}
`, zone, v.ttl, v.stage, v.rrTTL, v.www, v.cm)
	}
	check := func(v version) int64 {
		t.Helper()
		id := parseID(t, d.output(t, "id"))
		if z := e.zoneInfo(t, id); z.TTL != v.ttl || !maps.Equal(z.Labels, map[string]string{"row": "cli-02-zones", "stage": v.stage}) {
			t.Errorf("zone ttl %d, labels %v; want %d, stage %s", z.TTL, z.Labels, v.ttl, v.stage)
		}
		rr := wantValues(t, e, id, "www", "A", "hcloud_zone_rrset", v.www)
		if rr.TTL == nil || *rr.TTL != v.rrTTL || !maps.Equal(rr.Labels, map[string]string{"stage": v.stage}) {
			t.Errorf("RRSet www/A ttl %v, labels %v; want %d, stage %s", rr.TTL, rr.Labels, v.rrTTL, v.stage)
		}
		api := wantValues(t, e, id, "api", "A", "hcloud_zone_record", "192.0.2.10")
		if len(api.Records) != 1 || api.Records[0].Comment == nil || *api.Records[0].Comment != v.cm {
			t.Errorf("record api/A %+v, want comment %q", api.Records, v.cm)
		}
		if got := d.output(t, "ds_ttl"); got != fmt.Sprint(v.ttl) {
			t.Errorf("data hcloud_zone: ttl %s, want %d", got, v.ttl)
		}
		if got := d.output(t, "ds_names"); got != `["`+zone+`"]` {
			t.Errorf("data hcloud_zones with row=cli-02-zones: %s, want [%q]", got, zone)
		}
		if got := d.output(t, "ds_www"); got != `["`+v.www+`"]` {
			t.Errorf("data hcloud_zone_rrset www/A: %s, want [%q]", got, v.www)
		}
		if got := d.output(t, "ds_rrsets"); !strings.Contains(got, `"api/A"`) || !strings.Contains(got, `"www/A"`) {
			t.Errorf("data hcloud_zone_rrsets: %s, want api/A and www/A among them", got)
		}
		return id
	}
	d.run(t, "init", "-no-color")

	v1 := version{ttl: 3600, rrTTL: 600, stage: "one", www: "192.0.2.1", cm: "one"}
	d.writeMain(t, main(v1))
	d.run(t, "apply", "-auto-approve", "-no-color")
	id := check(v1)
	t.Cleanup(func() { _ = e.deleteZone(id) })

	v2 := version{ttl: 7200, rrTTL: 900, stage: "two", www: "192.0.2.2", cm: "two"}
	d.writeMain(t, main(v2))
	r := d.run(t, "apply", "-auto-approve", "-no-color")
	if want := "0 added, 3 changed, 0 destroyed"; !strings.Contains(r.Stdout, want) {
		t.Errorf("second apply is no in-place update of zone, RRSet and record (want %q):\n%s", want, r.Stdout)
	}
	if id2 := check(v2); id2 != id {
		t.Errorf("zone ID changed from %d to %d; want an in-place update", id, id2)
	}

	d.run(t, "destroy", "-auto-approve", "-no-color")
	assertGoneIDs(t, e, "/zones", "zones", id)
}

// testCLI03Zones: zone and zone_rrset create, update and delete; zone_info
// and zone_rrset_info find them.
func testCLI03Zones(t *testing.T, _ *rowCtx) {
	e, w := sharedAnsible(t)
	zone := zoneName("ansible")
	t.Cleanup(func() { _, _ = e.hcloudRun(context.Background(), 5*time.Minute, "zone", "delete", zone) })
	row := map[string]any{"zone": zone}
	present := func(ttl, rrTTL int, stage, value string) string {
		return fmt.Sprintf(`    - name: zone
      hetzner.hcloud.zone:
        name: "{{ row.zone }}"
        mode: primary
        ttl: %d
        labels:
          row: cli-03-zones
          stage: %s
        state: present
    - name: rrset
      hetzner.hcloud.zone_rrset:
        zone: "{{ row.zone }}"
        name: www
        type: A
        ttl: %d
        records:
          - value: %s
        labels:
          stage: %s
        state: present
`, ttl, stage, rrTTL, value, stage)
	}
	check := func(id int64, ttl, rrTTL int, stage, value string) {
		t.Helper()
		if z := e.zoneInfo(t, id); z.TTL != ttl || !maps.Equal(z.Labels, map[string]string{"row": "cli-03-zones", "stage": stage}) {
			t.Errorf("zone ttl %d, labels %v; want %d, stage %s", z.TTL, z.Labels, ttl, stage)
		}
		rr := wantValues(t, e, id, "www", "A", "zone_rrset", value)
		if rr.TTL == nil || *rr.TTL != rrTTL || !maps.Equal(rr.Labels, map[string]string{"stage": stage}) {
			t.Errorf("RRSet www/A ttl %v, labels %v; want %d, stage %s", rr.TTL, rr.Labels, rrTTL, stage)
		}
	}

	e.ansibleOK(t, w, "cli03-zones-create", localPlay(t, row, present(3600, 600, "one", "192.0.2.1")))
	id := e.zoneByName(t, zone)
	check(id, 3600, 600, "one", "192.0.2.1")

	row["id"] = id
	e.ansibleOK(t, w, "cli03-zones-update", localPlay(t, row, present(7200, 900, "two", "192.0.2.2")+`    - name: zone by name
      hetzner.hcloud.zone_info:
        name: "{{ row.zone }}"
      register: by_name
    - name: zone by label selector
      hetzner.hcloud.zone_info:
        label_selector: row=cli-03-zones
      register: by_label
    - name: rrset www/A
      hetzner.hcloud.zone_rrset_info:
        zone: "{{ row.zone }}"
        name: www
        type: A
      register: rrset
    - name: check the answers
      ansible.builtin.assert:
        that:
          - by_name.hcloud_zone_info | length == 1
          - by_name.hcloud_zone_info[0].id | int == row.id
          - by_name.hcloud_zone_info[0].ttl | int == 7200
          - by_label.hcloud_zone_info | map(attribute='name') | list == [row.zone]
          - rrset.hcloud_zone_rrset_info | length == 1
          - rrset.hcloud_zone_rrset_info[0].ttl | int == 900
          - rrset.hcloud_zone_rrset_info[0].records | map(attribute='value') | list == ["192.0.2.2"]
`))
	check(id, 7200, 900, "two", "192.0.2.2")

	e.ansibleOK(t, w, "cli03-zones-absent", localPlay(t, row, `    - name: rrset absent
      hetzner.hcloud.zone_rrset:
        zone: "{{ row.zone }}"
        name: www
        type: A
        state: absent
    - name: zone absent
      hetzner.hcloud.zone:
        name: "{{ row.zone }}"
        state: absent
`))
	assertGoneIDs(t, e, "/zones", "zones", id)
}
