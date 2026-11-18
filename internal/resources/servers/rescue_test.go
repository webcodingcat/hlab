package servers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	"github.com/webcodingcat/hlab/internal/resources/servers"
)

const rescueUserData = "#cloud-config\nruncmd:\n  - [sh, -c, \"echo hlab-rescue-user-data > /root/marker\"]\n"

// testRescue holds the rows of rescue_test.go; TestServers runs them.
func testRescue(t *testing.T) {
	t.Run("SRV-11", func(t *testing.T) {
		p := newRecPlatform()
		imgs := newTestImages(t)
		clk := newTestClock(time.Date(2026, 11, 18, 3, 0, 0, 0, time.UTC))
		env, mod, _ := newImageEnv(t, p, func(d *servers.Deps) {
			d.Images = imgs
			d.Clock = clk
		})
		keyID := createSSHKey(t, env, "rescue", testPublicKey)
		id := createdServer(t, env, "rescue", map[string]any{"user_data": rescueUserData})
		vm := vmName(t, p.Fake)
		serverSeed := p.lastDVD(t, 0).iso
		serverMeta := isoFiles(t, serverSeed)["meta-data"]
		serverInstance := instanceID(t, serverMeta)

		// enable checks the rescue configuration and returns the password.
		enable := func(t *testing.T) (password string, child string) {
			t.Helper()
			n := len(p.Fake.Calls())
			act, raw := startAction(t, env, id, "enable_rescue", map[string]any{"type": "linux64", "ssh_keys": []int64{keyID}})
			if err := json.Unmarshal(raw, &password); err != nil || password == "" {
				t.Fatalf("enable_rescue root_password = %s, want a non-empty string", raw)
			}
			if a := env.WaitAction(act); a.Status != "success" {
				t.Fatalf("enable_rescue action: %q %+v, want success", a.Status, a.Error)
			}
			if c := callsIn(p.Fake.Calls()[n:], "Power"); len(c) != 0 {
				t.Errorf("enable_rescue changed the power state: %+v", c)
			}
			if s := rescueView(t, env, id); !s.RescueEnabled {
				t.Error("rescue_enabled false after enable_rescue, want true")
			}
			children := p.childDisks()
			if len(children) == 0 {
				t.Fatal("no CreateChildDisk call after enable_rescue")
			}
			c := children[len(children)-1]
			if !sameFile(c.parent, imgs.rescue) {
				t.Errorf("rescue disk is a child of %q, want the rescue base %q", c.parent, imgs.rescue)
			}
			at := p.attachesAt(63)
			if len(at) == 0 || !sameFile(at[len(at)-1].path, c.path) || at[len(at)-1].vm != vm {
				t.Errorf("attaches at LUN 63 %+v, want the rescue disk %s on %s", at, c.path, vm)
			}
			if first := firstBoot(t, p.Fake); first != (platform.BootDevice{Kind: "disk", Slot: 63}) {
				t.Errorf("first boot device after enable_rescue %+v, want disk LUN 63", first)
			}
			seed := p.lastDVD(t, 0).iso
			if sameFile(seed, serverSeed) {
				t.Fatalf("DVD slot 0 still holds the server's seed after enable_rescue")
			}
			files := isoFiles(t, seed)
			if got := instanceID(t, files["meta-data"]); got != serverInstance {
				t.Errorf("rescue seed instance-id %q, want the server's %q", got, serverInstance)
			}
			all := bytes.Join([][]byte{files["meta-data"], files["user-data"], files["vendor-data"]}, []byte("\n"))
			if !bytes.Contains(all, []byte(testPublicKey[:60])) {
				t.Errorf("rescue seed lacks the given SSH key")
			}
			checkCrypt(t, "rescue seed", all, password)
			if bytes.Contains(all, []byte("hlab-rescue-user-data")) {
				t.Errorf("rescue seed holds the server's user_data")
			}
			return password, c.path
		}

		// checkRestored checks rescue_enabled false, LUN 0 first and the
		// server's seed in slot 0, set after call n.
		checkRestored := func(t *testing.T, what string, n int) {
			t.Helper()
			if s := rescueView(t, env, id); s.RescueEnabled {
				t.Errorf("rescue_enabled true after %s, want false", what)
			}
			if first := firstBoot(t, p.Fake); first != (platform.BootDevice{Kind: "disk", Slot: 0}) {
				t.Errorf("first boot device after %s %+v, want disk LUN 0", what, first)
			}
			d := p.lastDVD(t, 0)
			if d.seq < n {
				t.Errorf("DVD slot 0 not set again after %s", what)
				return
			}
			files := isoFiles(t, d.iso)
			if instanceID(t, files["meta-data"]) != serverInstance || !bytes.Contains(files["user-data"], []byte("hlab-rescue-user-data")) {
				t.Errorf("DVD slot 0 after %s holds no seed of the server with its user_data", what)
			}
		}

		// bootsRescue runs op and checks it started the VM into rescue and
		// restored the server's configuration after the start.
		bootsRescue := func(t *testing.T, op string) {
			t.Helper()
			n := len(p.Fake.Calls())
			runAction(t, env, id, op, nil)
			calls := p.Fake.Calls()[n:]
			power := -1
			for i, c := range calls {
				if c.Op == "Power" {
					power = i
				}
			}
			if power < 0 {
				t.Fatalf("%s with rescue enabled made no power call", op)
			}
			for _, c := range calls[:power] {
				if order, ok := c.Arg.([]platform.BootDevice); c.Op == "SetBootOrder" && ok && len(order) > 0 && order[0].Slot != 63 {
					t.Errorf("%s set boot order %+v before booting into rescue", op, order)
				}
			}
			checkRestored(t, op+" into rescue", n+power+1)
		}

		_, child1 := enable(t)
		bootsRescue(t, "reset")
		n := len(p.Fake.Calls())
		runAction(t, env, id, "reboot", nil)
		if !detached(p.Fake.Calls()[n:], child1) || fileExists(child1) {
			t.Errorf("rescue disk %s not detached and deleted at the next boot without rescue", child1)
		}

		_, child2 := enable(t)
		clk.Add(59 * time.Minute)
		tickWatch(t, mod)
		if !rescueView(t, env, id).RescueEnabled {
			t.Fatal("rescue disabled 59 minutes after enable_rescue, want enabled until 60")
		}
		n = len(p.Fake.Calls())
		clk.Add(2 * time.Minute)
		tickWatch(t, mod)
		checkRestored(t, "60 minutes without a boot", n)

		_, child3 := enable(t)
		n = len(p.Fake.Calls())
		runAction(t, env, id, "disable_rescue", nil)
		checkRestored(t, "disable_rescue", n)

		n = len(p.Fake.Calls())
		p.SetState(vm, platform.StateOff)
		tickWatch(t, mod)
		if !detached(p.Fake.Calls()[n:], child3) || fileExists(child3) {
			t.Errorf("rescue disk %s not detached and deleted when the VM was seen off", child3)
		}
		// The rescue disk of the timed-out rescue is gone by now as well,
		// whether enable_rescue replaced it or the VM was seen off.
		if child2 != child3 && (!detached(p.Fake.Calls(), child2) || fileExists(child2)) {
			t.Errorf("rescue disk %s of the timed-out rescue not detached and deleted", child2)
		}

		enable(t)
		bootsRescue(t, "poweron")
	})
}

