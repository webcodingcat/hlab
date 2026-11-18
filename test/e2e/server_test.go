//go:build e2e

package e2e

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// testSRV01: the server has its name as hostname, accepts the key for root
// and has run the user_data.
func testSRV01(t *testing.T) {
	e, srv := sharedServer(t)

	// BatchMode with only the run's key: success means the key was accepted.
	r, err := e.ssh(srv, time.Minute, "", "hostname")
	if err != nil {
		t.Fatalf("ssh root@%s with the key: %v\n%s", srv.IPv4, err, r)
	}
	if got := strings.TrimSpace(r.Stdout); got != srv.Name {
		t.Errorf("hostname %q, want %q", got, srv.Name)
	}

	r, err = e.ssh(srv, time.Minute, "", "cat "+userDataMarker)
	if err != nil {
		t.Fatalf("user_data marker: %v\n%s", err, r)
	}
	if got := strings.TrimSpace(r.Stdout); got != userDataValue {
		t.Errorf("marker %q, want %q", got, userDataValue)
	}
}

// testSRV03: the public IPv4 is a /32 on eth0 with default route via
// 172.31.1.1 on-link, and ssh from the host works.
func testSRV03(t *testing.T) {
	e, srv := sharedServer(t)

	r, err := e.ssh(srv, time.Minute, "", "ip -j -4 addr show dev eth0")
	if err != nil {
		t.Fatalf("ssh root@%s: %v\n%s", srv.IPv4, err, r)
	}
	var links []struct {
		AddrInfo []struct {
			Local     string `json:"local"`
			PrefixLen int    `json:"prefixlen"`
		} `json:"addr_info"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &links); err != nil {
		t.Fatalf("ip addr: %v\n%s", err, r.Stdout)
	}
	found := false
	for _, l := range links {
		for _, a := range l.AddrInfo {
			if a.Local == srv.IPv4 {
				found = true
				if a.PrefixLen != 32 {
					t.Errorf("%s on eth0 has prefix /%d, want /32", a.Local, a.PrefixLen)
				}
			}
		}
	}
	if !found {
		t.Errorf("public IPv4 %s not on eth0:\n%s", srv.IPv4, r.Stdout)
	}

	r, err = e.ssh(srv, time.Minute, "", "ip -j -4 route show default")
	if err != nil {
		t.Fatalf("ip route: %v\n%s", err, r)
	}
	type route struct {
		Gateway string   `json:"gateway"`
		Dev     string   `json:"dev"`
		Flags   []string `json:"flags"`
	}
	var routes []route
	if err := json.Unmarshal([]byte(r.Stdout), &routes); err != nil {
		t.Fatalf("ip route: %v\n%s", err, r.Stdout)
	}
	if !slices.ContainsFunc(routes, func(rt route) bool {
		return rt.Gateway == "172.31.1.1" && rt.Dev == "eth0" && slices.Contains(rt.Flags, "onlink")
	}) {
		t.Errorf("no default route via 172.31.1.1 on-link on eth0:\n%s", r.Stdout)
	}
}

// testSRV05: the server reaches the internet by DNS name over HTTPS.
func testSRV05(t *testing.T) {
	e, srv := sharedServer(t)
	r, err := e.ssh(srv, 3*time.Minute, "", "curl -sS -o /dev/null --max-time 60 https://cloud-images.ubuntu.com/")
	if err != nil || r.ExitCode != 0 {
		t.Errorf("curl https://cloud-images.ubuntu.com/ from %s: %v\n%s", srv.Name, err, r)
	}
}

// testSRV16: deleting a server removes its VM and disk file, and the next
// server does not get the released IP at once.
func testSRV16(t *testing.T) {
	e := sharedEnv(t)

	first, err := e.createServer(harness.runID + "-srv16-a")
	if err != nil {
		t.Fatal(err)
	}
	vm := vmName(first.ID)
	disks, err := vmDisks(vm)
	if err != nil {
		t.Fatalf("disks of VM %s: %v", vm, err)
	}
	if len(disks) == 0 {
		t.Fatalf("VM %s has no disk", vm)
	}

	if err := e.deleteServer(first.Name); err != nil {
		t.Fatal(err)
	}
	if err := waitFor(2*time.Minute, 2*time.Second, func() (bool, error) {
		exists, err := vmExists(vm)
		return !exists, err
	}); err != nil {
		t.Errorf("VM %s still exists after delete: %v", vm, err)
	}
	for _, d := range disks {
		if err := waitFor(2*time.Minute, 2*time.Second, func() (bool, error) {
			return notExist(d.Path), nil
		}); err != nil {
			t.Errorf("disk %s still exists after delete: %v", d.Path, err)
		}
	}

	next, err := e.createServer(harness.runID + "-srv16-b")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := e.deleteServer(next.Name); err != nil {
			t.Error(err)
		}
	})
	if next.IPv4 == first.IPv4 {
		t.Errorf("next server got the released IP %s at once", first.IPv4)
	}
}

// testSRV19: the server's disk is a dynamic VHDX under the data folder.
func testSRV19(t *testing.T) {
	_, srv := sharedServer(t)
	vm := vmName(srv.ID)
	disks, err := vmDisks(vm)
	if err != nil {
		t.Fatalf("disks of VM %s: %v", vm, err)
	}
	if len(disks) == 0 {
		t.Fatalf("VM %s has no disk", vm)
	}
	folder := dataFolder()
	for _, d := range disks {
		if d.VhdType != "Dynamic" {
			t.Errorf("disk %s is %s, want Dynamic", d.Path, d.VhdType)
		}
		if !strings.EqualFold(filepath.Ext(d.Path), ".vhdx") {
			t.Errorf("disk %s is not a VHDX", d.Path)
		}
		if !within(d.Path, folder) {
			t.Errorf("disk %s is not under the data folder %s", d.Path, folder)
		}
	}
}
