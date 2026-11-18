package servers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	diskfs "github.com/diskfs/go-diskfs"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/resources/sshkeys"
	"github.com/webcodingcat/hlab/internal/shacrypt"
	"github.com/webcodingcat/hlab/internal/store"
)

func TestServers(t *testing.T) {
	t.Run("SRV-04", func(t *testing.T) {
		cat, err := catalog.Load("../../../catalog")
		if err != nil {
			t.Fatalf("load shipped catalog: %v", err)
		}
		env := apitest.New(t, servers.New(servers.Deps{
			Catalog:     cat,
			PublicRange: netip.MustParsePrefix("198.19.0.0/16"),
			Machines:    apitest.NoopMachines{},
		}))

		var nets []netip.Prefix
		for _, name := range []string{"srv-a", "srv-b"} {
			resp := env.Do(http.MethodPost, "/servers", map[string]any{
				"name":               name,
				"server_type":        "cx23",
				"image":              "ubuntu-24.04",
				"location":           "fsn1",
				"start_after_create": true,
			})
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("POST /servers %s: status %d, want 201", name, resp.StatusCode)
			}
			var body struct {
				Server struct {
					PublicNet struct {
						IPv6 *struct {
							IP string `json:"ip"`
						} `json:"ipv6"`
					} `json:"public_net"`
				} `json:"server"`
			}
			apitest.Decode(t, resp, &body)
			if body.Server.PublicNet.IPv6 == nil {
				t.Fatalf("%s: public_net.ipv6 is null", name)
			}
			p, err := netip.ParsePrefix(body.Server.PublicNet.IPv6.IP)
			if err != nil || !p.Addr().Is6() || p.Bits() != 64 {
				t.Fatalf("%s: public_net.ipv6.ip = %q, want an IPv6 /64", name, body.Server.PublicNet.IPv6.IP)
			}
			nets = append(nets, p.Masked())
		}
		if nets[0] == nets[1] {
			t.Errorf("both servers got IPv6 %s, want distinct /64s", nets[0])
		}
	})

	t.Run("CAT-04", func(t *testing.T) {
		env := newEnv(t, apitest.NoopMachines{})
		resp := env.Do(http.MethodPost, "/servers", map[string]any{
			"name":        "arm",
			"server_type": "cax11",
			"image":       "ubuntu-24.04",
			"location":    "fsn1",
		})
		if resp.StatusCode != http.StatusPreconditionFailed {
			t.Fatalf("POST /servers cax11: status %d, want 412", resp.StatusCode)
		}
		if code := apitest.ErrorCode(t, resp); code != "resource_unavailable" {
			t.Errorf("POST /servers cax11: error.code %q, want resource_unavailable", code)
		}
		var list struct {
			Servers []struct {
				ID int64 `json:"id"`
			} `json:"servers"`
		}
		apitest.Decode(t, env.Do(http.MethodGet, "/servers", nil), &list)
		if len(list.Servers) != 0 {
			t.Errorf("GET /servers lists %d servers after the refused create, want none", len(list.Servers))
		}
		if acts := listActions(t, env, "/servers/actions"); len(acts) != 0 {
			t.Errorf("GET /servers/actions lists %d actions after the refused create, want none", len(acts))
		}
	})

	t.Run("API-08", func(t *testing.T) {
		machines := apitest.NewBlockingMachines()
		env := newEnv(t, machines)
		a, actA := createServer(t, env, "act-a", nil)
		b, actB := createServer(t, env, "act-b", nil)

		running := env.GetAction(actA)
		if running.Status != "running" || running.Progress >= 100 || running.Finished != nil {
			t.Errorf("create action while its work blocks: status %q, progress %d, finished %v; want running, below 100, null",
				running.Status, running.Progress, running.Finished)
		}

		machines.Release()
		done := env.WaitAction(actA)
		env.WaitAction(actB)
		if done.Status != "success" || done.Progress != 100 || done.Finished == nil {
			t.Errorf("create action after its work: status %q, progress %d, finished %v; want success, 100, set",
				done.Status, done.Progress, done.Finished)
		}

		want := rawAction(t, env, fmt.Sprintf("/actions/%d", actA))
		for _, path := range []string{"/servers/actions", fmt.Sprintf("/servers/%d/actions", a)} {
			var found map[string]any
			for _, act := range listActions(t, env, path) {
				if id, _ := act["id"].(float64); int64(id) == actA {
					found = act
				}
			}
			if !reflect.DeepEqual(found, want) {
				t.Errorf("GET %s: action %d = %v, want %v", path, actA, found, want)
			}
		}
		path := fmt.Sprintf("/servers/%d/actions/%d", a, actA)
		if got := rawAction(t, env, path); !reflect.DeepEqual(got, want) {
			t.Errorf("GET %s = %v, want %v", path, got, want)
		}

		path = fmt.Sprintf("/servers/%d/actions/%d", a, actB)
		resp := env.Do(http.MethodGet, path, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s (action of server %d): status %d, want 404", path, b, resp.StatusCode)
		}
		if code := apitest.ErrorCode(t, resp); code != "not_found" {
			t.Errorf("GET %s: error.code %q, want not_found", path, code)
		}

		failing := apitest.NewBlockingMachines()
		fenv := newEnv(t, failing)
		_, actF := createServer(t, fenv, "act-fail", nil)
		failing.Fail()
		failed := fenv.WaitAction(actF)
		if failed.Status != "error" || failed.Error == nil || failed.Error.Code == "" || failed.Error.Message == "" {
			t.Errorf("create action with failing work: status %q, error %+v; want error with code and message",
				failed.Status, failed.Error)
		}
	})

	t.Run("API-12", func(t *testing.T) {
		env := newEnv(t, apitest.NoopMachines{})
		id, act := createServer(t, env, "prot", nil)
		env.WaitAction(act)

		changeProtection(t, env, id, map[string]any{"delete": true})
		if p := serverProtection(t, env, id); !p.Delete {
			t.Errorf("protection after change_protection delete=true: %+v, want delete true", p)
		}
		resp := env.Do(http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil)
		if resp.StatusCode != http.StatusLocked {
			t.Errorf("DELETE protected server: status %d, want 423", resp.StatusCode)
		}
		if code := apitest.ErrorCode(t, resp); code != "protected" {
			t.Errorf("DELETE protected server: error.code %q, want protected", code)
		}
		if resp := env.Do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("GET server after refused delete: status %d, want 200", resp.StatusCode)
		}

		changeProtection(t, env, id, map[string]any{"delete": false})
		if p := serverProtection(t, env, id); p.Delete {
			t.Errorf("protection after change_protection delete=false: %+v, want delete false", p)
		}
		deleteServer(t, env, id)
	})

	t.Run("API-13", func(t *testing.T) {
		machines := apitest.NewBlockingMachines()
		env := newEnv(t, machines)
		id, act := createServer(t, env, "lock", nil)

		resp := env.Do(http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil)
		if resp.StatusCode != http.StatusLocked {
			t.Errorf("DELETE during create: status %d, want 423", resp.StatusCode)
		}
		if code := apitest.ErrorCode(t, resp); code != "locked" {
			t.Errorf("DELETE during create: error.code %q, want locked", code)
		}
		resp = env.Do(http.MethodPost, fmt.Sprintf("/servers/%d/actions/change_protection", id), map[string]any{"rebuild": true})
		if resp.StatusCode != http.StatusLocked {
			t.Errorf("change_protection during create: status %d, want 423", resp.StatusCode)
		}
		if code := apitest.ErrorCode(t, resp); code != "locked" {
			t.Errorf("change_protection during create: error.code %q, want locked", code)
		}

		machines.Release()
		env.WaitAction(act)
		changeProtection(t, env, id, map[string]any{"rebuild": true})
		deleteServer(t, env, id)
	})

	t.Run("SRV-17", func(t *testing.T) {
		env := newEnv(t, apitest.NoopMachines{})
		id, act := createServer(t, env, "console", nil)
		env.WaitAction(act)
		path := fmt.Sprintf("/servers/%d/actions", id)
		before := len(listActions(t, env, path))

		resp := env.Do(http.MethodPost, fmt.Sprintf("/servers/%d/actions/request_console", id), nil)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("request_console: status %d, want 422", resp.StatusCode)
		}
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		apitest.Decode(t, resp, &body)
		if body.Error.Code != "unsupported_error" || body.Error.Message != "server console is not supported by hlab yet" {
			t.Errorf("request_console: error %+v, want unsupported_error with the SPEC message", body.Error)
		}
		if after := len(listActions(t, env, path)); after != before {
			t.Errorf("GET %s: %d actions after request_console, want %d", path, after, before)
		}
	})

	t.Run("SRV-18/create", func(t *testing.T) {
		ramMB, diskBytes := refSizes(t)
		unit := diskBytes / 40 // bytes per catalog GB; cx23 has 40 GB
		reserveRAM := int64(reserveRAMMB)
		reserveDisk := reserveDiskGB * unit

		// Zero fields keep the roomy value; the existing server's disk file
		// does not exist on the fake platform, so its whole nominal size is
		// remaining growth.
		cases := []struct {
			name      string
			existing  bool // an off cx23 server exists before the create
			cores     int
			freeRAMMB int64
			freeDisk  int64
			ok        bool
		}{
			{name: "cores_above", cores: 1},
			{name: "cores_at", cores: 2, ok: true},
			{name: "ram_above", freeRAMMB: reserveRAM + ramMB - 1},
			{name: "ram_at", freeRAMMB: reserveRAM + ramMB, ok: true},
			{name: "ram_at_stopped_server_holds_none", existing: true, freeRAMMB: reserveRAM + ramMB, ok: true},
			{name: "disk_above", freeDisk: reserveDisk + diskBytes - 1},
			{name: "disk_at", freeDisk: reserveDisk + diskBytes, ok: true},
			{name: "disk_growth_above", existing: true, freeDisk: reserveDisk + 2*diskBytes - 1},
			{name: "disk_growth_at", existing: true, freeDisk: reserveDisk + 2*diskBytes, ok: true},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				fake := platformtest.NewFake()
				env, _, _ := newFakeEnv(t, fake, nil)
				if c.existing {
					createdServer(t, env, "existing", map[string]any{"start_after_create": false})
				}
				r := roomy
				if c.cores != 0 {
					r.LogicalCPUs = c.cores
				}
				if c.freeRAMMB != 0 {
					r.FreeMemoryMB = c.freeRAMMB
				}
				if c.freeDisk != 0 {
					r.FreeDiskBytes = c.freeDisk
				}
				fake.SetResources(r)
				servers0, actions0 := countServers(t, env), len(listActions(t, env, "/servers/actions"))

				resp := env.Do(http.MethodPost, "/servers", map[string]any{
					"name": "limit", "server_type": "cx23", "image": "ubuntu-24.04", "location": "fsn1",
				})
				if c.ok {
					if resp.StatusCode != http.StatusCreated {
						t.Fatalf("POST /servers at the limit: status %d (%s), want 201", resp.StatusCode, apitest.ErrorCode(t, resp))
					}
					var body struct {
						Action apitest.Action `json:"action"`
					}
					apitest.Decode(t, resp, &body)
					if a := env.WaitAction(body.Action.ID); a.Status != "success" {
						t.Errorf("create action at the limit: %q %+v, want success", a.Status, a.Error)
					}
					return
				}
				wantError(t, resp, http.StatusPreconditionFailed, "resource_unavailable", "POST /servers above the limit")
				if n := countServers(t, env); n != servers0 {
					t.Errorf("GET /servers lists %d servers after the refused create, want %d", n, servers0)
				}
				if n := len(listActions(t, env, "/servers/actions")); n != actions0 {
					t.Errorf("GET /servers/actions lists %d actions after the refused create, want %d", n, actions0)
				}
			})
		}

		t.Run("shrunk_after_request", func(t *testing.T) {
			shrunk := roomy
			shrunk.FreeMemoryMB = 0
			p := &shrinking{Fake: platformtest.NewFake(), after: shrunk}
			env, _, _ := newFakeEnv(t, p, nil)
			_, act := createServer(t, env, "shrunk", nil)
			a := env.WaitAction(act)
			if a.Status != "error" || a.Error == nil || a.Error.Code != "resource_unavailable" {
				t.Errorf("create action after the resources shrank: %q %+v, want error resource_unavailable", a.Status, a.Error)
			}
		})
	})

	t.Run("SRV-20/power", func(t *testing.T) {
		fake := platformtest.NewFake()
		env, mod, _ := newFakeEnv(t, fake, nil)

		releaseCreate := block(t, fake, "CreateVM")
		releaseStart := block(t, fake, "Power")
		id, act := createServer(t, env, "power", nil)
		waitStatus(t, env, id, "initializing")
		releaseCreate()
		waitStatus(t, env, id, "starting")
		releaseStart()
		if a := env.WaitAction(act); a.Status != "success" {
			t.Fatalf("create action: %q %+v, want success", a.Status, a.Error)
		}
		waitStatus(t, env, id, "running")
		vm := vmName(t, fake)

		for _, s := range []struct{ action, during, after string }{
			{"poweroff", "stopping", "off"},
			{"poweron", "starting", "running"},
			{"reboot", "", "running"},
			{"reset", "", "running"},
		} {
			release := block(t, fake, "Power")
			act, _ := startAction(t, env, id, s.action, nil)
			if s.during != "" {
				waitStatus(t, env, id, s.during)
			}
			expectLocked(t, env, id, s.action)
			release()
			if a := env.WaitAction(act); a.Status != "success" {
				t.Fatalf("%s action: %q %+v, want success", s.action, a.Status, a.Error)
			}
			if got := getServer(t, env, id).Status; got != s.after {
				t.Errorf("status after %s: %q, want %q", s.action, got, s.after)
			}
		}

		fake.HoldShutdown(vm)
		release := block(t, fake, "Power")
		act, _ = startAction(t, env, id, "shutdown", nil)
		waitStatus(t, env, id, "stopping")
		expectLocked(t, env, id, "shutdown")
		release()
		if a := env.WaitAction(act); a.Status != "success" {
			t.Fatalf("shutdown action once sent: %q %+v, want success", a.Status, a.Error)
		}
		if got := getServer(t, env, id).Status; got != "stopping" {
			t.Errorf("status after shutdown while the guest holds the VM: %q, want stopping", got)
		}
		fake.SetState(vm, platform.StateOff)
		tickWatch(t, mod)
		if got := getServer(t, env, id).Status; got != "off" {
			t.Errorf("status one watch interval after the VM went off: %q, want off", got)
		}

		fake.SetState(vm, platform.StateRunning)
		tickWatch(t, mod)
		if got := getServer(t, env, id).Status; got != "running" {
			t.Errorf("status one watch interval after the guest turned the VM on: %q, want running", got)
		}
		fake.SetState(vm, platform.StateOff)
		tickWatch(t, mod)
		if got := getServer(t, env, id).Status; got != "off" {
			t.Errorf("status one watch interval after the guest turned the VM off: %q, want off", got)
		}

		runAction(t, env, id, "poweron", nil)
		release = block(t, fake, "Power")
		act, _ = startAction(t, env, id, "poweroff", nil)
		waitStatus(t, env, id, "stopping")
		tickWatch(t, mod)
		if got := getServer(t, env, id).Status; got != "stopping" {
			t.Errorf("status after a watch interval during poweroff: %q, want stopping", got)
		}
		release()
		if a := env.WaitAction(act); a.Status != "success" {
			t.Fatalf("poweroff action: %q %+v, want success", a.Status, a.Error)
		}

		release = block(t, fake, "DeleteVM")
		resp := env.Do(http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("DELETE /servers/%d: status %d, want 200", id, resp.StatusCode)
		}
		var del struct {
			Action apitest.Action `json:"action"`
		}
		apitest.Decode(t, resp, &del)
		waitStatus(t, env, id, "deleting")
		release()
		if a := env.WaitAction(del.Action.ID); a.Status != "success" {
			t.Errorf("delete action: %q %+v, want success", a.Status, a.Error)
		}
	})

	t.Run("SRV-02", func(t *testing.T) {
		fake := platformtest.NewFake()
		env, _, deps := newFakeEnv(t, fake, nil)

		_, act, raw := createWithPassword(t, env, "pw", nil)
		if a := env.WaitAction(act); a.Status != "success" {
			t.Fatalf("create action: %q %+v, want success", a.Status, a.Error)
		}
		var password string
		if err := json.Unmarshal(raw, &password); err != nil || password == "" {
			t.Fatalf("root_password without SSH keys = %s, want a non-empty string", raw)
		}

		var docs []json.RawMessage
		err := env.Store.Tx(func(tx store.Tx) error {
			var err error
			docs, err = store.ListAll[json.RawMessage](tx, servers.Kind)
			return err
		})
		if err != nil || len(docs) != 1 {
			t.Fatalf("stored servers: %d, %v; want one", len(docs), err)
		}
		checkCrypt(t, "stored server record", docs[0], password)

		vendor := seedFile(t, deps.Settings.DataFolder, "vendor-data")
		checkCrypt(t, "seed vendor-data", vendor, password)
		for _, want := range []struct{ what, re string }{
			{"ssh_pwauth: true", `ssh_pwauth:\s*true`},
			{"chpasswd", `chpasswd:`},
			{"expire: false", `expire:\s*false`},
			{"PermitRootLogin yes drop-in", `/etc/ssh/sshd_config\.d/\S+\.conf`},
			{"PermitRootLogin yes", `PermitRootLogin yes`},
			{"hlab-guest timer", `hlab-guest\.timer`},
		} {
			if !regexp.MustCompile(want.re).Match(vendor) {
				t.Errorf("vendor-data lacks %s:\n%s", want.what, vendor)
			}
		}

		resp := env.Do(http.MethodPost, "/ssh_keys", map[string]any{"name": "k", "public_key": testPublicKey})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST /ssh_keys: status %d, want 201", resp.StatusCode)
		}
		var key struct {
			SSHKey struct {
				ID int64 `json:"id"`
			} `json:"ssh_key"`
		}
		apitest.Decode(t, resp, &key)
		_, act, raw = createWithPassword(t, env, "keyed", map[string]any{"ssh_keys": []any{key.SSHKey.ID}})
		env.WaitAction(act)
		if string(raw) != "null" {
			t.Errorf("root_password with SSH keys = %s, want null", raw)
		}
	})

	t.Run("SRV-20/rebuild", func(t *testing.T) {
		fake := platformtest.NewFake()
		env, _, _ := newFakeEnv(t, fake, nil)
		id := createdServer(t, env, "rebuild", nil)
		rebuild := map[string]any{"image": "ubuntu-24.04"}

		release := block(t, fake, "CreateDisk")
		act, _ := startAction(t, env, id, "rebuild", rebuild)
		waitStatus(t, env, id, "rebuilding")
		expectLocked(t, env, id, "rebuild")
		release()
		if a := env.WaitAction(act); a.Status != "success" {
			t.Fatalf("rebuild action: %q %+v, want success", a.Status, a.Error)
		}
		if got := getServer(t, env, id).Status; got != "running" {
			t.Errorf("status after rebuild: %q, want running", got)
		}

		fake.FailNext("CreateDisk", &platform.Error{Kind: platform.Failed, Op: "CreateDisk", Err: errors.New("injected failure")})
		act, _ = startAction(t, env, id, "rebuild", rebuild)
		a := env.WaitAction(act)
		if a.Status != "error" || a.Error == nil {
			t.Errorf("rebuild action with a failing platform: %q %+v, want error", a.Status, a.Error)
		}
		if got := getServer(t, env, id).Status; got != "off" {
			t.Errorf("status after the failed rebuild: %q, want off", got)
		}
	})

	t.Run("API-12/rebuild", func(t *testing.T) {
		fake := platformtest.NewFake()
		env, _, _ := newFakeEnv(t, fake, nil)
		id := createdServer(t, env, "prot-rebuild", nil)
		rebuild := map[string]any{"image": "ubuntu-24.04"}

		changeProtection(t, env, id, map[string]any{"delete": true, "rebuild": true})
		path := fmt.Sprintf("/servers/%d/actions", id)
		actions0, disks0, status0 := len(listActions(t, env, path)), len(callsOf(fake, "CreateDisk")), getServer(t, env, id).Status

		wantError(t, postAction(env, id, "rebuild", rebuild), http.StatusLocked, "protected", "rebuild with rebuild protection")
		if n := len(listActions(t, env, path)); n != actions0 {
			t.Errorf("GET %s: %d actions after the refused rebuild, want %d", path, n, actions0)
		}
		if n := len(callsOf(fake, "CreateDisk")); n != disks0 {
			t.Errorf("CreateDisk calls after the refused rebuild: %d, want %d", n, disks0)
		}
		if got := getServer(t, env, id).Status; got != status0 {
			t.Errorf("status after the refused rebuild: %q, want %q", got, status0)
		}

		changeProtection(t, env, id, map[string]any{"delete": false, "rebuild": false})
		runAction(t, env, id, "rebuild", rebuild)
	})

	t.Run("SRV-09", func(t *testing.T) {
		fake := platformtest.NewFake()
		env, _, _ := newFakeEnv(t, fake, nil)
		id := createdServer(t, env, "resize", nil) // cx23: 2 cores, 4 GB, 40 GB
		memMB := vmSpec(t, lastCall(t, fake, "CreateVM")).MemoryMB
		diskBytes := sizeArg(t, lastCall(t, fake, "CreateDisk"))

		wantError(t, postAction(env, id, "change_type", map[string]any{"server_type": "cx33", "upgrade_disk": false}),
			http.StatusUnprocessableEntity, "server_not_stopped", "change_type on a running server")

		runAction(t, env, id, "poweroff", nil)
		for _, bad := range []struct{ typ, why string }{
			{"no-such-type", "unknown"},
			{"cax11", "arm"},
			{"cpx31", "deprecated in fsn1"},
			{"cpx02", "disk 20 GB below primary_disk_size 40"},
		} {
			wantError(t, postAction(env, id, "change_type", map[string]any{"server_type": bad.typ, "upgrade_disk": false}),
				http.StatusUnprocessableEntity, "invalid_server_type", "change_type to "+bad.typ+" ("+bad.why+")")
		}

		n := len(fake.Calls())
		runAction(t, env, id, "change_type", map[string]any{"server_type": "cx33", "upgrade_disk": false})
		calls := fake.Calls()[n:]
		checkSetVMThenStart(t, calls, 4, 2*memMB)
		if c := callsIn(calls, "ResizeDisk"); len(c) != 0 {
			t.Errorf("ResizeDisk calls with upgrade_disk false: %d, want none", len(c))
		}
		if s := getServer(t, env, id); s.ServerType.Name != "cx33" || s.PrimaryDiskSize != 40 || s.Status != "running" {
			t.Errorf("server after change_type cx33 without disk: type %q, primary_disk_size %d, status %q; want cx33, 40, running",
				s.ServerType.Name, s.PrimaryDiskSize, s.Status)
		}

		runAction(t, env, id, "poweroff", nil)
		runAction(t, env, id, "change_type", map[string]any{"server_type": "cx23", "upgrade_disk": false})
		if s := getServer(t, env, id); s.ServerType.Name != "cx23" || s.PrimaryDiskSize != 40 {
			t.Errorf("server after the downgrade to cx23: type %q, primary_disk_size %d; want cx23, 40", s.ServerType.Name, s.PrimaryDiskSize)
		}

		runAction(t, env, id, "poweroff", nil)
		n = len(fake.Calls())
		runAction(t, env, id, "change_type", map[string]any{"server_type": "cx33", "upgrade_disk": true})
		calls = fake.Calls()[n:]
		checkSetVMThenStart(t, calls, 4, 2*memMB)
		resize := callsIn(calls, "ResizeDisk")
		if len(resize) != 1 {
			t.Fatalf("ResizeDisk calls with upgrade_disk true: %d, want 1", len(resize))
		}
		if got := sizeArg(t, resize[0]); got != 2*diskBytes {
			t.Errorf("ResizeDisk size %d, want %d (80 GB)", got, 2*diskBytes)
		}
		if s := getServer(t, env, id); s.ServerType.Name != "cx33" || s.PrimaryDiskSize != 80 || s.Status != "running" {
			t.Errorf("server after change_type cx33 with disk: type %q, primary_disk_size %d, status %q; want cx33, 80, running",
				s.ServerType.Name, s.PrimaryDiskSize, s.Status)
		}
	})

	t.Run("SRV-20/change_type", func(t *testing.T) {
		fake := platformtest.NewFake()
		env, _, _ := newFakeEnv(t, fake, nil)
		id := createdServer(t, env, "migrate", nil)
		runAction(t, env, id, "poweroff", nil)

		release := block(t, fake, "SetVM")
		act, _ := startAction(t, env, id, "change_type", map[string]any{"server_type": "cx33", "upgrade_disk": false})
		waitStatus(t, env, id, "migrating")
		release()
		if a := env.WaitAction(act); a.Status != "success" {
			t.Fatalf("change_type action: %q %+v, want success", a.Status, a.Error)
		}
		if got := getServer(t, env, id).Status; got != "running" {
			t.Errorf("status after change_type: %q, want running", got)
		}
	})

	t.Run("SRV-18/actions", func(t *testing.T) {
		fake := platformtest.NewFake()
		env, _, _ := newFakeEnv(t, fake, nil)
		id := createdServer(t, env, "limits", map[string]any{"start_after_create": false})
		ramMB := vmSpec(t, lastCall(t, fake, "CreateVM")).MemoryMB

		r := roomy
		r.FreeMemoryMB = reserveRAMMB + ramMB - 1
		fake.SetResources(r)
		wantError(t, postAction(env, id, "poweron", nil), http.StatusPreconditionFailed, "resource_unavailable",
			"poweron with RAM above the limit")

		r = roomy
		r.LogicalCPUs = 2
		fake.SetResources(r)
		wantError(t, postAction(env, id, "change_type", map[string]any{"server_type": "cx33", "upgrade_disk": false}),
			http.StatusPreconditionFailed, "resource_unavailable", "change_type to 4 cores on a 2-core host")

		r = roomy
		r.FreeDiskBytes = 0
		fake.SetResources(r)
		wantError(t, postAction(env, id, "rebuild", map[string]any{"image": "ubuntu-24.04"}),
			http.StatusPreconditionFailed, "resource_unavailable", "rebuild with the disk growth above the limit")
	})

	t.Run("SRV-10", func(t *testing.T) {
		fake := platformtest.NewFake()
		env, rec := newAgentEnv(t, fake, 0)
		off := createdServer(t, env, "reset-off", map[string]any{"start_after_create": false})
		wantError(t, postAction(env, off, "reset_password", nil), http.StatusConflict, "conflict", "reset_password on an off server")

		id := createdServer(t, env, "reset", nil)
		act, raw := startAction(t, env, id, "reset_password", nil)
		var password string
		if err := json.Unmarshal(raw, &password); err != nil || password == "" {
			t.Fatalf("reset_password root_password = %s, want a non-empty string", raw)
		}
		reset := rec.waitPushedReset(t, id)
		if got, err := shacrypt.SHA512(password, reset.SHA512Crypt); err != nil || got != reset.SHA512Crypt {
			t.Errorf("pushed reset hash %q does not verify against the returned password (%q, %v)", reset.SHA512Crypt, got, err)
		}
		if doc := rec.pushedJSON(t); bytes.Contains(doc, []byte(password)) {
			t.Errorf("pushed agent document contains the clear-text password")
		}
		rec.confirm.Store(true)
		if a := env.WaitAction(act); a.Status != "success" {
			t.Errorf("reset_password action after confirmation: %q %+v, want success", a.Status, a.Error)
		}
		if r := rec.currentReset(t, id); r != nil {
			t.Errorf("pending reset after confirmation: %+v, want none", r)
		}

		tenv, trec := newAgentEnv(t, platformtest.NewFake(), 300*time.Millisecond)
		tid := createdServer(t, tenv, "reset-timeout", nil)
		act, _ = startAction(t, tenv, tid, "reset_password", nil)
		a := tenv.WaitAction(act)
		if a.Status != "error" || a.Error == nil || a.Error.Code != "server_error" {
			t.Errorf("reset_password action without confirmation: %q %+v, want error server_error", a.Status, a.Error)
		}
		if r := trec.currentReset(t, tid); r != nil {
			t.Errorf("pending reset after the timeout: %+v, want none", r)
		}
	})
}

