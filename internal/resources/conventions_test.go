package resources_test

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	actionsres "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/datacenters"
	"github.com/webcodingcat/hlab/internal/resources/firewalls"
	"github.com/webcodingcat/hlab/internal/resources/floatingips"
	imagesres "github.com/webcodingcat/hlab/internal/resources/images"
	"github.com/webcodingcat/hlab/internal/resources/locations"
	"github.com/webcodingcat/hlab/internal/resources/networks"
	"github.com/webcodingcat/hlab/internal/resources/placementgroups"
	"github.com/webcodingcat/hlab/internal/resources/pricing"
	"github.com/webcodingcat/hlab/internal/resources/primaryips"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/resources/servertypes"
	"github.com/webcodingcat/hlab/internal/resources/sshkeys"
)

// conventionRows runs the rows of the cross-resource conventions table as
// subtests of TestResources.
func conventionRows(t *testing.T) {
	t.Run("API-03", testReadOnly)
	t.Run("API-04", testProjectIsolation)
	t.Run("API-05", testListPaging)
	t.Run("API-06", testListFilterSort)
	t.Run("API-07", testLabelsOnResources)
	t.Run("API-11", testNameUniqueness)
}

// newFullEnv mounts every Hetzner API module of the phase.
func newFullEnv(t *testing.T) *apitest.Env {
	t.Helper()
	cat, err := catalog.Load("../../catalog")
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	d := apitest.ServersDeps(t, cat, platformtest.NewFake())
	srv := servers.New(d)
	nets := networks.New(networks.Deps{Store: d.Store, Catalog: cat, Servers: srv, PushAgent: d.PushAgent})
	pips := primaryips.New(primaryips.Deps{Store: d.Store, Catalog: cat, PublicRange: d.PublicRange, Servers: srv, PushAgent: d.PushAgent})
	fips := floatingips.New(floatingips.Deps{Store: d.Store, Catalog: cat, PublicRange: d.PublicRange, PushAgent: d.PushAgent})
	srv.SetNetworks(nets)
	srv.SetPublicIPs(pips)
	srv.OnDelete(pips.ServerDeleted)
	srv.OnDelete(fips.ServerDeleted)
	fws := firewalls.New(firewalls.Deps{Store: d.Store, PushAgent: d.PushAgent})
	srv.SetFirewalls(fws)
	srv.OnDelete(fws.ServerDeleted)
	return apitest.New(t,
		actionsres.New(),
		locations.New(cat),
		datacenters.New(),
		servertypes.New(cat),
		imagesres.New(cat),
		pricing.New(cat),
		sshkeys.New(),
		placementgroups.New(),
		srv, nets, pips, fips, fws,
	)
}

// as returns e reporting on t and sending token instead of e.Token.
func as(t *testing.T, e *apitest.Env, token string) *apitest.Env {
	c := e.For(t)
	c.Token = token
	return c
}

// call sends a request with e.Token and decodes a JSON body, if any.
func call(t *testing.T, e *apitest.Env, method, path string, body any) (int, map[string]any) {
	t.Helper()
	resp := e.Do(method, path, body)
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	var m map[string]any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("%s %s: decode body %q: %v", method, path, data, err)
		}
	}
	return resp.StatusCode, m
}

// errCode returns error.code of a decoded body.
func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

