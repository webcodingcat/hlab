//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// catalogType is one catalog resource type as the three clients name it.
type catalogType struct {
	row    string // row name
	path   string // list endpoint
	key    string // list key of the endpoint
	hcloud string // hcloud command
	tofu   string // data source name without hcloud_ and the plural s
	plural string // list attribute of the plural data source
	module string // Ansible info module without _info
	pick   func(t *testing.T, e *env, all map[int64]string) int64
}

// pickName picks the item with the name; pickFirst the first by name.
func pickName(name string) func(t *testing.T, e *env, all map[int64]string) int64 {
	return func(t *testing.T, _ *env, all map[int64]string) int64 {
		t.Helper()
		for id, n := range all {
			if n == name {
				return id
			}
		}
		t.Fatalf("the API lists no %s", name)
		return 0
	}
}

func pickFirst(t *testing.T, _ *env, all map[int64]string) int64 {
	t.Helper()
	ids := slices.SortedFunc(maps.Keys(all), func(a, b int64) int { return strings.Compare(all[a], all[b]) })
	if len(ids) == 0 {
		t.Fatal("the API lists nothing")
	}
	return ids[0]
}

var catalogTypes = []catalogType{
	{row: "server_types", path: "/server_types", key: "server_types", hcloud: "server-type", tofu: "server_type", plural: "server_types", module: "server_type", pick: pickName(serverType)},
	{row: "locations", path: "/locations", key: "locations", hcloud: "location", tofu: "location", plural: "locations", module: "location", pick: pickName(serverLocation)},
	{row: "load_balancer_types", path: "/load_balancer_types", key: "load_balancer_types", hcloud: "load-balancer-type", tofu: "load_balancer_type", plural: "load_balancer_types", module: "load_balancer_type", pick: pickFirst},
	{row: "isos", path: "/isos", key: "isos", hcloud: "iso", module: "iso", pick: func(t *testing.T, e *env, _ map[int64]string) int64 { return e.firstISO(t).ID }},
}

func init() {
	for _, ct := range catalogTypes {
		registerRow(clientRow{Req: "CLI-01", Name: ct.row, Run: func(t *testing.T, c *rowCtx) { testCLI01Catalog(t, c.E, ct) }})
		if ct.tofu != "" {
			registerRow(clientRow{Req: "CLI-02", Name: ct.row, Run: func(t *testing.T, _ *rowCtx) { testCLI02Catalog(t, ct) }})
		}
		registerRow(clientRow{Req: "CLI-03", Name: ct.row, Run: func(t *testing.T, _ *rowCtx) { testCLI03Catalog(t, ct) }})
	}
}

// testCLI01Catalog: `hcloud <type> list` and `describe` by name and ID
// match the API.
func testCLI01Catalog(t *testing.T, e *env, ct catalogType) {
	want := e.apiNamed(t, ct.path, ct.key)
	var listed []named
	e.hcloudJSON(t, &listed, ct.hcloud, "list")
	if got := namedOf(listed); !maps.Equal(got, want) {
		t.Errorf("hcloud %s list: %v, the API lists %v", ct.hcloud, got, want)
	}
	id := ct.pick(t, e, want)
	for _, ref := range []string{want[id], strconv.FormatInt(id, 10)} {
		var d named
		e.hcloudJSON(t, &d, ct.hcloud, "describe", ref)
		if d.ID != id || d.Name != want[id] {
			t.Errorf("hcloud %s describe %s: %d %q, the API has %d %q", ct.hcloud, ref, d.ID, d.Name, id, want[id])
		}
	}
}

// testCLI02Catalog: data source hcloud_<type> by name and by ID and
// hcloud_<type>s match the API.
func testCLI02Catalog(t *testing.T, ct catalogType) {
	e := sharedTofu(t)
	want := e.apiNamed(t, ct.path, ct.key)
	id := ct.pick(t, e, want)
	d := e.newTofuDir(t, "catalog-"+ct.row)
	d.run(t, "init", "-no-color")
	d.writeMain(t, fmt.Sprintf(`data "hcloud_%[1]s" "by_name" {
  name = %[2]q
}

data "hcloud_%[1]s" "by_id" {
  id = %[3]d
}

data "hcloud_%[1]ss" "all" {}

output "by_name_id" {
  value = data.hcloud_%[1]s.by_name.id
}

output "by_id_name" {
  value = data.hcloud_%[1]s.by_id.name
}

output "all" {
  value = jsonencode([for x in data.hcloud_%[1]ss.all.%[4]s : { id = x.id, name = x.name }])
}
`, ct.tofu, want[id], id, ct.plural))
	d.run(t, "apply", "-auto-approve", "-no-color")
	if got := d.output(t, "by_name_id"); got != strconv.FormatInt(id, 10) {
		t.Errorf("data hcloud_%s by name %s: ID %s, the API has %d", ct.tofu, want[id], got, id)
	}
	if got := d.output(t, "by_id_name"); got != want[id] {
		t.Errorf("data hcloud_%s by ID %d: name %q, the API has %q", ct.tofu, id, got, want[id])
	}
	var all []named
	if err := json.Unmarshal([]byte(d.output(t, "all")), &all); err != nil {
		t.Fatalf("output all: %v", err)
	}
	if got := namedOf(all); !maps.Equal(got, want) {
		t.Errorf("data hcloud_%ss: %v, the API lists %v", ct.tofu, got, want)
	}
}

// testCLI03Catalog: <type>_info by name and without filter matches the API.
func testCLI03Catalog(t *testing.T, ct catalogType) {
	e, w := sharedAnsible(t)
	want := e.apiNamed(t, ct.path, ct.key)
	id := ct.pick(t, e, want)
	names := slices.Sorted(maps.Values(want))
	ret := "hcloud_" + ct.module + "_info"
	e.ansibleOK(t, w, "cli03-catalog-"+ct.row, localPlay(t, map[string]any{"id": id, "name": want[id], "names": names}, fmt.Sprintf(`    - name: all
      hetzner.hcloud.%[1]s_info:
      register: all
    - name: by name
      hetzner.hcloud.%[1]s_info:
        name: "{{ row.name }}"
      register: by_name
    - name: check the answers
      ansible.builtin.assert:
        that:
          - (all.%[2]s | map(attribute='name') | sort | list) == row.names
          - by_name.%[2]s | length == 1
          - by_name.%[2]s[0].id | int == row.id
          - by_name.%[2]s[0].name == row.name
`, ct.module, ret)))
}
