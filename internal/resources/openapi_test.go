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
	})
}

func fixed(p string) func() string { return func() string { return p } }
