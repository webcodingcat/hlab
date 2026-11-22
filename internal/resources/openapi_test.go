package resources_test

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/http"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	actionsres "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/datacenters"
	imagesres "github.com/webcodingcat/hlab/internal/resources/images"
	"github.com/webcodingcat/hlab/internal/resources/locations"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/resources/servertypes"
	"github.com/webcodingcat/hlab/internal/resources/sshkeys"
)

// ed25519PublicKey returns a well-formed OpenSSH ed25519 public key line.
func ed25519PublicKey() string {
	var wire []byte
	for _, part := range [][]byte{[]byte("ssh-ed25519"), make([]byte, 32)} {
		wire = binary.BigEndian.AppendUint32(wire, uint32(len(part)))
		wire = append(wire, part...)
	}
	for i := range 32 {
		wire[len(wire)-32+i] = byte(i + 1)
	}
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(wire) + " api-10"
}

func TestResources(t *testing.T) {
	conventionRows(t)
	t.Run("API-10", func(t *testing.T) {
		cat, err := catalog.Load("../../catalog")
		if err != nil {
			t.Fatalf("load shipped catalog: %v", err)
		}
		env := apitest.New(t,
			actionsres.New(),
			locations.New(cat),
			datacenters.New(),
			servertypes.New(cat),
			imagesres.New(cat),
			sshkeys.New(),
			servers.New(apitest.ServersDeps(t, cat, platformtest.NewFake())),
		)

		// IDs created by earlier cases and used by later ones.
		var keyID, serverID, createAction, deleteAction int64

		type idBody struct {
			SSHKey *struct {
				ID int64 `json:"id"`
			} `json:"ssh_key"`
			Server *struct {
				ID int64 `json:"id"`
			} `json:"server"`
			Action *struct {
				ID     int64  `json:"id"`
				Status string `json:"status"`
			} `json:"action"`
		}

		cases := []struct {
			name   string
			auth   *string // nil: the read/write token of project p
			method string
			path   func() string
			body   any
			want   int
			after  func(t *testing.T, b idBody)
		}{
			{name: "GET /locations", method: "GET", path: fixed("/locations"), want: 200},
			{name: "GET /datacenters", method: "GET", path: fixed("/datacenters"), want: 410},
			{name: "GET /server_types", method: "GET", path: fixed("/server_types"), want: 200},
			{name: "GET /images", method: "GET", path: fixed("/images"), want: 200},
			{name: "POST /ssh_keys", method: "POST", path: fixed("/ssh_keys"),
				body: map[string]any{"name": "key-1", "public_key": ed25519PublicKey()}, want: 201,
				after: func(t *testing.T, b idBody) {
					if b.SSHKey == nil {
						t.Fatal("no ssh_key in the response")
					}
					keyID = b.SSHKey.ID
				}},
			{name: "GET /ssh_keys", method: "GET", path: fixed("/ssh_keys"), want: 200},
			{name: "GET /ssh_keys/{id}", method: "GET", path: func() string { return fmt.Sprintf("/ssh_keys/%d", keyID) }, want: 200},
			{name: "POST /servers", method: "POST", path: fixed("/servers"),
				body: map[string]any{
					"name": "srv-1", "server_type": "cx23", "image": "ubuntu-24.04", "location": "fsn1",
					"start_after_create": true,
				}, want: 201,
				after: func(t *testing.T, b idBody) {
					if b.Server == nil || b.Action == nil {
						t.Fatal("no server or action in the response")
					}
					serverID, createAction = b.Server.ID, b.Action.ID
				}},
			{name: "GET /actions/{id}", method: "GET", path: func() string { return fmt.Sprintf("/actions/%d", createAction) }, want: 200},
			{name: "GET /servers", method: "GET", path: fixed("/servers"), want: 200},
			{name: "GET /servers/{id}", method: "GET", path: func() string { return fmt.Sprintf("/servers/%d", serverID) }, want: 200},
			{name: "DELETE /servers/{id}", method: "DELETE", path: func() string { return fmt.Sprintf("/servers/%d", serverID) }, want: 200,
				after: func(t *testing.T, b idBody) {
					if b.Action == nil {
						t.Fatal("no action in the response")
					}
					deleteAction = b.Action.ID
				}},
			{name: "GET /actions?id=..&id=..", method: "GET", path: func() string {
				return fmt.Sprintf("/actions?id=%d&id=%d&sort=status&sort=id", createAction, deleteAction)
			}, want: 200},
			{name: "DELETE /ssh_keys/{id}", method: "DELETE", path: func() string { return fmt.Sprintf("/ssh_keys/%d", keyID) }, want: 204},
			{name: "401", auth: new(""), method: "GET", path: fixed("/locations"), want: 401},
			{name: "404", method: "GET", path: fixed("/servers/987654321"), want: 404},
		}

		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				e := env.For(t)
				if c.name == "DELETE /servers/{id}" {
					// The create action locks the server until it finishes.
					e.WaitAction(createAction)
				}
				var resp *http.Response
				if c.auth != nil {
					resp = e.DoAuth(*c.auth, c.method, c.path(), c.body)
				} else {
					resp = e.Do(c.method, c.path(), c.body)
				}
				if resp.StatusCode != c.want {
					t.Fatalf("status %d, want %d", resp.StatusCode, c.want)
				}
				if c.after != nil {
					var b idBody
					apitest.Decode(t, resp, &b)
					c.after(t, b)
				}
			})
		}
		t.Run("storage_boxes", testStorageBoxesDescription)
	})
}

