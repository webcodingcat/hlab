package zones_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/zones"
	"github.com/webcodingcat/hlab/internal/store"
)

// hetznerNS are the assigned nameservers of every zone.
var hetznerNS = []string{"hydrogen.ns.hetzner.com", "oxygen.ns.hetzner.com", "helium.ns.hetzner.de"}

// clock is the fixed time of the module, so SOA serials stay within one day.
var clock = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

// fakeShadow records the shadow checks of the module.
type fakeShadow struct {
	mu      sync.Mutex
	checks  []zones.ZoneRef
	forgets []string
}

func (f *fakeShadow) Check(zone, project string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks = append(f.checks, zones.ZoneRef{Zone: zone, Project: project})
}

func (f *fakeShadow) Forget(zone string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forgets = append(f.forgets, zone)
}

func (f *fakeShadow) checked() []zones.ZoneRef {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.checks)
}

type zoneEnv struct {
	*apitest.Env
	mod    *zones.Module
	shadow *fakeShadow

	failPush atomic.Bool
	mu       sync.Mutex
	gate     *apitest.PushGate
	pushes   []agentapi.Config
	sets     [][]string
}

// newZoneEnv mounts the zones module with a recording agent push (failing
// while failPush is set), a recording SetZones and a fake shadow watch.
func newZoneEnv(t *testing.T) *zoneEnv {
	t.Helper()
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	// ServersDeps opens the store the next apitest.New uses.
	st := apitest.ServersDeps(t, cat, platformtest.NewFake()).Store
	e := &zoneEnv{shadow: &fakeShadow{}, gate: apitest.NewPushGate(t)}
	e.mod = zones.New(zones.Deps{
		Store:     st,
		PushAgent: e.push,
		SetZones:  e.setZones,
		Shadow:    e.shadow,
		Now:       func() time.Time { return clock },
	})
	e.Env = apitest.New(t, e.mod, resactions.New())
	return e
}

func (e *zoneEnv) push(ctx context.Context) error {
	if e.failPush.Load() {
		return errors.New("agent unreachable")
	}
	if err := e.gate.Wait(ctx); err != nil {
		return err
	}
	var doc agentapi.Config
	if err := e.Store.Tx(func(tx store.Tx) error { return e.mod.Contribute(tx, &doc) }); err != nil {
		return err
	}
	e.mu.Lock()
	e.pushes = append(e.pushes, doc)
	e.mu.Unlock()
	return nil
}

func (e *zoneEnv) setZones(_ context.Context, names []string) error {
	e.mu.Lock()
	e.sets = append(e.sets, slices.Clone(names))
	e.mu.Unlock()
	return nil
}

// lastPush returns the latest pushed agent document.
func (e *zoneEnv) lastPush(t *testing.T) agentapi.Config {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.pushes) == 0 {
		t.Fatal("no agent push")
	}
	return e.pushes[len(e.pushes)-1]
}

// lastSetZones returns the zone names of the latest SetZones call.
func (e *zoneEnv) lastSetZones(t *testing.T) []string {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.sets) == 0 {
		t.Fatal("no SetZones call")
	}
	return e.sets[len(e.sets)-1]
}

// pushedZone returns the zone name of the latest pushed document.
func pushedZone(doc agentapi.Config, name string) (agentapi.Zone, bool) {
	for _, z := range doc.Zones {
		if z.Name == name {
			return z, true
		}
	}
	return agentapi.Zone{}, false
}

type ref struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type actionView struct {
	ID        int64  `json:"id"`
	Command   string `json:"command"`
	Status    string `json:"status"`
	Resources []ref  `json:"resources"`
}

type nsView struct {
	Address       string `json:"address"`
	Port          int    `json:"port"`
	TSIGKey       string `json:"tsig_key,omitempty"`
	TSIGAlgorithm string `json:"tsig_algorithm,omitempty"`
}

type zoneView struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Mode        string            `json:"mode"`
	TTL         int               `json:"ttl"`
	Labels      map[string]string `json:"labels"`
	RecordCount int               `json:"record_count"`
	Status      string            `json:"status"`
	Protection  struct {
		Delete bool `json:"delete"`
	} `json:"protection"`
	PrimaryNameservers       []nsView `json:"primary_nameservers"`
	AuthoritativeNameservers struct {
		Assigned []string `json:"assigned"`
	} `json:"authoritative_nameservers"`
}

type recordView struct {
	Value   string `json:"value"`
	Comment string `json:"comment,omitempty"`
}