// roomy are host resources far above what any test server needs.
var roomy = platform.Resources{MemoryMB: 64 << 10, FreeMemoryMB: 64 << 10, LogicalCPUs: 16, FreeDiskBytes: 1 << 40}

// Reserves the fake-platform environments set.
const (
	reserveRAMMB  = 1024
	reserveDiskGB = 10
)

// testPublicKey is a valid ed25519 public key.
const testPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA42AvjmOSeFhkGqi7LL8SMihzTLFoX9Kd9sDKhkOtDv servers-test"

// newFakeEnv mounts the servers module on the shipped catalog and platform
// p (the fake platform or a wrapper of it) through apitest.ServersDeps, with
// the reserves above and tune applied, plus the ssh_keys and actions
// modules.
func newFakeEnv(t *testing.T, p platform.Platform, tune func(*servers.Deps)) (*apitest.Env, *servers.Module, servers.Deps) {
	t.Helper()
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	d := apitest.ServersDeps(t, cat, p)
	d.Settings.ReserveRAMMB = reserveRAMMB
	d.Settings.ReserveDiskGB = reserveDiskGB
	if tune != nil {
		tune(&d)
	}
	mod := servers.New(d)
	return apitest.New(t, sshkeys.New(), mod, resactions.New()), mod, d
}

