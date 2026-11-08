//go:build live && windows

package itest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/api/cloud"
	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/system"
	"github.com/webcodingcat/hlab/internal/web"
)

// checkUserData makes the guest serve what its metadata service told it, on
// port 8000, so that the test can read it from the host without logging in.
// Debian's cloud image has python3 for cloud-init. It syncs first: the reset
// that follows would otherwise lose what the first boot wrote in the last
// 30 s (ext4 writes data back after 30 s), cloud-init's netplan file among
// it, and the guest would come back without a network.
const checkUserData = `#cloud-config
runcmd:
  - - sh
    - -c
    - >-
      mkdir -p /srv/check && cd /srv/check &&
      for key in instance-id hostname public-ipv4 region; do
      python3 -c "import sys, urllib.request as u; sys.stdout.buffer.write(u.urlopen('http://169.254.169.254/hetzner/v1/metadata/' + sys.argv[1]).read())" $key > $key;
      done && sync &&
      (nohup python3 -m http.server 8000 --bind :: > /dev/null 2>&1 &)
`

// TestServersBootAnswerAndOutliveTheLab runs the whole lab as hlabd does,
// with the Cloud API in the test's process, and creates a Debian and an
// Ubuntu server through it. They must answer SSH and serve what their
// metadata service told them (back on port 8000) over IPv4 and IPv6, take
// the lab's key, see the host as 198.18.0.1 (also from WSL2), reach the
// Internet but neither the lab control nor private ranges through the
// uplink, reach each other only through the appliance, and take a
// connection over IPv6 at once after 45 s without IPv6. Then the lab
// stops and starts again: the same servers with the same addresses and
// disks, also a server whose VM was removed; HLab crashes and the next
// start takes everything over; the appliance is turned off hard and comes
// back. HLab crashes again and comes back with a new release of the
// appliance: the new appliance replaces the old one while a server runs,
// with an outage under 5 s and two SSH sessions that live on: one prints
// all the time, the other is quiet until after the update and then sends
// first. Last come a reset, the guest's shutdown and the deletes, and the
// host must be as before. It needs administrator rights, ssh.exe and the
// appliance's disk next to the test (bin/dev itest copies it); HLab must
// not run. The system images stay in image-cache next to the test, so that
// later runs need not load them.
func TestServersBootAnswerAndOutliveTheLab(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	here := filepath.Dir(executable)
	release := filepath.Join(here, "hlab-appliance.vhdx")
	if _, err := os.Stat(release); err != nil {
		t.Skipf("no appliance to boot: %v (bin/dev appliance)", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
	defer cancel()
	parts := newParts(t.TempDir())
	t.Cleanup(func() { _ = parts.Close() })
	keys := newLabKey(t)
	run := labRun{
		dataDir: t.TempDir(),
		release: release,
		labKey:  keys.public,
		parts:   parts,
		caches: []*imageCache{
			{dir: filepath.Join(here, "image-cache"), name: "debian-13"},
			{dir: filepath.Join(here, "image-cache"), name: "ubuntu-26.04"},
		},
	}
	l := run.open(ctx, t)
	t.Cleanup(func() {
		run.close(t, l)
		run.removeVMs(t)
	})
	api := newCloudAPI(t, l)

	debian := createServer(ctx, t, api, "itest-debian", "debian-13")
	ubuntu := createServer(ctx, t, api, "itest-ubuntu", "ubuntu-26.04")
	for _, cache := range run.caches {
		cache.keep(t, l)
	}
	for _, server := range []guest{debian, ubuntu} {
		for _, address := range []string{server.ipv4, server.ipv6} {
			checkMetadata(ctx, t, address, map[string]string{
				"instance-id": fmt.Sprint(server.id), "hostname": server.name, "public-ipv4": server.ipv4, "region": "eu-central",
			})
		}
	}

	// the lab's key logs in, and the guest sees the host's address
	if got := keys.run(t, debian.ipv4, "", "echo $SSH_CLIENT"); !strings.HasPrefix(got, "198.18.0.1 ") {
		t.Errorf("the guest sees an SSH connection over IPv4 from %q, want 198.18.0.1", got)
	}
	if got := keys.run(t, debian.ipv6, "", "echo $SSH_CLIENT"); !strings.HasPrefix(got, "2001:db8:0:ffff::1 ") {
		t.Errorf("the guest sees an SSH connection over IPv6 from %q, want 2001:db8:0:ffff::1", got)
	}
	idleSince := time.Now() // the next checks use IPv4 only
	keys.fromWSL(t, debian.ipv4)
	checkUplink(t, keys, debian.ipv4)
	// isolated ports: the servers reach each other only through the
	// appliance, which takes one off the TTL
	if got := keys.run(t, ubuntu.ipv4, "", "ping -c 1 -W 3 "+debian.ipv4); !strings.Contains(got, "ttl=63") {
		t.Errorf("ping from %s to %s: %q, want ttl=63 (one router between them)", ubuntu.name, debian.name, got)
	}
	checkConnectAfterIdle(ctx, t, debian.ipv6, idleSince)

	// a stop and a start: the same servers, addresses and disks; the
	// Ubuntu server loses its VM meanwhile and gets a new one with its disk
	for _, server := range []guest{debian, ubuntu} {
		keys.run(t, server.ipv4, "", "echo "+server.name+" > /root/itest-mark && sync")
	}
	booted := map[string]string{debian.name: keys.bootID(t, debian), ubuntu.name: keys.bootID(t, ubuntu)}
	run.close(t, l)
	checkHostIsClean(t)
	if err := parts.Hypervisor.DeleteVM(ctx, lab.ServerVM(ubuntu.id)); err != nil {
		t.Fatal(err)
	}
	restarted := time.Now()
	l = run.open(ctx, t)
	for _, server := range []guest{debian, ubuntu} {
		waitForSSH(ctx, t, server.ipv4)
		if got := keys.run(t, server.ipv4, "", "cat /root/itest-mark"); got != server.name {
			t.Errorf("%s after the restart: the mark on its disk is %q, want %q", server.name, got, server.name)
		}
		if id := keys.bootID(t, server); id == booted[server.name] {
			t.Errorf("%s did not boot again with the lab", server.name)
		}
	}
	t.Logf("both servers answered %s after the lab's start", time.Since(restarted).Round(100*time.Millisecond))

	// a crash: another process runs the lab and is killed; the servers keep
	// running, and the next start takes them over without a restart
	run.close(t, l)
	run.crash(ctx, t)
	for _, server := range []guest{debian, ubuntu} {
		waitForSSH(ctx, t, server.ipv4)
		booted[server.name] = keys.bootID(t, server)
	}
	l = run.open(ctx, t)
	api = newCloudAPI(t, l) // the project and its token came through the restarts
	for _, server := range []guest{debian, ubuntu} {
		if id := keys.bootID(t, server); id != booted[server.name] {
			t.Errorf("%s booted again after the crash; the start should have taken it over", server.name)
		}
		waitForStatus(ctx, t, api, server.id, "running")
	}

	// the appliance turned off hard: the watchdog brings it back
	turnedOff := time.Now()
	if err := parts.Hypervisor.StopVM(ctx, system.ApplianceVM("A")); err != nil {
		t.Fatal(err)
	}
	t.Logf("the appliance was off %s after the call", time.Since(turnedOff).Round(10*time.Millisecond))
	waitForPort(ctx, t, debian.ipv4, 22)
	restored := time.Since(turnedOff)
	t.Logf("the server answered again %s after the appliance was turned off", restored.Round(10*time.Millisecond))
	if restored > 10*time.Second {
		t.Errorf("the outage took %s, want less than 10 s", restored.Round(100*time.Millisecond))
	}

	// the appliance's update: HLab crashes and comes back with a new
	// release, which replaces the running appliance (MAC set A) by one with
	// the set B while the servers run on
	run.close(t, l)
	run.crash(ctx, t)
	waitForSSH(ctx, t, debian.ipv4)
	booted[debian.name] = keys.bootID(t, debian)
	run.release = newRelease(t, release)
	outages := watchOutage(debian.ipv4, 22)
	session := keys.watchSession(t, debian.ipv4, tickingSession)
	quiet := keys.watchSession(t, debian.ipv4, quietSession)
	updated := time.Now()
	l = run.open(ctx, t)
	t.Logf("the lab started with the appliance updated in %s", time.Since(updated).Round(100*time.Millisecond))
	time.Sleep(2 * time.Second) // an outage right after the switch would show now
	outage := outages.end()
	keys.run(t, debian.ipv4, "", "touch "+quietMark)
	stalled, lived := session.end()
	_, quietLived := quiet.end()
	t.Logf("during the update the server took no new connection for %s at most, and the SSH session stalled for %s at most",
		outage.Round(10*time.Millisecond), stalled.Round(10*time.Millisecond))
	if outage > 5*time.Second {
		t.Errorf("the update's outage took %s, want less than 5 s", outage.Round(100*time.Millisecond))
	}
	if !lived {
		t.Error("the SSH session through the appliance did not live through the update")
	}
	if !quietLived {
		t.Error("the SSH session that was quiet during the update hung when the guest spoke first")
	}
	api = newCloudAPI(t, l)
	if id := keys.bootID(t, debian); id != booted[debian.name] {
		t.Errorf("%s booted again during the appliance's update", debian.name)
	}
	waitForStatus(ctx, t, api, debian.id, "running")
	if state, err := parts.Hypervisor.VMState(ctx, system.ApplianceVM("A")); !errors.Is(err, platform.ErrNotFound) {
		t.Errorf("the old appliance: %+v, %v; want it gone", state, err)
	}
	if state, err := parts.Hypervisor.VMState(ctx, system.ApplianceVM("B")); err != nil || state.Power != platform.PowerRunning {
		t.Errorf("the new appliance: %+v, %v; want it running", state, err)
	}
	checkApplianceFolder(t, run.dataDir, run.release, "B")

	reset := time.Now()
	api.action(t, debian.id, "reset")
	time.Sleep(2 * time.Second) // the reset takes the guest down at once; SSH may answer from before
	waitForSSH(ctx, t, debian.ipv4)
	t.Logf("SSH answered again %s after the reset", time.Since(reset).Round(100*time.Millisecond))

	shutdown := time.Now()
	api.action(t, debian.id, "shutdown")
	waitForStatus(ctx, t, api, debian.id, "off")
	t.Logf("the guest shut down and the server was off %s after the shutdown", time.Since(shutdown).Round(100*time.Millisecond))

	for _, server := range []guest{debian, ubuntu} {
		var deleted struct {
			Action struct{ ID int64 } `json:"action"`
		}
		api.do(t, http.MethodDelete, fmt.Sprintf("/servers/%d", server.id), nil, http.StatusOK, &deleted)
		waitForAction(ctx, t, api, deleted.Action.ID)
		waitForVMGone(ctx, t, parts.Hypervisor, lab.ServerVM(server.id))
	}
	run.close(t, l)
	for _, cache := range run.caches {
		cache.store(t)
	}
	// the appliance's VM stays off for the next start, as after every stop
	found, err := parts.Hypervisor.FindVMs(ctx, system.VMTag)
	if err != nil || len(found) != 1 || found[0].Name != system.ApplianceVM("B") || found[0].Power != platform.PowerOff {
		t.Errorf("the lab's VMs left: %v, %v; want only the appliance's, off", found, err)
	}
	checkHostIsClean(t)
}

// waitForPort waits until a TCP port of the address takes connections; it
// tries every 100 ms, so that it measures an outage to a tenth of a second.
func waitForPort(ctx context.Context, t *testing.T, address string, port int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		conn, err := (&net.Dialer{Timeout: 300 * time.Millisecond}).DialContext(ctx, "tcp", net.JoinHostPort(address, fmt.Sprint(port)))
		if err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s:%d took no connection within 2 minutes: %v", address, port, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// checkConnectAfterIdle waits until the host has not used IPv6 with the
// lab for 45 s, so that the neighbor entries of the host and the appliance
// went stale, and then connects to the guest's SSH port over IPv6. The
// connection must come as quickly as one over IPv4, not after the 5 s the
// host waits before it asks a stale neighbor again.
func checkConnectAfterIdle(ctx context.Context, t *testing.T, address string, idleSince time.Time) {
	t.Helper()
	time.Sleep(time.Until(idleSince.Add(45 * time.Second)))
	started := time.Now()
	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(address, "22"))
	took := time.Since(started)
	if err != nil {
		t.Errorf("after 45 s without IPv6, a connection to %s failed after %s: %v", address, took.Round(time.Millisecond), err)
		return
	}
	_ = conn.Close()
	t.Logf("after 45 s without IPv6, a connection to %s took %s", address, took.Round(time.Millisecond))
	if took > time.Second {
		t.Errorf("after 45 s without IPv6, a connection to %s took %s, want less than 1 s", address, took.Round(time.Millisecond))
	}
}

// guest is a server the test created.
type guest struct {
	name       string
	id         int64
	ipv4, ipv6 string
}

// createServer creates a server of the image with the user data that serves
// its metadata on port 8000, and waits until it answers SSH.
func createServer(ctx context.Context, t *testing.T, api cloudAPI, name, image string) guest {
	t.Helper()
	started := time.Now()
	var created struct {
		Server struct {
			ID        int64 `json:"id"`
			PublicNet struct {
				IPv4 struct{ IP string } `json:"ipv4"`
				IPv6 struct{ IP string } `json:"ipv6"`
			} `json:"public_net"`
		} `json:"server"`
	}
	api.do(t, http.MethodPost, "/servers", map[string]any{
		"name": name, "server_type": "cpx22", "image": image, "location": "fsn1", "user_data": checkUserData,
	}, http.StatusCreated, &created)
	server := guest{
		name: name,
		id:   created.Server.ID,
		ipv4: created.Server.PublicNet.IPv4.IP,
		ipv6: strings.TrimSuffix(created.Server.PublicNet.IPv6.IP, "/64") + "1",
	}
	waitForSSH(ctx, t, server.ipv4)
	t.Logf("%s (%d, %s, %s): SSH answered %s after the create", name, server.id, server.ipv4, server.ipv6,
		time.Since(started).Round(100*time.Millisecond))
	return server
}

// waitForAction waits until an action has ended and fails the test unless
// it succeeded.
func waitForAction(ctx context.Context, t *testing.T, api cloudAPI, id int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		var answer struct {
			Action struct {
				Status string
				Error  *struct{ Message string }
			} `json:"action"`
		}
		api.do(t, http.MethodGet, fmt.Sprintf("/actions/%d", id), nil, http.StatusOK, &answer)
		switch answer.Action.Status {
		case "success":
			return
		case "error":
			t.Fatalf("action %d failed: %+v", id, answer.Action.Error)
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			t.Fatalf("action %d is still %s after 2 minutes", id, answer.Action.Status)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// waitForVMGone waits until a deleted server's VM is gone.
func waitForVMGone(ctx context.Context, t *testing.T, hypervisor platform.Hypervisor, vm string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		if _, err := hypervisor.VMState(ctx, vm); errors.Is(err, platform.ErrNotFound) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s is still there a minute after the delete", vm)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// cloudAPI calls the lab's Cloud API as a project's tool would.
type cloudAPI struct {
	url   string
	token string
}

// newCloudAPI serves the lab's Cloud API on a local port, with a token of the
// project "itest", which it creates if the lab has none.
func newCloudAPI(t *testing.T, l *lab.Lab) cloudAPI {
	t.Helper()
	project, err := l.ProjectByName("itest")
	if errors.Is(err, state.ErrNotFound) {
		project, err = l.CreateProject("itest", state.Limits{})
	}
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := l.CreateToken(project.ID, fmt.Sprint("itest-", time.Now().UnixNano()), state.PermissionReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	api := web.NewAPI(web.Options{Backend: l, RateLimit: web.HetznerRateLimit, Logger: quietLogger()})
	cloud.Register(api, l)
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	return cloudAPI{url: server.URL + web.Prefix, token: token}
}

// do sends a request and decodes the answer into out, if given; another
// status than want fails the test.
func (c cloudAPI) do(t *testing.T, method, path string, body any, want int, out any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	request, _ := http.NewRequestWithContext(t.Context(), method, c.url+path, reader)
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != want {
		t.Fatalf("%s %s answered %d %s", method, path, response.StatusCode, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatal(err)
		}
	}
}

// action runs a server action.
func (c cloudAPI) action(t *testing.T, id int64, command string) {
	t.Helper()
	c.do(t, http.MethodPost, fmt.Sprintf("/servers/%d/actions/%s", id, command), map[string]any{}, http.StatusCreated, nil)
}

// waitForSSH waits until the address answers with an SSH banner.
func waitForSSH(ctx context.Context, t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for {
		conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(address, "22"))
		if err == nil {
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			banner, readErr := bufio.NewReader(conn).ReadString('\n')
			_ = conn.Close()
			if readErr == nil && strings.HasPrefix(banner, "SSH-2.0-") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not answer SSH within 5 minutes: %v", address, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// checkMetadata reads what the guest's metadata service told it, from the
// guest's port 8000, and compares it with want.
func checkMetadata(ctx context.Context, t *testing.T, address string, want map[string]string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(2 * time.Minute)
	for key, value := range want {
		url := "http://" + net.JoinHostPort(address, "8000") + "/" + key
		for {
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			response, err := client.Do(request)
			if err == nil {
				data, _ := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					if got := strings.TrimSpace(string(data)); got != value {
						t.Errorf("%s: %q, want %q", url, got, value)
					}
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not answer within 2 minutes: %v", url, err)
			}
			time.Sleep(time.Second)
		}
	}
}

// waitForStatus waits until the server has the status.
func waitForStatus(ctx context.Context, t *testing.T, api cloudAPI, id int64, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		var answer struct {
			Server struct{ Status string } `json:"server"`
		}
		api.do(t, http.MethodGet, fmt.Sprintf("/servers/%d", id), nil, http.StatusOK, &answer)
		if answer.Server.Status == want {
			return
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			t.Fatalf("the server is %s, not %s, after 2 minutes", answer.Server.Status, want)
		}
		time.Sleep(time.Second)
	}
}

// imageCache keeps a system image next to the test between runs: the lab
// would load it anew into its temporary data folder every time.
type imageCache struct {
	dir    string
	name   string
	loaded string // the file the lab loaded in this run, to keep
}

// lend gives the lab the kept image, if there is one, as its cache entry.
func (c *imageCache) lend(t *testing.T, l *lab.Lab) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(c.dir, c.name+".json"))
	if err != nil {
		return
	}
	var kept state.CachedImage
	if err := json.Unmarshal(data, &kept); err != nil {
		t.Fatal(err)
	}
	if err := l.Change(func(tx *state.Tx) error {
		if tx.Lab().Images == nil {
			tx.Lab().Images = map[string]state.CachedImage{}
		}
		tx.Lab().Images[c.name] = kept
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("the image %s comes from %s", c.name, kept.File)
}

// keep notes the image the lab loaded itself, to store it after the run.
func (c *imageCache) keep(t *testing.T, l *lab.Lab) {
	t.Helper()
	_ = l.View(func(tx *state.Tx) error {
		if cached := tx.Lab().Images[c.name]; !strings.HasPrefix(cached.File, c.dir) {
			c.loaded = cached.File
			data, _ := json.Marshal(cached)
			_ = os.MkdirAll(c.dir, 0o755)
			_ = os.WriteFile(filepath.Join(c.dir, c.name+".json.new"), data, 0o644)
		}
		return nil
	})
}

// store moves the image the lab loaded next to the test, once no disk uses
// it any more.
func (c *imageCache) store(t *testing.T) {
	t.Helper()
	if c.loaded == "" {
		return
	}
	file := filepath.Join(c.dir, c.name+".vhdx")
	if err := os.Rename(c.loaded, file); err != nil {
		t.Logf("keeping the image: %v", err)
		return
	}
	data, err := os.ReadFile(filepath.Join(c.dir, c.name+".json.new"))
	if err != nil {
		return
	}
	var cached state.CachedImage
	_ = json.Unmarshal(data, &cached)
	cached.File = file
	data, _ = json.Marshal(cached)
	_ = os.WriteFile(filepath.Join(c.dir, c.name+".json"), data, 0o644)
	_ = os.Remove(filepath.Join(c.dir, c.name+".json.new"))
}