type rrsetView struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	TTL        *int              `json:"ttl"`
	Labels     map[string]string `json:"labels"`
	Protection struct {
		Change bool `json:"change"`
	} `json:"protection"`
	Records []recordView `json:"records"`
	Zone    int64        `json:"zone"`
}

// values returns the record values of r, sorted.
func (r rrsetView) values() []string {
	var v []string
	for _, rec := range r.Records {
		v = append(v, rec.Value)
	}
	slices.Sort(v)
	return v
}

func zonePath(id int64) string { return fmt.Sprintf("/zones/%d", id) }

func rrsetPath(zone int64, name, typ string) string {
	return fmt.Sprintf("/zones/%d/rrsets/%s/%s", zone, name, typ)
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details struct {
			Fields []struct {
				Name string `json:"name"`
			} `json:"fields"`
		} `json:"details"`
	} `json:"error"`
}

// wantError checks status and error.code.
func wantError(t *testing.T, resp *http.Response, status int, code, what string) errorBody {
	t.Helper()
	var b errorBody
	apitest.Decode(t, resp, &b)
	if resp.StatusCode != status || b.Error.Code != code {
		t.Errorf("%s: status %d code %q (%s), want %d %s", what, resp.StatusCode, b.Error.Code, b.Error.Message, status, code)
	}
	return b
}

// wantFieldError checks a 422 invalid_input naming field.
func wantFieldError(t *testing.T, resp *http.Response, field, what string) {
	t.Helper()
	b := wantError(t, resp, http.StatusUnprocessableEntity, "invalid_input", what)
	var names []string
	for _, f := range b.Error.Details.Fields {
		names = append(names, f.Name)
	}
	if !slices.Contains(names, field) {
		t.Errorf("%s: error on fields %v, want %s", what, names, field)
	}
}

// waitSuccess waits for action a and fails unless it succeeds.
func waitSuccess(t *testing.T, e *zoneEnv, a actionView, what string) {
	t.Helper()
	if got := e.For(t).WaitAction(a.ID); got.Status != "success" {
		t.Fatalf("%s: action %d %s ended %q %+v, want success", what, a.ID, a.Command, got.Status, got.Error)
	}
}

// postZone posts a zone create and returns the response.
func postZone(e *zoneEnv, body map[string]any) *http.Response {
	return e.Do(http.MethodPost, "/zones", body)
}

// createZone creates a zone, expects 201 and a successful create_zone
// action, and returns the zone of the answer.
func createZone(t *testing.T, e *zoneEnv, body map[string]any) zoneView {
	t.Helper()
	resp := postZone(e, body)
	if resp.StatusCode != http.StatusCreated {
		wantError(t, resp, http.StatusCreated, "", fmt.Sprintf("POST /zones %v", body))
		t.FailNow()
	}
	var b struct {
		Zone   zoneView   `json:"zone"`
		Action actionView `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	if b.Action.Command != "create_zone" {
		t.Errorf("create action %+v, want create_zone", b.Action)
	}
	waitSuccess(t, e, b.Action, "create zone "+b.Zone.Name)
	return b.Zone
}

// getZone returns the zone at /zones/{idOrName} and whether it exists.
func getZone(t *testing.T, e *zoneEnv, idOrName string) (zoneView, bool) {
	t.Helper()
	resp := e.Do(http.MethodGet, "/zones/"+idOrName, nil)
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return zoneView{}, false
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /zones/%s: status %d, want 200", idOrName, resp.StatusCode)
	}
	var b struct {
		Zone zoneView `json:"zone"`
	}
	apitest.Decode(t, resp, &b)
	return b.Zone, true
}

func mustZone(t *testing.T, e *zoneEnv, id int64) zoneView {
	t.Helper()
	z, ok := getZone(t, e, strconv.FormatInt(id, 10))
	if !ok {
		t.Fatalf("zone %d: 404, want the zone", id)
	}
	return z
}

// startAction posts body to path, expects status and returns the action.
func startAction(t *testing.T, e *zoneEnv, method, path string, body any, status int) actionView {
	t.Helper()
	resp := e.Do(method, path, body)
	if resp.StatusCode != status {
		wantError(t, resp, status, "", fmt.Sprintf("%s %s %v", method, path, body))
		t.FailNow()
	}
	var b struct {
		Action actionView `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	return b.Action
}

// runAction posts body to path, expects 201 and an action command that
// succeeds, and returns the action.
func runAction(t *testing.T, e *zoneEnv, path string, body any, command string) actionView {
	t.Helper()
	a := startAction(t, e, http.MethodPost, path, body, http.StatusCreated)
	if a.Command != command {
		t.Errorf("POST %s: action %q, want %s", path, a.Command, command)
	}
	waitSuccess(t, e, a, "POST "+path)
	return a
}

// actionCount returns the number of actions of zone id.
func actionCount(t *testing.T, e *zoneEnv, id int64) int {
	t.Helper()
	resp := e.Do(http.MethodGet, zonePath(id)+"/actions?per_page=50", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s/actions: status %d", zonePath(id), resp.StatusCode)
	}
	var b struct {
		Actions []actionView `json:"actions"`
	}
	apitest.Decode(t, resp, &b)
	return len(b.Actions)
}

// listRRSets returns the RRSets of zone id with the extra query.
func listRRSets(t *testing.T, e *zoneEnv, id int64, query string) []rrsetView {
	t.Helper()
	path := zonePath(id) + "/rrsets?per_page=50"
	if query != "" {
		path += "&" + query
	}
	resp := e.Do(http.MethodGet, path, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", path, resp.StatusCode)
	}
	var b struct {
		RRSets []rrsetView `json:"rrsets"`
	}
	apitest.Decode(t, resp, &b)
	return b.RRSets
}

// getRRSet returns the RRSet and whether it exists.
func getRRSet(t *testing.T, e *zoneEnv, zone int64, name, typ string) (rrsetView, bool) {
	t.Helper()
	resp := e.Do(http.MethodGet, rrsetPath(zone, name, typ), nil)
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return rrsetView{}, false
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", rrsetPath(zone, name, typ), resp.StatusCode)
	}
	var b struct {
		RRSet rrsetView `json:"rrset"`
	}
	apitest.Decode(t, resp, &b)
	return b.RRSet, true
}