func fixed(p string) func() string { return func() string { return p } }

// testStorageBoxesDescription is TestResources/API-10/storage_boxes: every
// Storage Box route of the Hetzner API answers as hetzner.spec.json says,
// and a storage box action read through /v1/actions as cloud.spec.json
// says. apitest.Env.Do validates each response.
func testStorageBoxesDescription(t *testing.T) {
	env := newFullEnv(t)
	const hz = "/hetzner/v1"
	var typeID, box, sub, boxAction int64
	boxPath := func() string { return fmt.Sprintf("%s/storage_boxes/%d", hz, box) }
	subPath := func() string { return fmt.Sprintf("%s/subaccounts/%d", boxPath(), sub) }

	cases := []struct {
		name   string
		auth   *string // nil: the read/write token of project p
		method string
		path   func() string
		body   any
		want   int
		after  func(t *testing.T, m map[string]any)
	}{
		{name: "GET /storage_box_types", method: "GET", path: fixed(hz + "/storage_box_types"), want: 200,
			after: func(t *testing.T, m map[string]any) {
				items, _ := m["storage_box_types"].([]any)
				if len(items) == 0 {
					t.Fatal("no storage box types")
				}
				typeID = idOf(items[0])
			}},
		{name: "GET /storage_box_types/{id}", method: "GET", path: func() string {
			return fmt.Sprintf("%s/storage_box_types/%d", hz, typeID)
		}, want: 200},
		{name: "POST /storage_boxes", method: "POST", path: fixed(hz + "/storage_boxes"), body: map[string]any{
			"name": "box-api-10", "storage_box_type": "bx21", "location": "fsn1", "password": boxPassword,
			"labels": map[string]string{"env": "test"}, "ssh_keys": []string{ed25519PublicKey()},
			"access_settings": map[string]any{"samba_enabled": true},
		}, want: 201, after: func(t *testing.T, m map[string]any) {
			box, boxAction = idOf(m["storage_box"]), idOf(m["action"])
			if box == 0 || boxAction == 0 {
				t.Fatalf("no storage box or action in the response: %v", m)
			}
		}},
		{name: "GET /storage_boxes", method: "GET", path: fixed(hz + "/storage_boxes"), want: 200},
		{name: "GET /storage_boxes/{id}", method: "GET", path: boxPath, want: 200},
		{name: "PUT /storage_boxes/{id}", method: "PUT", path: boxPath,
			body: map[string]any{"name": "box-api-10b", "labels": map[string]string{"env": "prod"}}, want: 200},
		{name: "GET /storage_boxes/actions", method: "GET", path: fixed(hz + "/storage_boxes/actions"), want: 200},
		{name: "GET /storage_boxes/actions/{id}", method: "GET", path: func() string {
			return fmt.Sprintf("%s/storage_boxes/actions/%d", hz, boxAction)
		}, want: 200},
		{name: "GET /storage_boxes/{id}/actions", method: "GET", path: func() string { return boxPath() + "/actions" }, want: 200},
		{name: "GET /storage_boxes/{id}/actions/{action_id}", method: "GET", path: func() string {
			return fmt.Sprintf("%s/actions/%d", boxPath(), boxAction)
		}, want: 200},
		{name: "GET /v1/actions/{id}", method: "GET", path: func() string { return fmt.Sprintf("/actions/%d", boxAction) }, want: 200},
		{name: "GET /v1/actions?id=", method: "GET", path: func() string { return fmt.Sprintf("/actions?id=%d", boxAction) }, want: 200},
		{name: "POST change_protection", method: "POST", path: func() string { return boxPath() + "/actions/change_protection" },
			body: map[string]any{"delete": false}, want: 201},
		{name: "POST change_type", method: "POST", path: func() string { return boxPath() + "/actions/change_type" },
			body: map[string]any{"storage_box_type": "bx31"}, want: 201},
		{name: "POST reset_password", method: "POST", path: func() string { return boxPath() + "/actions/reset_password" },
			body: map[string]any{"password": "Other-Pass-456"}, want: 201},
		{name: "POST update_access_settings", method: "POST", path: func() string { return boxPath() + "/actions/update_access_settings" },
			body: map[string]any{"ssh_enabled": true, "zfs_enabled": true}, want: 201},
		{name: "POST enable_snapshot_plan", method: "POST", path: func() string { return boxPath() + "/actions/enable_snapshot_plan" },
			body: map[string]any{"max_snapshots": 5, "minute": 30, "hour": 3, "day_of_week": 7, "day_of_month": nil}, want: 201},
		{name: "GET /storage_boxes/{id} with snapshot plan", method: "GET", path: boxPath, want: 200},
		{name: "POST disable_snapshot_plan", method: "POST", path: func() string { return boxPath() + "/actions/disable_snapshot_plan" },
			want: 201},
		{name: "POST /storage_boxes/{id}/subaccounts", method: "POST", path: func() string { return boxPath() + "/subaccounts" },
			body: map[string]any{
				"home_directory": "backup/server01", "password": boxPassword, "description": "api-10",
				"labels": map[string]string{"env": "test"}, "access_settings": map[string]any{"readonly": true},
			}, want: 201, after: func(t *testing.T, m map[string]any) {
				sub = idOf(m["subaccount"])
				if sub == 0 || idOf(m["action"]) == 0 {
					t.Fatalf("no subaccount or action in the response: %v", m)
				}
			}},
		{name: "GET /storage_boxes/{id}/subaccounts", method: "GET", path: func() string { return boxPath() + "/subaccounts" }, want: 200},
		{name: "GET /storage_boxes/{id}/subaccounts/{subaccount_id}", method: "GET", path: subPath, want: 200},
		{name: "PUT /storage_boxes/{id}/subaccounts/{subaccount_id}", method: "PUT", path: subPath,
			body: map[string]any{"name": "sub-api-10", "description": "changed", "labels": map[string]string{"env": "prod"}}, want: 200},
		{name: "POST change_home_directory", method: "POST", path: func() string { return subPath() + "/actions/change_home_directory" },
			body: map[string]any{"home_directory": "backup/server02"}, want: 201},
		{name: "POST reset_subaccount_password", method: "POST", path: func() string { return subPath() + "/actions/reset_subaccount_password" },
			body: map[string]any{"password": "Other-Pass-456"}, want: 201},
		{name: "POST subaccount update_access_settings", method: "POST", path: func() string { return subPath() + "/actions/update_access_settings" },
			body: map[string]any{"readonly": false, "webdav_enabled": true}, want: 201},
		{name: "DELETE /storage_boxes/{id}/subaccounts/{subaccount_id}", method: "DELETE", path: subPath, want: 201},
		{name: "404", method: "GET", path: fixed(hz + "/storage_boxes/987654321"), want: 404},
		{name: "422", method: "POST", path: fixed(hz + "/storage_boxes"),
			body: map[string]any{"name": "box-bad", "storage_box_type": "no-such-type", "location": "fsn1", "password": boxPassword}, want: 422},
		{name: "401", auth: new(""), method: "GET", path: fixed(hz + "/storage_boxes"), want: 401},
		{name: "DELETE /storage_boxes/{id}", method: "DELETE", path: boxPath, want: 201},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := env.For(t)
			var resp *http.Response
			if c.auth != nil {
				resp = e.DoAuth(*c.auth, c.method, c.path(), c.body)
			} else {
				resp = e.Do(c.method, c.path(), c.body)
			}
			if resp.StatusCode != c.want {
				t.Fatalf("status %d, want %d", resp.StatusCode, c.want)
			}
			var m map[string]any
			apitest.Decode(t, resp, &m)
			if c.after != nil {
				c.after(t, m)
			}
			// Every box action locks the box: let it finish before the next
			// case.
			if a := idOf(m["action"]); a != 0 {
				e.WaitAction(a)
			}
		})
	}
}