// shrinking is the fake platform whose host resources become after from
// the second HostResources call on.
type shrinking struct {
	*platformtest.Fake
	after platform.Resources
	calls atomic.Int32
}

func (s *shrinking) HostResources(ctx context.Context) (platform.Resources, error) {
	if s.calls.Add(1) > 1 {
		return s.after, nil
	}
	return s.Fake.HostResources(ctx)
}

// refSizes creates a cx23 server on a roomy fake platform and returns the
// RAM its VM gets and the nominal size of its disk, so limits follow the
// module's units.
func refSizes(t *testing.T) (ramMB, diskBytes int64) {
	t.Helper()
	fake := platformtest.NewFake()
	env, _, _ := newFakeEnv(t, fake, nil)
	createdServer(t, env, "reference", nil)
	return vmSpec(t, lastCall(t, fake, "CreateVM")).MemoryMB, sizeArg(t, lastCall(t, fake, "CreateDisk"))
}

// createdServer creates a cx23 server like createServer and waits for its
// create action to succeed.
func createdServer(t *testing.T, env *apitest.Env, name string, extra map[string]any) int64 {
	t.Helper()
	id, act := createServer(t, env, name, extra)
	if a := env.WaitAction(act); a.Status != "success" {
		t.Fatalf("create %s: action %q %+v, want success", name, a.Status, a.Error)
	}
	return id
}

