package zones_test

import (
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/apitest"
)

func records(values ...string) []map[string]any {
	var r []map[string]any
	for _, v := range values {
		r = append(r, map[string]any{"value": v})
	}
	return r
}

func testRRSets(t *testing.T) {
	e := newZoneEnv(t)
	z := createZone(t, e, map[string]any{"name": "example.com", "mode": "primary", "ttl": 3600})
	www := rrsetPath(z.ID, "www", "A")
	create := map[string]any{"name": "www", "type": "A", "records": []map[string]any{{"value": "198.51.100.10", "comment": "web"}}}

	resp := e.Do(http.MethodPost, zonePath(z.ID)+"/rrsets", create)
	if resp.StatusCode != http.StatusCreated {
		wantError(t, resp, http.StatusCreated, "", "POST rrsets")
		t.FailNow()
	}
	var created struct {
		RRSet  rrsetView  `json:"rrset"`
		Action actionView `json:"action"`
	}
	apitest.Decode(t, resp, &created)
	if created.RRSet.ID != "www/A" || created.Action.Command != "create_rrset" {
		t.Errorf("create answered rrset %+v action %+v, want www/A and create_rrset", created.RRSet, created.Action)
	}
	waitSuccess(t, e, created.Action, "create RRSet")
	pz, _ := pushedZone(e.lastPush(t), "example.com")
	i := slices.IndexFunc(pz.RRSets, func(r agentapi.RRSet) bool { return r.Name == "www" && r.Type == "A" })
	if i < 0 || pz.RRSets[i].TTL != 3600 || !reflect.DeepEqual(pz.RRSets[i].Records, []string{"198.51.100.10"}) {
		t.Errorf("pushed zone %+v, want www/A with TTL 3600 and record 198.51.100.10", pz)
	}

	wantError(t, e.Do(http.MethodPost, zonePath(z.ID)+"/rrsets", create),
		http.StatusConflict, "uniqueness_error", "create of an existing RRSet")

	var ids []string
	for _, r := range listRRSets(t, e, z.ID, "") {
		ids = append(ids, r.ID)
	}
	for _, id := range []string{"@/SOA", "@/NS", "www/A"} {
		if !slices.Contains(ids, id) {
			t.Errorf("RRSet list %v, want %s", ids, id)
		}
	}
	got := mustRRSet(t, e, z.ID, "www", "A")
	if got.Zone != z.ID || got.TTL != nil || !reflect.DeepEqual(got.Records, []recordView{{"198.51.100.10", "web"}}) {
		t.Errorf("GET www/A %+v, want zone %d, ttl null, record 198.51.100.10 with comment web", got, z.ID)
	}

	before := actionCount(t, e, z.ID)
	resp = e.Do(http.MethodPut, www, map[string]any{"labels": map[string]string{"k": "v"}})
	if resp.StatusCode != http.StatusOK {
		wantError(t, resp, http.StatusOK, "", "PUT RRSet labels")
	} else {
		_ = resp.Body.Close()
	}
	if got := mustRRSet(t, e, z.ID, "www", "A"); !reflect.DeepEqual(got.Labels, map[string]string{"k": "v"}) {
		t.Errorf("RRSet labels after PUT %v, want k=v", got.Labels)
	}
	if after := actionCount(t, e, z.ID); after != before {
		t.Errorf("PUT RRSet labels: %d zone actions, want %d (no action)", after, before)
	}

	// raised runs fn and checks that the SOA serial went up.
	raised := func(what string, fn func()) {
		t.Helper()
		old := serial(t, e, z.ID)
		fn()
		if s := serial(t, e, z.ID); s <= old {
			t.Errorf("%s: SOA serial %d, want above %d", what, s, old)
		}
	}
	raised("change_ttl", func() {
		runAction(t, e, www+"/actions/change_ttl", map[string]any{"ttl": 300}, "change_rrset_ttl")
		if got := mustRRSet(t, e, z.ID, "www", "A"); got.TTL == nil || *got.TTL != 300 {
			t.Errorf("ttl after change_ttl %v, want 300", got.TTL)
		}
	})
	raised("set_records", func() {
		runAction(t, e, www+"/actions/set_records", map[string]any{"records": records("198.51.100.20", "198.51.100.21")}, "set_rrset_records")
		if got := mustRRSet(t, e, z.ID, "www", "A").values(); !reflect.DeepEqual(got, []string{"198.51.100.20", "198.51.100.21"}) {
			t.Errorf("records after set_records %v", got)
		}
	})
	mail := rrsetPath(z.ID, "mail", "A")
	raised("add_records", func() {
		runAction(t, e, mail+"/actions/add_records", map[string]any{"ttl": 600, "records": records("198.51.100.30")}, "add_rrset_records")
		if got := mustRRSet(t, e, z.ID, "mail", "A"); !reflect.DeepEqual(got.values(), []string{"198.51.100.30"}) {
			t.Errorf("mail/A after add_records %+v, want record 198.51.100.30", got)
		}
	})
	raised("remove_records", func() {
		runAction(t, e, www+"/actions/remove_records", map[string]any{"records": records("198.51.100.20")}, "remove_rrset_records")
		if got := mustRRSet(t, e, z.ID, "www", "A").values(); !reflect.DeepEqual(got, []string{"198.51.100.21"}) {
			t.Errorf("records after remove_records %v, want [198.51.100.21]", got)
		}
	})
	raised("remove_records of the last record", func() {
		runAction(t, e, mail+"/actions/remove_records", map[string]any{"records": records("198.51.100.30")}, "remove_rrset_records")
		if _, ok := getRRSet(t, e, z.ID, "mail", "A"); ok {
			t.Errorf("mail/A still there after removing its last record")
		}
	})
	raised("update_records", func() {
		a := startAction(t, e, http.MethodPost, www+"/actions/update_records",
			map[string]any{"records": []map[string]any{{"value": "198.51.100.21", "comment": "new"}}}, http.StatusOK)
		if a.Command != "update_rrset_records" {
			t.Errorf("update_records action %q, want update_rrset_records", a.Command)
		}
		waitSuccess(t, e, a, "update_records")
		if got := mustRRSet(t, e, z.ID, "www", "A"); !reflect.DeepEqual(got.Records, []recordView{{"198.51.100.21", "new"}}) {
			t.Errorf("records after update_records %+v, want 198.51.100.21 with comment new", got.Records)
		}
	})

	wantFieldError(t, e.Do(http.MethodPost, www+"/actions/set_records", map[string]any{"records": records("not-an-ip")}),
		"records[0].value", "set_records with an invalid A value")
	if got := mustRRSet(t, e, z.ID, "www", "A").values(); !reflect.DeepEqual(got, []string{"198.51.100.21"}) {
		t.Errorf("records after refused set_records %v, want [198.51.100.21]", got)
	}
	wantFieldError(t, e.Do(http.MethodPost, zonePath(z.ID)+"/rrsets", map[string]any{
		"name": "www", "type": "CNAME", "records": records("target.example.org."),
	}), "type", "CNAME beside an A")

	runAction(t, e, www+"/actions/change_protection", map[string]any{"change": true}, "change_rrset_protection")
	if got := mustRRSet(t, e, z.ID, "www", "A"); !got.Protection.Change {
		t.Errorf("protection after change_protection %+v, want change", got.Protection)
	}
	wantError(t, e.Do(http.MethodPost, www+"/actions/set_records", map[string]any{"records": records("198.51.100.99")}),
		http.StatusLocked, "protected", "set_records on a protected RRSet")
	wantError(t, e.Do(http.MethodDelete, www, nil), http.StatusLocked, "protected", "DELETE of a protected RRSet")
	runAction(t, e, www+"/actions/change_protection", map[string]any{"change": false}, "change_rrset_protection")
	del := startAction(t, e, http.MethodDelete, www, nil, http.StatusCreated)
	if del.Command != "delete_rrset" {
		t.Errorf("DELETE action %q, want delete_rrset", del.Command)
	}
	waitSuccess(t, e, del, "delete RRSet")
	if _, ok := getRRSet(t, e, z.ID, "www", "A"); ok {
		t.Errorf("www/A still there after DELETE")
	}

	soa := mustRRSet(t, e, z.ID, "@", "SOA")
	wantFieldError(t, e.Do(http.MethodPost, rrsetPath(z.ID, "@", "SOA")+"/actions/set_records",
		map[string]any{"records": records("hydrogen.ns.hetzner.com. dns.hetzner.com. 2026010100 86400 10800 3600000 3600")}),
		"type", "set_records on the apex SOA")
	if got := mustRRSet(t, e, z.ID, "@", "SOA"); !reflect.DeepEqual(got.Records, soa.Records) {
		t.Errorf("SOA after refused set_records %+v, want %+v", got.Records, soa.Records)
	}
}