func mustRRSet(t *testing.T, e *zoneEnv, zone int64, name, typ string) rrsetView {
	t.Helper()
	r, ok := getRRSet(t, e, zone, name, typ)
	if !ok {
		t.Fatalf("RRSet %s/%s of zone %d: 404, want it", name, typ, zone)
	}
	return r
}

// serial returns the serial of the zone's SOA record.
func serial(t *testing.T, e *zoneEnv, zone int64) uint64 {
	t.Helper()
	soa := mustRRSet(t, e, zone, "@", "SOA")
	if len(soa.Records) != 1 {
		t.Fatalf("SOA records %+v, want one", soa.Records)
	}
	f := strings.Fields(soa.Records[0].Value)
	if len(f) < 3 {
		t.Fatalf("SOA value %q has no serial", soa.Records[0].Value)
	}
	n, err := strconv.ParseUint(f[2], 10, 32)
	if err != nil {
		t.Fatalf("SOA serial %q: %v", f[2], err)
	}
	return n
}

// trimDots returns the values without trailing dots, sorted.
func trimDots(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = strings.TrimSuffix(v, ".")
	}
	slices.Sort(out)
	return out
}

func TestZones(t *testing.T) {
	t.Run("DNS-01/zones", testZones)
	t.Run("DNS-01/secondary", testSecondary)
	t.Run("DNS-01/rrsets", testRRSets)
	t.Run("DNS-01/zonefile", testZonefile)
	t.Run("CLI-02/queue", testQueue)
	t.Run("CLI-02/effect", testEffect)
	t.Run("DNS-04", testShadow)
}