// createWithPassword creates a cx23 server and returns its ID, its create
// action and the raw root_password of the response.
func createWithPassword(t *testing.T, env *apitest.Env, name string, extra map[string]any) (id, action int64, rootPassword json.RawMessage) {
	t.Helper()
	req := map[string]any{"name": name, "server_type": "cx23", "image": "ubuntu-24.04", "location": "fsn1"}
	for k, v := range extra {
		req[k] = v
	}
	resp := env.Do(http.MethodPost, "/servers", req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /servers %s: status %d, want 201", name, resp.StatusCode)
	}
	var body struct {
		Server struct {
			ID int64 `json:"id"`
		} `json:"server"`
		Action       apitest.Action  `json:"action"`
		RootPassword json.RawMessage `json:"root_password"`
	}
	apitest.Decode(t, resp, &body)
	return body.Server.ID, body.Action.ID, body.RootPassword
}

func countServers(t *testing.T, env *apitest.Env) int {
	t.Helper()
	var list struct {
		Servers []struct {
			ID int64 `json:"id"`
		} `json:"servers"`
	}
	apitest.Decode(t, env.Do(http.MethodGet, "/servers", nil), &list)
	return len(list.Servers)
}

type serverView struct {
	Status          string `json:"status"`
	PrimaryDiskSize int    `json:"primary_disk_size"`
	ServerType      struct {
		Name string `json:"name"`
	} `json:"server_type"`
}