// testImages names base disks and ISOs in a temporary folder, records the
// ISO requests and fails those of failISO.
type testImages struct {
	dir    string
	rescue string

	mu      sync.Mutex
	isos    []string
	failISO string
}

var _ servers.Images = (*testImages)(nil)

func newTestImages(t *testing.T) *testImages {
	dir := t.TempDir()
	return &testImages{dir: dir, rescue: filepath.Join(dir, "rescue.vhdx")}
}

func (m *testImages) Ensure(_ context.Context, name string) (string, error) {
	return filepath.Join(m.dir, name+".vhdx"), nil
}

func (m *testImages) EnsureISO(_ context.Context, name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.isos = append(m.isos, name)
	if name == m.failISO {
		return "", errors.New("injected download failure")
	}
	return m.isoPath(name), nil
}

func (m *testImages) EnsureRescue(context.Context) (string, error) { return m.rescue, nil }

func (m *testImages) isoPath(name string) string { return filepath.Join(m.dir, "isos", name) }

func (m *testImages) isoRequests() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.isos...)
}

func (m *testImages) setFailISO(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failISO = name
}

// firstBoot is the first device of the last boot order set.
func firstBoot(t *testing.T, fake *platformtest.Fake) platform.BootDevice {
	t.Helper()
	order := lastBootOrder(t, fake)
	if len(order) == 0 {
		t.Fatal("empty boot order")
	}
	return order[0]
}

func lastBootOrder(t *testing.T, fake *platformtest.Fake) []platform.BootDevice {
	t.Helper()
	c := lastCall(t, fake, "SetBootOrder")
	order, ok := c.Arg.([]platform.BootDevice)
	if !ok {
		t.Fatalf("SetBootOrder argument %T, want []platform.BootDevice", c.Arg)
	}
	return order
}

// detached reports whether calls detach the disk at path.
func detached(calls []platformtest.Call, path string) bool {
	for _, c := range callsIn(calls, "DetachDisk") {
		if p, ok := c.Arg.(string); ok && sameFile(p, path) {
			return true
		}
	}
	return false
}

type rescueState struct {
	RescueEnabled bool `json:"rescue_enabled"`
}

func rescueView(t *testing.T, env *apitest.Env, id int64) rescueState {
	t.Helper()
	resp := env.Do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /servers/%d: status %d, want 200", id, resp.StatusCode)
	}
	var b struct {
		Server rescueState `json:"server"`
	}
	apitest.Decode(t, resp, &b)
	return b.Server
}

// createSSHKey creates an SSH key and returns its ID.
func createSSHKey(t *testing.T, env *apitest.Env, name, publicKey string) int64 {
	t.Helper()
	resp := env.Do(http.MethodPost, "/ssh_keys", map[string]any{"name": name, "public_key": publicKey})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /ssh_keys: status %d, want 201", resp.StatusCode)
	}
	var key struct {
		SSHKey struct {
			ID int64 `json:"id"`
		} `json:"ssh_key"`
	}
	apitest.Decode(t, resp, &key)
	return key.SSHKey.ID
}