func testZones(t *testing.T) {
	e := newZoneEnv(t)
	resp := postZone(e, map[string]any{
		"name": "example.com", "mode": "primary", "ttl": 7200,
		"labels": map[string]string{"env": "lab"},
		"rrsets": []map[string]any{{
			"name": "www", "type": "A",
			"records": []map[string]any{{"value": "198.51.100.10"}, {"value": "198.51.100.11"}},
		}},
	})
	if resp.StatusCode != http.StatusCreated {
		wantError(t, resp, http.StatusCreated, "", "POST /zones")
		t.FailNow()
	}
	var created struct {
		Zone   zoneView   `json:"zone"`
		Action actionView `json:"action"`
	}
	apitest.Decode(t, resp, &created)
	z := created.Zone
	if z.Name != "example.com" || z.Mode != "primary" || z.TTL != 7200 || z.Labels["env"] != "lab" || z.Status != "ok" {
		t.Errorf("created zone %+v, want example.com, primary, ttl 7200, label env=lab, status ok", z)
	}
	if z.RecordCount != 6 {
		t.Errorf("record_count %d, want 6 (SOA, three NS, two A)", z.RecordCount)
	}
	if got := trimDots(z.AuthoritativeNameservers.Assigned); !reflect.DeepEqual(got, trimDots(hetznerNS)) {
		t.Errorf("assigned nameservers %v, want %v", z.AuthoritativeNameservers.Assigned, hetznerNS)
	}
	if created.Action.Command != "create_zone" || !reflect.DeepEqual(created.Action.Resources, []ref{{z.ID, "zone"}}) {
		t.Errorf("create action %+v, want create_zone naming zone %d", created.Action, z.ID)
	}
	waitSuccess(t, e, created.Action, "create zone")
	if got := e.lastSetZones(t); !reflect.DeepEqual(got, []string{"example.com"}) {
		t.Errorf("SetZones %v after create, want [example.com]", got)
	}
	pz, ok := pushedZone(e.lastPush(t), "example.com")
	if !ok || !slices.ContainsFunc(pz.RRSets, func(r agentapi.RRSet) bool { return r.Name == "www" && r.Type == "A" }) {
		t.Errorf("pushed zone %+v (found %v), want example.com with www/A", pz, ok)
	}

	soa := mustRRSet(t, e, z.ID, "@", "SOA")
	if len(soa.Records) != 1 {
		t.Errorf("SOA records %+v, want one", soa.Records)
	}
	ns := mustRRSet(t, e, z.ID, "@", "NS")
	if got := trimDots(ns.values()); !reflect.DeepEqual(got, trimDots(hetznerNS)) {
		t.Errorf("apex NS records %v, want %v", ns.values(), hetznerNS)
	}

	byID := mustZone(t, e, z.ID)
	byName, ok := getZone(t, e, "example.com")
	if !ok || byName.ID != z.ID || byID.Name != "example.com" {
		t.Errorf("GET by ID %+v, by name %+v (found %v), want zone %d", byID, byName, ok, z.ID)
	}

	before := actionCount(t, e, z.ID)
	resp = e.Do(http.MethodPut, zonePath(z.ID), map[string]any{"labels": map[string]string{"env": "prod"}})
	if resp.StatusCode != http.StatusOK {
		wantError(t, resp, http.StatusOK, "", "PUT labels")
	} else {
		_ = resp.Body.Close()
	}
	if got := mustZone(t, e, z.ID); !reflect.DeepEqual(got.Labels, map[string]string{"env": "prod"}) {
		t.Errorf("labels after PUT %v, want env=prod", got.Labels)
	}
	if after := actionCount(t, e, z.ID); after != before {
		t.Errorf("PUT labels: %d zone actions, want %d (no action)", after, before)
	}

	runAction(t, e, zonePath(z.ID)+"/actions/change_ttl", map[string]any{"ttl": 600}, "change_ttl")
	if got := mustZone(t, e, z.ID); got.TTL != 600 {
		t.Errorf("ttl after change_ttl %d, want 600", got.TTL)
	}
	runAction(t, e, zonePath(z.ID)+"/actions/change_protection", map[string]any{"delete": true}, "change_protection")
	if got := mustZone(t, e, z.ID); !got.Protection.Delete {
		t.Errorf("protection after change_protection %+v, want delete", got.Protection)
	}
	wantError(t, e.Do(http.MethodDelete, zonePath(z.ID), nil), http.StatusLocked, "protected", "DELETE protected zone")
	if _, ok := getZone(t, e, "example.com"); !ok {
		t.Errorf("protected zone gone after refused DELETE")
	}

	q := e.For(t)
	q.Token = e.NewToken("q", false)
	wantError(t, q.Do(http.MethodPost, "/zones", map[string]any{"name": "example.com", "mode": "primary"}),
		http.StatusConflict, "uniqueness_error", "same zone name in another project")

	wantFieldError(t, postZone(e, map[string]any{"name": "sub.example.com", "mode": "primary"}), "name", "invalid zone name")

	e.failPush.Store(true)
	a := startAction(t, e, http.MethodPost, zonePath(z.ID)+"/actions/change_ttl", map[string]any{"ttl": 900}, http.StatusCreated)
	if got := e.For(t).WaitAction(a.ID); got.Status != "error" {
		t.Errorf("change_ttl with a failing agent push ended %q, want error", got.Status)
	}
	e.failPush.Store(false)

	for _, p := range []string{fmt.Sprintf("/zones/actions/%d", a.ID), fmt.Sprintf("/actions/%d", a.ID)} {
		resp := e.Do(http.MethodGet, p, nil)
		var b struct {
			Action actionView `json:"action"`
		}
		apitest.Decode(t, resp, &b)
		if resp.StatusCode != http.StatusOK || b.Action.ID != a.ID || b.Action.Command != "change_ttl" {
			t.Errorf("GET %s: status %d action %+v, want the change_ttl action", p, resp.StatusCode, b.Action)
		}
	}

	runAction(t, e, zonePath(z.ID)+"/actions/change_protection", map[string]any{"delete": false}, "change_protection")
	del := startAction(t, e, http.MethodDelete, zonePath(z.ID), nil, http.StatusCreated)
	if del.Command != "delete_zone" {
		t.Errorf("DELETE action %q, want delete_zone", del.Command)
	}
	waitSuccess(t, e, del, "delete zone")
	if got := e.lastSetZones(t); slices.Contains(got, "example.com") {
		t.Errorf("SetZones %v after delete, still names example.com", got)
	}
	if _, ok := getZone(t, e, strconv.FormatInt(z.ID, 10)); ok {
		t.Errorf("zone %d still there after DELETE", z.ID)
	}
}