func getServer(t *testing.T, env *apitest.Env, id int64) serverView {
	t.Helper()
	resp := env.Do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /servers/%d: status %d, want 200", id, resp.StatusCode)
	}
	var body struct {
		Server serverView `json:"server"`
	}
	apitest.Decode(t, resp, &body)
	return body.Server
}

// waitStatus polls the server until its status is want, failing the test
// after apitest.ActionTimeout.
func waitStatus(t *testing.T, env *apitest.Env, id int64, want string) {
	t.Helper()
	deadline := time.Now().Add(apitest.ActionTimeout)
	for {
		got := getServer(t, env, id).Status
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server %d status %q after %s, want %q", id, got, apitest.ActionTimeout, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// tickWatch runs the module's most frequent job once: one interval of the
// status watch.
func tickWatch(t *testing.T, mod *servers.Module) {
	t.Helper()
	jobs := mod.Jobs()
	if len(jobs) == 0 {
		t.Fatal("servers module has no jobs, want the status watch")
	}
	watch := jobs[0]
	for _, j := range jobs[1:] {
		if j.Every < watch.Every {
			watch = j
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), apitest.ActionTimeout)
	defer cancel()
	watch.Run(ctx)
}

// block makes the next call of op on fake wait; the returned release may be
// called more than once and runs at the end of the test at the latest.
func block(t *testing.T, fake *platformtest.Fake, op string) func() {
	t.Helper()
	release := fake.Block(op)
	var once sync.Once
	r := func() { once.Do(release) }
	t.Cleanup(r)
	return r
}

func postAction(env *apitest.Env, id int64, name string, body any) *http.Response {
	return env.Do(http.MethodPost, fmt.Sprintf("/servers/%d/actions/%s", id, name), body)
}

// startAction posts the action, expects 201 and returns the action ID and
// the raw root_password of the response (empty when absent).
func startAction(t *testing.T, env *apitest.Env, id int64, name string, body any) (int64, json.RawMessage) {
	t.Helper()
	resp := postAction(env, id, name, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /servers/%d/actions/%s: status %d (%s), want 201", id, name, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Action       apitest.Action  `json:"action"`
		RootPassword json.RawMessage `json:"root_password"`
	}
	apitest.Decode(t, resp, &b)
	return b.Action.ID, b.RootPassword
}

// runAction runs the action and waits for its success.
func runAction(t *testing.T, env *apitest.Env, id int64, name string, body any) {
	t.Helper()
	act, _ := startAction(t, env, id, name, body)
	if a := env.WaitAction(act); a.Status != "success" {
		t.Fatalf("%s %v: action %q %+v, want success", name, body, a.Status, a.Error)
	}
}

func wantError(t *testing.T, resp *http.Response, status int, code, what string) {
	t.Helper()
	if resp.StatusCode != status {
		t.Errorf("%s: status %d, want %d", what, resp.StatusCode, status)
	}
	if got := apitest.ErrorCode(t, resp); got != code {
		t.Errorf("%s: error.code %q, want %s", what, got, code)
	}
}

// expectLocked checks that another action on the server answers 423
// locked while the named action runs.
func expectLocked(t *testing.T, env *apitest.Env, id int64, during string) {
	t.Helper()
	wantError(t, postAction(env, id, "change_protection", map[string]any{"delete": false, "rebuild": false}),
		http.StatusLocked, "locked", "change_protection during "+during)
}

func callsIn(calls []platformtest.Call, op string) []platformtest.Call {
	var out []platformtest.Call
	for _, c := range calls {
		if c.Op == op {
			out = append(out, c)
		}
	}
	return out
}

func callsOf(fake *platformtest.Fake, op string) []platformtest.Call {
	return callsIn(fake.Calls(), op)
}

func lastCall(t *testing.T, fake *platformtest.Fake, op string) platformtest.Call {
	t.Helper()
	calls := callsOf(fake, op)
	if len(calls) == 0 {
		t.Fatalf("no %s call on the fake platform", op)
	}
	return calls[len(calls)-1]
}

func vmSpec(t *testing.T, c platformtest.Call) platform.VMSpec {
	t.Helper()
	s, ok := c.Arg.(platform.VMSpec)
	if !ok {
		t.Fatalf("%s call argument %T, want platform.VMSpec", c.Op, c.Arg)
	}
	return s
}

func sizeArg(t *testing.T, c platformtest.Call) int64 {
	t.Helper()
	switch v := c.Arg.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	}
	t.Fatalf("%s call argument %T, want the size in bytes", c.Op, c.Arg)
	return 0
}

// vmName is the VM of the last CreateVM call.
func vmName(t *testing.T, fake *platformtest.Fake) string {
	t.Helper()
	c := lastCall(t, fake, "CreateVM")
	if c.VM != "" {
		return c.VM
	}
	return vmSpec(t, c).Name
}

// checkSetVMThenStart checks that calls hold a SetVM with cores and ramMB
// followed by a power start.
func checkSetVMThenStart(t *testing.T, calls []platformtest.Call, cores int, ramMB int64) {
	t.Helper()
	setAt := -1
	for i, c := range calls {
		if c.Op == "SetVM" {
			setAt = i
			if s := vmSpec(t, c); s.CPUs != cores || s.MemoryMB != ramMB {
				t.Errorf("SetVM cores %d, RAM %d MB; want %d, %d", s.CPUs, s.MemoryMB, cores, ramMB)
			}
		}
	}
	if setAt < 0 {
		t.Fatalf("no SetVM call in %+v", calls)
	}
	for _, c := range calls[setAt+1:] {
		if op, ok := c.Arg.(platform.PowerOp); c.Op == "Power" && ok && op == platform.PowerStart {
			return
		}
	}
	t.Errorf("no power start after SetVM in %+v", calls)
}

var cryptPattern = regexp.MustCompile(`\$6\$[./0-9A-Za-z$=]+`)

// checkCrypt checks that data holds a SHA-512 crypt of password and not the
// clear text.
func checkCrypt(t *testing.T, what string, data []byte, password string) {
	t.Helper()
	if bytes.Contains(data, []byte(password)) {
		t.Errorf("%s contains the clear-text root password", what)
	}
	for _, h := range cryptPattern.FindAll(data, -1) {
		if got, err := shacrypt.SHA512(password, string(h)); err == nil && got == string(h) {
			return
		}
	}
	t.Errorf("%s holds no SHA-512 crypt of the root password:\n%s", what, data)
}

// seedFile returns the named file of the only seed ISO in the data folder.
func seedFile(t *testing.T, dataFolder, name string) []byte {
	t.Helper()
	var isos []string
	err := filepath.WalkDir(dataFolder, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".iso") {
			isos = append(isos, path)
		}
		return err
	})
	if err != nil || len(isos) != 1 {
		t.Fatalf("seed ISOs in %s: %v (%v), want one", dataFolder, isos, err)
	}
	d, err := diskfs.Open(isos[0], diskfs.WithOpenMode(diskfs.ReadOnly))
	if err != nil {
		t.Fatalf("open %s: %v", isos[0], err)
	}
	defer func() { _ = d.Close() }()
	fsys, err := d.GetFilesystem(0)
	if err != nil {
		t.Fatalf("read filesystem of %s: %v", isos[0], err)
	}
	f, err := fsys.OpenFile("/"+name, os.O_RDONLY)
	if err != nil {
		t.Fatalf("open %s in %s: %v", name, isos[0], err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read %s in %s: %v", name, isos[0], err)
	}
	return data
}

