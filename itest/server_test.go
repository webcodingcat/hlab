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
	"log/slog"
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
	"github.com/webcodingcat/hlab/internal/platform/windows"
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

// TestServerBootsAndAnswersOnItsAddresses runs the whole lab as hlabd does,
// with the Cloud API in the test's process, and creates a Debian server
// through it. The server must answer SSH on its Primary IPv4, see its
// metadata (served back on port 8000, over IPv4 and IPv6), come back after
// a reset, shut down through its guest and go with its delete. It needs
// administrator rights and the appliance's disk next to the test (bin/dev
// itest copies it); HLab must not run. The system image stays in
// image-cache next to the test, so that later runs need not load it.
func TestServerBootsAndAnswersOnItsAddresses(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	here := filepath.Dir(executable)
	release := filepath.Join(here, "hlab-appliance.vhdx")
	if _, err := os.Stat(release); err != nil {
		t.Skipf("no appliance to boot: %v (bin/dev appliance)", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
	defer cancel()
	parts := windows.New(quietLogger())
	t.Cleanup(func() { _ = parts.Close() })
	dataDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	l, err := lab.Open(lab.Options{DataDir: dataDir, TimeScale: 0, Logger: logger}, system.New(parts.Platform))
	if err != nil {
		t.Fatal(err)
	}
	cache := imageCache{dir: filepath.Join(here, "image-cache"), name: "debian-13"}
	cache.lend(t, l)
	closed := false
	closeLab := func() {
		if closed {
			return
		}
		closed = true
		if err := l.Close(); err != nil {
			t.Errorf("closing the lab: %v", err)
		}
		// the appliance, and the server of a test that failed before its
		// delete
		found, _ := parts.Hypervisor.FindVMs(context.Background(), system.VMTag)
		for _, vm := range found {
			if err := parts.Hypervisor.DeleteVM(context.Background(), vm.Name); err != nil && !errors.Is(err, platform.ErrNotFound) {
				t.Errorf("removing %s: %v", vm.Name, err)
			}
		}
	}
	t.Cleanup(closeLab)
	if err := l.StartMachine(ctx, lab.MachineOptions{ApplianceRelease: release, ServerShutdown: 30 * time.Second}); err != nil {
		t.Fatal(err)
	}
	api := newCloudAPI(t, l)

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
		"name": "itest-web", "server_type": "cpx22", "image": "debian-13", "location": "fsn1", "user_data": checkUserData,
	}, http.StatusCreated, &created)
	id := created.Server.ID
	ipv4 := created.Server.PublicNet.IPv4.IP
	ipv6 := strings.TrimSuffix(created.Server.PublicNet.IPv6.IP, "/64") + "1"
	t.Logf("server %d: %s and %s", id, ipv4, ipv6)
	waitForSSH(ctx, t, ipv4)
	t.Logf("SSH answered on %s %s after the create", ipv4, time.Since(started).Round(100*time.Millisecond))
	cache.keep(t, l)

	for _, address := range []string{ipv4, ipv6} {
		checkMetadata(ctx, t, address, map[string]string{
			"instance-id": fmt.Sprint(id), "hostname": "itest-web", "public-ipv4": ipv4, "region": "eu-central",
		})
	}

	reset := time.Now()
	api.action(t, id, "reset")
	time.Sleep(2 * time.Second) // the reset takes the guest down at once; SSH may answer from before
	waitForSSH(ctx, t, ipv4)
	t.Logf("SSH answered again %s after the reset", time.Since(reset).Round(100*time.Millisecond))

	shutdown := time.Now()
	api.action(t, id, "shutdown")
	waitForStatus(ctx, t, api, id, "off")
	t.Logf("the guest shut down and the server was off %s after the shutdown", time.Since(shutdown).Round(100*time.Millisecond))

	api.do(t, http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil, http.StatusOK, nil)
	deadline := time.Now().Add(time.Minute)
	for {
		if _, err := parts.Hypervisor.VMState(ctx, lab.ServerVM(id)); errors.Is(err, platform.ErrNotFound) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the server's VM is still there a minute after the delete")
		}
		time.Sleep(200 * time.Millisecond)
	}

	closeLab()
	cache.store(t)
	found, err := parts.Hypervisor.FindVMs(ctx, system.VMTag)
	if err != nil || len(found) > 0 {
		t.Errorf("the lab's VMs left: %v, %v", found, err)
	}
}

// cloudAPI calls the lab's Cloud API as a project's tool would.
type cloudAPI struct {
	url   string
	token string
}

// newCloudAPI serves the lab's Cloud API on a local port, with a token of a
// new project.
func newCloudAPI(t *testing.T, l *lab.Lab) cloudAPI {
	t.Helper()
	project, err := l.CreateProject("itest", state.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := l.CreateToken(project.ID, "itest", state.PermissionReadWrite)
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
		tx.Lab().Images = map[string]state.CachedImage{c.name: kept}
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