func testSecondary(t *testing.T) {
	e := newZoneEnv(t)
	sec := createZone(t, e, map[string]any{
		"name": "example.org", "mode": "secondary",
		"primary_nameservers": []map[string]any{{"address": "192.0.2.53", "port": 5353}, {"address": "2001:db8::53"}},
	})
	want := []nsView{{Address: "192.0.2.53", Port: 5353}, {Address: "2001:db8::53", Port: 53}}
	if got := mustZone(t, e, sec.ID); got.Mode != "secondary" || !reflect.DeepEqual(got.PrimaryNameservers, want) {
		t.Errorf("secondary zone %+v, want mode secondary with primary_nameservers %+v", got, want)
	}
	if got := e.lastSetZones(t); !slices.Contains(got, "example.org") {
		t.Errorf("SetZones %v, want example.org named", got)
	}
	if _, ok := pushedZone(e.lastPush(t), "example.org"); ok {
		t.Errorf("secondary zone example.org in the pushed agent document")
	}

	key := "c2VjcmV0LWtleS1mb3ItdHNpZw=="
	runAction(t, e, zonePath(sec.ID)+"/actions/change_primary_nameservers", map[string]any{
		"primary_nameservers": []map[string]any{{"address": "192.0.2.54", "tsig_algorithm": "hmac-sha256", "tsig_key": key}},
	}, "change_primary_nameservers")
	want = []nsView{{Address: "192.0.2.54", Port: 53, TSIGAlgorithm: "hmac-sha256", TSIGKey: key}}
	if got := mustZone(t, e, sec.ID); !reflect.DeepEqual(got.PrimaryNameservers, want) {
		t.Errorf("primary_nameservers after change %+v, want %+v", got.PrimaryNameservers, want)
	}

	for _, c := range []struct {
		name  string
		ns    []map[string]any
		field string
	}{
		{"address no IP", []map[string]any{{"address": "ns1.example.org"}}, "primary_nameservers[0].address"},
		{"duplicate address", []map[string]any{{"address": "192.0.2.53"}, {"address": "192.0.2.53"}}, "primary_nameservers[1].address"},
		{"unknown tsig_algorithm", []map[string]any{{"address": "192.0.2.53", "tsig_algorithm": "hmac-sha512", "tsig_key": key}}, "primary_nameservers[0].tsig_algorithm"},
	} {
		wantFieldError(t, postZone(e, map[string]any{"name": "example.net", "mode": "secondary", "primary_nameservers": c.ns}),
			c.field, c.name)
	}
	if _, ok := getZone(t, e, "example.net"); ok {
		t.Errorf("refused secondary create stored example.net")
	}

	wantError(t, e.Do(http.MethodPost, zonePath(sec.ID)+"/actions/change_ttl", map[string]any{"ttl": 600}),
		http.StatusUnprocessableEntity, "incorrect_zone_mode", "change_ttl on a secondary zone")
	wantError(t, e.Do(http.MethodPost, zonePath(sec.ID)+"/rrsets", map[string]any{
		"name": "www", "type": "A", "records": []map[string]any{{"value": "198.51.100.10"}},
	}), http.StatusUnprocessableEntity, "incorrect_zone_mode", "RRSet create on a secondary zone")
	wantError(t, e.Do(http.MethodGet, zonePath(sec.ID)+"/zonefile", nil),
		http.StatusUnprocessableEntity, "incorrect_zone_mode", "zone file export of a secondary zone")

	prim := createZone(t, e, map[string]any{"name": "example.com", "mode": "primary"})
	wantError(t, e.Do(http.MethodPost, zonePath(prim.ID)+"/actions/change_primary_nameservers", map[string]any{
		"primary_nameservers": []map[string]any{{"address": "192.0.2.53"}},
	}), http.StatusUnprocessableEntity, "incorrect_zone_mode", "change_primary_nameservers on a primary zone")
}