// errFields returns the names in error.details.fields of a decoded body.
func errFields(m map[string]any) []string {
	e, _ := m["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	fs, _ := d["fields"].([]any)
	var names []string
	for _, f := range fs {
		if fm, ok := f.(map[string]any); ok {
			n, _ := fm["name"].(string)
			names = append(names, n)
		}
	}
	return names
}

func idOf(v any) int64 {
	m, _ := v.(map[string]any)
	f, _ := m["id"].(float64)
	return int64(f)
}

// sshPublicKey returns a well-formed OpenSSH ed25519 public key line that
// differs for every n.
func sshPublicKey(n int) string {
	key := make([]byte, 32)
	binary.BigEndian.PutUint32(key, uint32(n)+1000)
	for i := 4; i < 32; i++ {
		key[i] = byte(i)
	}
	var wire []byte
	for _, part := range [][]byte{[]byte("ssh-ed25519"), key} {
		wire = binary.BigEndian.AppendUint32(wire, uint32(len(part)))
		wire = append(wire, part...)
	}
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(wire) + " conventions"
}

// labeled is a resource type with labels and a project-unique name.
type labeled struct {
	typ  string // path and list key, "ssh_keys"
	key  string // key of the single object, "ssh_key"
	body func(name string, n int) map[string]any
}

var labeledTypes = []labeled{
	{"ssh_keys", "ssh_key", func(name string, n int) map[string]any {
		return map[string]any{"name": name, "public_key": sshPublicKey(n)}
	}},
	{"servers", "server", func(name string, _ int) map[string]any {
		return map[string]any{"name": name, "server_type": "cx23", "image": "ubuntu-24.04", "location": "fsn1"}
	}},
	{"placement_groups", "placement_group", func(name string, _ int) map[string]any {
		return map[string]any{"name": name, "type": "spread"}
	}},
}

var keyCounter struct {
	sync.Mutex
	n int
}

func nextN() int {
	keyCounter.Lock()
	defer keyCounter.Unlock()
	keyCounter.n++
	return keyCounter.n
}

// create creates a resource of r named name with labels (nil: none) and
// waits for its action; it fails the test unless the answer is 201.
func create(t *testing.T, e *apitest.Env, r labeled, name string, labels map[string]string, extra map[string]any) map[string]any {
	t.Helper()
	body := r.body(name, nextN())
	if labels != nil {
		body["labels"] = labels
	}
	for k, v := range extra {
		body[k] = v
	}
	status, m := call(t, e, http.MethodPost, "/"+r.typ, body)
	if status != http.StatusCreated {
		t.Fatalf("POST /%s %q: status %d, want 201: %v", r.typ, name, status, m)
	}
	if a := idOf(m["action"]); a != 0 {
		e.For(t).WaitAction(a)
	}
	obj, _ := m[r.key].(map[string]any)
	return obj
}

// snapshot returns the bodies of the project's lists, to compare before and
// after requests that must change nothing.
func snapshot(t *testing.T, e *apitest.Env) string {
	t.Helper()
	var b strings.Builder
	for _, p := range []string{"/ssh_keys", "/servers", "/placement_groups", "/servers/actions"} {
		resp := e.Do(http.MethodGet, p+"?per_page=50", nil)
		data, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		fmt.Fprintf(&b, "%s %d %s\n", p, resp.StatusCode, data)
	}
	return b.String()
}

// fixture is one SSH key, one placement group and a server in that group
// with its finished create action, all in project p.
type fixture struct {
	key, group, server, action int64
}

func newFixture(t *testing.T, e *apitest.Env) fixture {
	t.Helper()
	var f fixture
	f.key = idOf(create(t, e, labeledTypes[0], "key-a", nil, nil))
	f.group = idOf(create(t, e, labeledTypes[2], "group-a", nil, nil))
	status, m := call(t, e, http.MethodPost, "/servers", map[string]any{
		"name": "server-a", "server_type": "cx23", "image": "ubuntu-24.04", "location": "fsn1",
		"placement_group": f.group,
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /servers: status %d: %v", status, m)
	}
	f.server, f.action = idOf(m["server"]), idOf(m["action"])
	e.For(t).WaitAction(f.action)
	return f
}

// firstID returns the ID of the first item of a list.
func firstID(t *testing.T, e *apitest.Env, path, key string) int64 {
	t.Helper()
	status, m := call(t, e, http.MethodGet, path, nil)
	items, _ := m[key].([]any)
	if status != http.StatusOK || len(items) == 0 {
		t.Fatalf("GET %s: status %d, %d items", path, status, len(items))
	}
	return idOf(items[0])
}

func testReadOnly(t *testing.T) {
	env := newFullEnv(t)
	f := newFixture(t, env)
	ro := as(t, env, env.NewToken("p", true))
	loc := firstID(t, env, "/locations", "locations")
	typ := firstID(t, env, "/server_types", "server_types")
	img := firstID(t, env, "/images", "images")

	reads := []string{
		"/locations", fmt.Sprintf("/locations/%d", loc),
		"/server_types", fmt.Sprintf("/server_types/%d", typ),
		"/images", fmt.Sprintf("/images/%d", img),
		"/pricing",
		"/ssh_keys", fmt.Sprintf("/ssh_keys/%d", f.key),
		"/placement_groups", fmt.Sprintf("/placement_groups/%d", f.group),
		"/servers", fmt.Sprintf("/servers/%d", f.server),
		fmt.Sprintf("/actions?id=%d", f.action), fmt.Sprintf("/actions/%d", f.action),
		"/servers/actions",
		fmt.Sprintf("/servers/%d/actions", f.server),
		fmt.Sprintf("/servers/%d/actions/%d", f.server, f.action),
	}
	for _, p := range reads {
		t.Run("GET "+p, func(t *testing.T) {
			if status, m := call(t, as(t, ro, ro.Token), http.MethodGet, p, nil); status != http.StatusOK {
				t.Errorf("status %d, want 200: %v", status, m)
			}
		})
	}

	srv := fmt.Sprintf("/servers/%d", f.server)
	writes := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/ssh_keys", map[string]any{"name": "ro-key", "public_key": sshPublicKey(nextN())}},
		{http.MethodPut, fmt.Sprintf("/ssh_keys/%d", f.key), map[string]any{"name": "ro-renamed"}},
		{http.MethodDelete, fmt.Sprintf("/ssh_keys/%d", f.key), nil},
		{http.MethodPost, "/placement_groups", map[string]any{"name": "ro-group", "type": "spread"}},
		{http.MethodPut, fmt.Sprintf("/placement_groups/%d", f.group), map[string]any{"name": "ro-renamed"}},
		{http.MethodDelete, fmt.Sprintf("/placement_groups/%d", f.group), nil},
		{http.MethodPost, "/servers", map[string]any{"name": "ro-server", "server_type": "cx23", "image": "ubuntu-24.04", "location": "fsn1"}},
		{http.MethodPut, srv, map[string]any{"name": "ro-renamed"}},
		{http.MethodDelete, srv, nil},
		{http.MethodPost, srv + "/actions/change_protection", map[string]any{"delete": true}},
		{http.MethodPost, srv + "/actions/request_console", nil},
		{http.MethodPost, srv + "/actions/remove_from_placement_group", nil},
		{http.MethodPost, srv + "/actions/add_to_placement_group", map[string]any{"placement_group": f.group}},
	}
	before := snapshot(t, env)
	for _, w := range writes {
		t.Run(w.method+" "+w.path, func(t *testing.T) {
			status, m := call(t, as(t, ro, ro.Token), w.method, w.path, w.body)
			if status != http.StatusForbidden || errCode(m) != "token_readonly" {
				t.Errorf("status %d code %q, want 403 token_readonly", status, errCode(m))
			}
		})
	}
	if after := snapshot(t, env); after != before {
		t.Errorf("read-only writes changed the project:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func testProjectIsolation(t *testing.T) {
	env := newFullEnv(t)
	f := newFixture(t, env)
	// A change_protection action that only project p may see.
	status, m := call(t, env, http.MethodPost, fmt.Sprintf("/servers/%d/actions/change_protection", f.server),
		map[string]any{"delete": false})
	if status != http.StatusCreated {
		t.Fatalf("change_protection: status %d: %v", status, m)
	}
	protectAction := idOf(m["action"])
	env.For(t).WaitAction(protectAction)
	q := env.NewToken("q", false)
	srv := fmt.Sprintf("/servers/%d", f.server)

	type request struct {
		method, path string
		body         any
	}
	cases := []struct {
		name     string
		lists    []string // list paths whose items must not include ids
		listKey  string
		ids      []int64
		requests []request
	}{
		{"ssh_keys", []string{"/ssh_keys"}, "ssh_keys", []int64{f.key}, []request{
			{http.MethodGet, fmt.Sprintf("/ssh_keys/%d", f.key), nil},
			{http.MethodPut, fmt.Sprintf("/ssh_keys/%d", f.key), map[string]any{"name": "taken-over"}},
			{http.MethodDelete, fmt.Sprintf("/ssh_keys/%d", f.key), nil},
		}},
		{"servers", []string{"/servers"}, "servers", []int64{f.server}, []request{
			{http.MethodGet, srv, nil},
			{http.MethodPut, srv, map[string]any{"name": "taken-over"}},
			{http.MethodDelete, srv, nil},
			{http.MethodGet, srv + "/actions", nil},
			{http.MethodGet, fmt.Sprintf("%s/actions/%d", srv, f.action), nil},
			{http.MethodPost, srv + "/actions/change_protection", map[string]any{"delete": true}},
			{http.MethodPost, srv + "/actions/remove_from_placement_group", nil},
		}},
		{"placement_groups", []string{"/placement_groups"}, "placement_groups", []int64{f.group}, []request{
			{http.MethodGet, fmt.Sprintf("/placement_groups/%d", f.group), nil},
			{http.MethodPut, fmt.Sprintf("/placement_groups/%d", f.group), map[string]any{"name": "taken-over"}},
			{http.MethodDelete, fmt.Sprintf("/placement_groups/%d", f.group), nil},
		}},
		{"actions", []string{
			fmt.Sprintf("/actions?id=%d&id=%d", f.action, protectAction),
			"/servers/actions",
		}, "actions", []int64{f.action, protectAction}, []request{
			{http.MethodGet, fmt.Sprintf("/actions/%d", f.action), nil},
			{http.MethodGet, fmt.Sprintf("/actions/%d", protectAction), nil},
		}},
	}
	before := snapshot(t, env)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eq := as(t, env, q)
			for _, l := range c.lists {
				status, m := call(t, eq, http.MethodGet, l, nil)
				if status != http.StatusOK {
					t.Errorf("GET %s: status %d, want 200", l, status)
					continue
				}
				items, _ := m[c.listKey].([]any)
				for _, it := range items {
					for _, id := range c.ids {
						if idOf(it) == id {
							t.Errorf("GET %s shows project p's item %d", l, id)
						}
					}
				}
			}
			for _, r := range c.requests {
				status, m := call(t, eq, r.method, r.path, r.body)
				if status != http.StatusNotFound || errCode(m) != "not_found" {
					t.Errorf("%s %s: status %d code %q, want 404 not_found", r.method, r.path, status, errCode(m))
				}
			}
		})
	}
	if after := snapshot(t, env); after != before {
		t.Errorf("requests of project q changed project p:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func testListPaging(t *testing.T) {
	env := newFullEnv(t)
	for _, name := range []string{"one", "two"} {
		for _, r := range labeledTypes {
			create(t, env, r, strings.ReplaceAll(r.key, "_", "-")+"-"+name, nil, nil)
		}
	}
	server := firstID(t, env, "/servers", "servers")
	status, m := call(t, env, http.MethodPost, fmt.Sprintf("/servers/%d/actions/change_protection", server),
		map[string]any{"delete": false})
	if status != http.StatusCreated {
		t.Fatalf("change_protection: status %d: %v", status, m)
	}
	env.For(t).WaitAction(idOf(m["action"]))

	endpoints := []struct{ path, key string }{
		{"/ssh_keys", "ssh_keys"},
		{"/servers", "servers"},
		{"/placement_groups", "placement_groups"},
		{"/server_types", "server_types"},
		{"/locations", "locations"},
		{"/images", "images"},
		{"/servers/actions", "actions"},
		{fmt.Sprintf("/servers/%d/actions", server), "actions"},
	}
	for _, ep := range endpoints {
		t.Run(ep.path, func(t *testing.T) {
			e := env.For(t)
			_, all := call(t, e, http.MethodGet, ep.path+"?per_page=50", nil)
			full, _ := all[ep.key].([]any)
			if len(full) < 2 {
				t.Fatalf("%d items, need two", len(full))
			}
			total := pagination(all)["total_entries"]
			for page := 1; page <= 2; page++ {
				status, m := call(t, e, http.MethodGet, fmt.Sprintf("%s?per_page=1&page=%d", ep.path, page), nil)
				if status != http.StatusOK {
					t.Fatalf("page %d: status %d", page, status)
				}
				items, _ := m[ep.key].([]any)
				if len(items) != 1 || idOf(items[0]) != idOf(full[page-1]) {
					t.Errorf("page %d: items %v, want only ID %d", page, ids(items), idOf(full[page-1]))
				}
				p := pagination(m)
				if p["page"] != float64(page) || p["per_page"] != float64(1) || p["total_entries"] != total {
					t.Errorf("page %d: meta.pagination %v", page, p)
				}
			}
		})
	}
}

func pagination(m map[string]any) map[string]any {
	meta, _ := m["meta"].(map[string]any)
	p, _ := meta["pagination"].(map[string]any)
	return p
}

func ids(items []any) []int64 {
	out := make([]int64, 0, len(items))
	for _, it := range items {
		out = append(out, idOf(it))
	}
	return out
}

// listParams returns the query parameters of GET path in the frozen
// OpenAPI description and the documented sort fields (without :asc/:desc).
func listParams(t *testing.T, path string) (map[string]bool, []string) {
	t.Helper()
	descOnce.Do(func() {
		p, err := apitest.DescriptionPath()
		if err != nil {
			descErr = err
			return
		}
		data, err := os.ReadFile(p)
		if err != nil {
			descErr = err
			return
		}
		descErr = json.Unmarshal(data, &desc)
	})
	if descErr != nil {
		t.Fatalf("read OpenAPI description: %v", descErr)
	}
	op, ok := desc.Paths[path]["get"]
	if !ok {
		t.Fatalf("GET %s not in the OpenAPI description", path)
	}
	params := map[string]bool{}
	var sorts []string
	for _, p := range op.Parameters {
		if p.Ref != "" {
			p = desc.Components.Parameters[p.Ref[strings.LastIndex(p.Ref, "/")+1:]]
		}
		params[p.Name] = true
		if p.Name != "sort" {
			continue
		}
		enum := p.Schema.Enum
		if p.Schema.Items != nil {
			enum = p.Schema.Items.Enum
		}
		for _, v := range enum {
			if !strings.Contains(v, ":") {
				sorts = append(sorts, v)
			}
		}
	}
	return params, sorts
}

type descParam struct {
	Ref    string `json:"$ref"`
	Name   string `json:"name"`
	Schema struct {
		Enum  []string `json:"enum"`
		Items *struct {
			Enum []string `json:"enum"`
		} `json:"items"`
	} `json:"schema"`
}

var (
	descOnce sync.Once
	descErr  error
	desc     struct {
		Paths map[string]map[string]struct {
			Parameters []descParam `json:"parameters"`
		} `json:"paths"`
		Components struct {
			Parameters map[string]descParam `json:"parameters"`
		} `json:"components"`
	}
)

// compareField orders two values of one JSON field: numbers numerically,
// timestamps by time, other strings lexically. ok is false when one is null.
func compareField(a, b any) (int, bool) {
	switch x := a.(type) {
	case float64:
		y, ok := b.(float64)
		if !ok {
			return 0, false
		}
		switch {
		case x < y:
			return -1, true
		case x > y:
			return 1, true
		}
		return 0, true
	case string:
		y, ok := b.(string)
		if !ok {
			return 0, false
		}
		ta, ea := time.Parse(time.RFC3339, x)
		tb, eb := time.Parse(time.RFC3339, y)
		if ea == nil && eb == nil {
			return ta.Compare(tb), true
		}
		return strings.Compare(x, y), true
	}
	return 0, false
}

type sortKey struct {
	field string
	desc  bool
}

// checkOrder reports the first pair of items out of the order keys give.
func checkOrder(t *testing.T, query string, items []any, keys []sortKey) {
	t.Helper()
	for i := 1; i < len(items); i++ {
		a, _ := items[i-1].(map[string]any)
		b, _ := items[i].(map[string]any)
		for _, k := range keys {
			c, ok := compareField(a[k.field], b[k.field])
			if !ok {
				break
			}
			if k.desc {
				c = -c
			}
			if c < 0 {
				break
			}
			if c > 0 {
				t.Errorf("%s: item %d (%s %v) before item %d (%s %v)", query,
					idOf(a), k.field, a[k.field], idOf(b), k.field, b[k.field])
				return
			}
		}
	}
}

func testListFilterSort(t *testing.T) {
	env := newFullEnv(t)
	prod := map[string]string{"env": "prod"}
	dev := map[string]string{"env": "dev"}
	for _, r := range labeledTypes {
		create(t, env, r, "alpha", prod, nil)
		create(t, env, r, "beta", dev, nil)
		create(t, env, r, "gamma", nil, nil)
	}
	server := firstID(t, env, "/servers", "servers")
	for _, del := range []bool{true, false} {
		status, m := call(t, env, http.MethodPost, fmt.Sprintf("/servers/%d/actions/change_protection", server),
			map[string]any{"delete": del})
		if status != http.StatusCreated {
			t.Fatalf("change_protection: status %d: %v", status, m)
		}
		env.For(t).WaitAction(idOf(m["action"]))
	}

	// filter is one of the endpoint's own documented filters: param=value
	// keeps exactly the items whose field equals value.
	type filter struct{ param, field, value string }
	endpoints := []struct {
		path, descPath, key string
		filters             func(full []any) []filter
	}{
		{"/ssh_keys", "/ssh_keys", "ssh_keys", func(full []any) []filter {
			fp, _ := full[0].(map[string]any)["fingerprint"].(string)
			return []filter{{"fingerprint", "fingerprint", fp}}
		}},
		{"/servers", "/servers", "servers", func([]any) []filter {
			return []filter{{"status", "status", "running"}, {"status", "status", "off"}}
		}},
		{"/placement_groups", "/placement_groups", "placement_groups", func([]any) []filter {
			return []filter{{"type", "type", "spread"}}
		}},
		{"/server_types", "/server_types", "server_types", nil},
		{"/locations", "/locations", "locations", nil},
		{"/images", "/images", "images", func([]any) []filter {
			return []filter{{"type", "type", "system"}, {"type", "type", "snapshot"}, {"status", "status", "available"}}
		}},
		{"/servers/actions", "/servers/actions", "actions", func([]any) []filter {
			return []filter{{"status", "status", "success"}, {"status", "status", "running"}}
		}},
		{fmt.Sprintf("/servers/%d/actions", server), "/servers/{id}/actions", "actions", func([]any) []filter {
			return []filter{{"status", "status", "success"}, {"status", "status", "error"}}
		}},
	}
	for _, ep := range endpoints {
		t.Run(ep.path, func(t *testing.T) {
			e := env.For(t)
			params, sorts := listParams(t, ep.descPath)
			get := func(query string) (int, []any, map[string]any) {
				t.Helper()
				status, m := call(t, e, http.MethodGet, ep.path+"?per_page=50&"+query, nil)
				items, _ := m[ep.key].([]any)
				return status, items, m
			}
			_, full, _ := get("")
			if len(full) < 2 {
				t.Fatalf("%d items, need two", len(full))
			}
			// expect checks that query returns exactly the items of full
			// that keep says, in any order.
			expect := func(query string, keep func(item map[string]any) bool) {
				t.Helper()
				status, items, m := get(query)
				if status != http.StatusOK {
					t.Errorf("%s: status %d: %v", query, status, m)
					return
				}
				var want []int64
				for _, it := range full {
					if keep(it.(map[string]any)) {
						want = append(want, idOf(it))
					}
				}
				got := ids(items)
				if !sameSet(got, want) {
					t.Errorf("%s: items %v, want %v", query, got, want)
				}
			}

			if params["name"] {
				name, _ := full[1].(map[string]any)["name"].(string)
				expect("name="+url.QueryEscape(name), func(it map[string]any) bool { return it["name"] == name })
			}
			if params["label_selector"] {
				expect("label_selector="+url.QueryEscape("env=prod"), func(it map[string]any) bool {
					l, _ := it["labels"].(map[string]any)
					return l["env"] == "prod"
				})
				expect("label_selector="+url.QueryEscape("!env"), func(it map[string]any) bool {
					l, _ := it["labels"].(map[string]any)
					_, has := l["env"]
					return !has
				})
				status, _, m := get("label_selector=" + url.QueryEscape("!!!=="))
				if status != http.StatusUnprocessableEntity || errCode(m) != "invalid_input" {
					t.Errorf("malformed label_selector: status %d code %q, want 422 invalid_input", status, errCode(m))
				}
			}
			if ep.filters != nil {
				for _, f := range ep.filters(full) {
					if !params[f.param] {
						t.Errorf("filter %s is not documented for GET %s", f.param, ep.descPath)
						continue
					}
					expect(f.param+"="+url.QueryEscape(f.value), func(it map[string]any) bool { return it[f.field] == f.value })
				}
			}
			for _, k := range sorts {
				for _, q := range []struct {
					query string
					desc  bool
				}{{k, false}, {k + ":asc", false}, {k + ":desc", true}} {
					status, items, m := get("sort=" + q.query)
					if status != http.StatusOK {
						t.Errorf("sort=%s: status %d: %v", q.query, status, m)
						continue
					}
					if len(items) != len(full) {
						t.Errorf("sort=%s: %d items, want %d", q.query, len(items), len(full))
					}
					checkOrder(t, "sort="+q.query, items, []sortKey{{k, q.desc}})
				}
			}
			if len(sorts) >= 2 {
				// Several keys in order: the last documented key first, ID
				// descending among equal values.
				k := sorts[len(sorts)-1]
				query := "sort=" + k + ":asc&sort=id:desc"
				status, items, m := get(query)
				if status != http.StatusOK {
					t.Errorf("%s: status %d: %v", query, status, m)
				} else {
					checkOrder(t, query, items, []sortKey{{k, false}, {"id", true}})
				}
			}
			if params["sort"] {
				status, _, m := get("sort=no_such_field")
				if status != http.StatusUnprocessableEntity || errCode(m) != "invalid_input" {
					t.Errorf("undocumented sort key: status %d code %q, want 422 invalid_input", status, errCode(m))
				}
			}
		})
	}
	listNameAndIDDesc(t, env)
}

// listNameAndIDDesc checks, for the network, IP and firewall resources,
// one name filter and one sort by id:desc each.
func listNameAndIDDesc(t *testing.T, env *apitest.Env) {
	for _, r := range []struct {
		path, key, list string
		body            func(name string, n int) map[string]any
	}{
		{"/networks", "network", "networks", func(name string, n int) map[string]any {
			return map[string]any{"name": name, "ip_range": fmt.Sprintf("10.%d.0.0/16", n%250)}
		}},
		{"/primary_ips", "primary_ip", "primary_ips", func(name string, _ int) map[string]any {
			return map[string]any{"name": name, "type": "ipv4", "assignee_type": "server", "location": "fsn1"}
		}},
		{"/floating_ips", "floating_ip", "floating_ips", func(name string, _ int) map[string]any {
			return map[string]any{"name": name, "type": "ipv4", "home_location": "fsn1"}
		}},
		{"/firewalls", "firewall", "firewalls", func(name string, _ int) map[string]any {
			return map[string]any{"name": name}
		}},
	} {
		t.Run(r.path, func(t *testing.T) {
			e := env.For(t)
			var created []int64
			for _, name := range []string{"list-alpha", "list-beta", "list-gamma"} {
				status, m := call(t, e, http.MethodPost, r.path, r.body(name, nextN()))
				if status != http.StatusCreated {
					t.Fatalf("POST %s %q: status %d, want 201: %v", r.path, name, status, m)
				}
				if a := idOf(m["action"]); a != 0 {
					e.WaitAction(a)
				}
				created = append(created, idOf(m[r.key]))
			}
			status, m := call(t, e, http.MethodGet, r.path+"?name=list-beta", nil)
			items, _ := m[r.list].([]any)
			if got := ids(items); status != http.StatusOK || !sameSet(got, created[1:2]) {
				t.Errorf("name=list-beta: status %d items %v, want [%d]", status, got, created[1])
			}
			status, m = call(t, e, http.MethodGet, r.path+"?per_page=50&sort=id:desc", nil)
			items, _ = m[r.list].([]any)
			if status != http.StatusOK || len(items) < len(created) {
				t.Fatalf("sort=id:desc: status %d, %d items, want at least %d", status, len(items), len(created))
			}
			checkOrder(t, "sort=id:desc", items, []sortKey{{"id", true}})
		})
	}
}

func sameSet(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[int64]int{}
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		seen[x]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func labelsOf(obj map[string]any) map[string]any {
	l, _ := obj["labels"].(map[string]any)
	if l == nil {
		l = map[string]any{}
	}
	return l
}

func testLabelsOnResources(t *testing.T) {
	env := newFullEnv(t)
	invalid := map[string]string{"hetzner.cloud/reserved": "x"}
	for _, r := range labeledTypes {
		t.Run(r.typ, func(t *testing.T) {
			e := env.For(t)
			obj := create(t, e, r, "labeled", map[string]string{"a": "1", "b": "2"}, nil)
			if got := labelsOf(obj); !reflect.DeepEqual(got, map[string]any{"a": "1", "b": "2"}) {
				t.Errorf("labels after create %v, want a=1, b=2", got)
			}
			path := fmt.Sprintf("/%s/%d", r.typ, idOf(obj))
			current := func() map[string]any {
				t.Helper()
				status, m := call(t, e, http.MethodGet, path, nil)
				if status != http.StatusOK {
					t.Fatalf("GET %s: status %d", path, status)
				}
				o, _ := m[r.key].(map[string]any)
				return labelsOf(o)
			}

			status, m := call(t, e, http.MethodPut, path, map[string]any{"labels": map[string]string{"c": "3"}})
			if status != http.StatusOK {
				t.Fatalf("PUT with labels: status %d: %v", status, m)
			}
			if got := current(); !reflect.DeepEqual(got, map[string]any{"c": "3"}) {
				t.Errorf("labels after PUT with labels %v, want only c=3", got)
			}

			status, m = call(t, e, http.MethodPut, path, map[string]any{"name": "relabeled"})
			if status != http.StatusOK {
				t.Fatalf("PUT without labels: status %d: %v", status, m)
			}
			if got := current(); !reflect.DeepEqual(got, map[string]any{"c": "3"}) {
				t.Errorf("labels after PUT without labels %v, want c=3 kept", got)
			}

			body := r.body("bad-labels", nextN())
			body["labels"] = invalid
			for _, req := range []struct {
				method, path string
				body         any
			}{
				{http.MethodPost, "/" + r.typ, body},
				{http.MethodPut, path, map[string]any{"labels": invalid}},
			} {
				status, m := call(t, e, req.method, req.path, req.body)
				if status != http.StatusUnprocessableEntity || errCode(m) != "invalid_input" ||
					!reflect.DeepEqual(errFields(m), []string{"labels"}) {
					t.Errorf("%s %s with invalid labels: status %d code %q fields %v, want 422 invalid_input on labels",
						req.method, req.path, status, errCode(m), errFields(m))
				}
			}
			if got := current(); !reflect.DeepEqual(got, map[string]any{"c": "3"}) {
				t.Errorf("labels after refused PUT %v, want c=3 kept", got)
			}
			status, m = call(t, e, http.MethodGet, "/"+r.typ+"?name=bad-labels", nil)
			if items, _ := m[r.typ].([]any); status != http.StatusOK || len(items) != 0 {
				t.Errorf("refused create stored %d items (status %d)", len(items), status)
			}
		})
	}
}

func testNameUniqueness(t *testing.T) {
	env := newFullEnv(t)
	q := env.NewToken("q", false)
	for _, r := range labeledTypes {
		t.Run(r.typ, func(t *testing.T) {
			e := env.For(t)
			dup := create(t, e, r, "dup", nil, nil)
			other := create(t, e, r, "other", nil, nil)

			status, m := call(t, e, http.MethodPost, "/"+r.typ, r.body("dup", nextN()))
			if status != http.StatusConflict || errCode(m) != "uniqueness_error" {
				t.Errorf("create with a used name: status %d code %q, want 409 uniqueness_error", status, errCode(m))
			}
			status, m = call(t, e, http.MethodPut, fmt.Sprintf("/%s/%d", r.typ, idOf(other)), map[string]any{"name": "dup"})
			if status != http.StatusConflict || errCode(m) != "uniqueness_error" {
				t.Errorf("PUT to a used name: status %d code %q, want 409 uniqueness_error", status, errCode(m))
			}
			status, m = call(t, e, http.MethodPut, fmt.Sprintf("/%s/%d", r.typ, idOf(dup)), map[string]any{"name": "dup"})
			if status != http.StatusOK {
				t.Errorf("PUT keeping the own name: status %d: %v", status, m)
			}
			create(t, as(t, env, q), r, "dup", nil, nil)
		})
	}
	t.Run("firewalls", func(t *testing.T) {
		e := env.For(t)
		status, m := call(t, e, http.MethodPost, "/firewalls", map[string]any{"name": "dup-fw"})
		if status != http.StatusCreated {
			t.Fatalf("POST /firewalls: status %d, want 201: %v", status, m)
		}
		status, m = call(t, e, http.MethodPost, "/firewalls", map[string]any{"name": "dup-fw"})
		if status != http.StatusConflict || errCode(m) != "uniqueness_error" {
			t.Errorf("create with a used name: status %d code %q, want 409 uniqueness_error", status, errCode(m))
		}
	})
}