// agentRecorder keeps the last agent document the module pushed and
// confirms its password resets once confirm is set.
type agentRecorder struct {
	env     *apitest.Env
	mod     *servers.Module
	confirm atomic.Bool

	mu     sync.Mutex
	pushed *agentapi.Config
}

// newAgentEnv is newFakeEnv with the push and agent status of an
// agentRecorder and the given reset timeout (0: the default).
func newAgentEnv(t *testing.T, fake *platformtest.Fake, resetTimeout time.Duration) (*apitest.Env, *agentRecorder) {
	t.Helper()
	rec := &agentRecorder{}
	env, mod, _ := newFakeEnv(t, fake, func(d *servers.Deps) {
		d.ResetTimeout = resetTimeout
		d.PushAgent = func(context.Context) error {
			doc, err := rec.contribute()
			if err != nil {
				return err
			}
			rec.mu.Lock()
			rec.pushed = &doc
			rec.mu.Unlock()
			return nil
		}
		d.AgentStatus = func(context.Context) (agentapi.Status, error) {
			if !rec.confirm.Load() {
				return agentapi.Status{}, nil
			}
			rec.mu.Lock()
			defer rec.mu.Unlock()
			var done []int64
			if rec.pushed != nil {
				for _, m := range rec.pushed.Metadata {
					if m.PasswordReset != nil {
						done = append(done, m.PasswordReset.ID)
					}
				}
			}
			return agentapi.Status{PasswordResetsDone: done}, nil
		}
	})
	rec.env, rec.mod = env, mod
	return env, rec
}