// testQueue: while a zone action still publishes, further zone and RRSet
// actions on the same zone wait as running instead of answering locked, as
// the Ansible zone_rrset module and parallel OpenTofu siblings need; the
// zone delete still answers locked.
func testQueue(t *testing.T) {
	e := newZoneEnv(t)
	z := createZone(t, e, map[string]any{"name": "example.com", "mode": "primary", "ttl": 3600})
	www := rrsetPath(z.ID, "www", "A")
	runAction(t, e, zonePath(z.ID)+"/rrsets", map[string]any{"name": "www", "type": "A", "records": records("198.51.100.10")}, "create_rrset")

	hold := make(chan struct{})
	e.mu.Lock()
	e.hold = hold
	e.mu.Unlock()
	var acts []actionView
	acts = append(acts, startAction(t, e, http.MethodPost, www+"/actions/change_ttl", map[string]any{"ttl": 900}, http.StatusCreated))
	acts = append(acts, startAction(t, e, http.MethodPost, www+"/actions/set_records", map[string]any{"records": records("198.51.100.20")}, http.StatusCreated))
	acts = append(acts, startAction(t, e, http.MethodPost, zonePath(z.ID)+"/rrsets", map[string]any{"name": "api", "type": "A", "records": records("198.51.100.30")}, http.StatusCreated))
	acts = append(acts, startAction(t, e, http.MethodPost, zonePath(z.ID)+"/actions/change_ttl", map[string]any{"ttl": 7200}, http.StatusCreated))
	wantError(t, e.Do(http.MethodDelete, zonePath(z.ID), nil), http.StatusLocked, "locked", "DELETE of a zone with running actions")
	e.mu.Lock()
	e.hold = nil
	e.mu.Unlock()
	close(hold)
	for _, a := range acts {
		waitSuccess(t, e, a, "queued "+a.Command)
	}

	pz, _ := pushedZone(e.lastPush(t), "example.com")
	got := map[string]agentapi.RRSet{}
	for _, r := range pz.RRSets {
		got[r.Name+"/"+r.Type] = r
	}
	if pz.TTL != 7200 || got["www/A"].TTL != 900 || !reflect.DeepEqual(got["www/A"].Records, []string{"198.51.100.20"}) ||
		!reflect.DeepEqual(got["api/A"].Records, []string{"198.51.100.30"}) {
		t.Errorf("pushed zone %+v, want TTL 7200, www/A 900 with 198.51.100.20 and api/A with 198.51.100.30", pz)
	}
}