func (r *agentRecorder) contribute() (agentapi.Config, error) {
	var doc agentapi.Config
	err := r.env.Store.Tx(func(tx store.Tx) error { return r.mod.Contribute(tx, &doc) })
	return doc, err
}

func resetOf(doc *agentapi.Config, id int64) *agentapi.PasswordReset {
	if doc == nil {
		return nil
	}
	for _, m := range doc.Metadata {
		if m.ServerID == id {
			return m.PasswordReset
		}
	}
	return nil
}

// waitPushedReset waits until a pushed document carries a password reset
// for server id and returns it.
func (r *agentRecorder) waitPushedReset(t *testing.T, id int64) agentapi.PasswordReset {
	t.Helper()
	deadline := time.Now().Add(apitest.ActionTimeout)
	for {
		r.mu.Lock()
		reset := resetOf(r.pushed, id)
		r.mu.Unlock()
		if reset != nil {
			return *reset
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pushed password reset for server %d after %s", id, apitest.ActionTimeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *agentRecorder) pushedJSON(t *testing.T) []byte {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := json.Marshal(r.pushed)
	if err != nil {
		t.Fatalf("encode pushed document: %v", err)
	}
	return b
}

// currentReset is the pending reset of server id in the document the
// module contributes now.
func (r *agentRecorder) currentReset(t *testing.T, id int64) *agentapi.PasswordReset {
	t.Helper()
	doc, err := r.contribute()
	if err != nil {
		t.Fatalf("contribute agent document: %v", err)
	}
	return resetOf(&doc, id)
}

// newEnv mounts the servers module on the shipped catalog with machines,
// plus the actions module for /actions/{id}.
func newEnv(t *testing.T, machines servers.Machines) *apitest.Env {
	t.Helper()
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	return apitest.New(t, servers.New(servers.Deps{
		Catalog:     cat,
		PublicRange: netip.MustParsePrefix("198.19.0.0/16"),
		Machines:    machines,
	}), resactions.New())
}

// createServer creates a cx23 server with the fields in extra added and
// returns its ID and the ID of its create action.
func createServer(t *testing.T, env *apitest.Env, name string, extra map[string]any) (id, action int64) {
	t.Helper()
	req := map[string]any{
		"name":        name,
		"server_type": "cx23",
		"image":       "ubuntu-24.04",
		"location":    "fsn1",
	}
	for k, v := range extra {
		req[k] = v
	}
	resp := env.Do(http.MethodPost, "/servers", req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /servers %s: status %d, want 201", name, resp.StatusCode)
	}
	var body struct {
		Server struct {
			ID int64 `json:"id"`
		} `json:"server"`
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	apitest.Decode(t, resp, &body)
	return body.Server.ID, body.Action.ID
}

func rawAction(t *testing.T, env *apitest.Env, path string) map[string]any {
	t.Helper()
	resp := env.Do(http.MethodGet, path, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", path, resp.StatusCode)
	}
	var body struct {
		Action map[string]any `json:"action"`
	}
	apitest.Decode(t, resp, &body)
	return body.Action
}

func listActions(t *testing.T, env *apitest.Env, path string) []map[string]any {
	t.Helper()
	resp := env.Do(http.MethodGet, path, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", path, resp.StatusCode)
	}
	var body struct {
		Actions []map[string]any `json:"actions"`
	}
	apitest.Decode(t, resp, &body)
	return body.Actions
}

// changeProtection runs change_protection and waits for its success.
func changeProtection(t *testing.T, env *apitest.Env, id int64, body map[string]any) {
	t.Helper()
	resp := env.Do(http.MethodPost, fmt.Sprintf("/servers/%d/actions/change_protection", id), body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("change_protection %v: status %d, want 201", body, resp.StatusCode)
	}
	var b struct {
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	if a := env.WaitAction(b.Action.ID); a.Status != "success" {
		t.Fatalf("change_protection %v: action %q, want success", body, a.Status)
	}
}

type protection struct {
	Delete  bool `json:"delete"`
	Rebuild bool `json:"rebuild"`
}

func serverProtection(t *testing.T, env *apitest.Env, id int64) protection {
	t.Helper()
	resp := env.Do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /servers/%d: status %d, want 200", id, resp.StatusCode)
	}
	var body struct {
		Server struct {
			Protection protection `json:"protection"`
		} `json:"server"`
	}
	apitest.Decode(t, resp, &body)
	return body.Server.Protection
}

// deleteServer deletes the server, waits for the delete action and checks
// the server is gone.
func deleteServer(t *testing.T, env *apitest.Env, id int64) {
	t.Helper()
	resp := env.Do(http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /servers/%d: status %d, want 200", id, resp.StatusCode)
	}
	var b struct {
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	if a := env.WaitAction(b.Action.ID); a.Status != "success" {
		t.Fatalf("DELETE /servers/%d: action %q, want success", id, a.Status)
	}
	if resp := env.Do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /servers/%d after delete: status %d, want 404", id, resp.StatusCode)
	}
}
