//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
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

// ownServerSlots bounds how many servers of single rows exist at once, so
// the parallel rows stay within the host's resources.
var ownServerSlots = make(chan struct{}, 3)

// ownServer creates a server with the run's key and user_data for one row,
// waits until it is ready and deletes it when the row ends.
func ownServer(t *testing.T, e *env, suffix string) *server {
	t.Helper()
	takeServerSlot(t)
	name := harness.runID + "-" + suffix
	t.Cleanup(func() { deleteIfCreated(t, e, name) })
	srv, err := e.createServer(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.waitReady(srv); err != nil {
		t.Fatal(err)
	}
	return srv
}

// takeServerSlot waits for a free slot and gives it back when the row ends
// (after the row's server is deleted, as cleanups run in reverse order).
func takeServerSlot(t *testing.T) {
	t.Helper()
	ownServerSlots <- struct{}{}
	t.Cleanup(func() { <-ownServerSlots })
}

// deleteIfCreated deletes the server if it exists; a failed create leaves
// nothing to delete.
func deleteIfCreated(t *testing.T, e *env, name string) {
	t.Helper()
	if _, err := e.describeServer(name); err != nil {
		return
	}
	if err := e.deleteServer(name); err != nil {
		t.Error(err)
	}
}

// hcloudAction runs an hcloud command that waits for its action.
func (e *env) hcloudAction(t *testing.T, timeout time.Duration, args ...string) {
	t.Helper()
	if r, err := e.hcloudRun(context.Background(), timeout, args...); err != nil {
		t.Fatalf("hcloud %s: %v\n%s", strings.Join(args, " "), err, r)
	}
}

// waitSSH waits until SSH with the run's key answers.
func (e *env) waitSSH(t *testing.T, srv *server, timeout time.Duration) {
	t.Helper()
	var last result
	if err := waitFor(timeout, 5*time.Second, func() (bool, error) {
		r, err := e.ssh(srv, 30*time.Second, "", "true")
		last = r
		return err == nil && r.ExitCode == 0, nil
	}); err != nil {
		t.Fatalf("ssh root@%s: %v\n%s", srv.IPv4, err, last)
	}
}

func (e *env) bootID(srv *server) (string, error) {
	r, err := e.ssh(srv, 30*time.Second, "", "cat /proc/sys/kernel/random/boot_id")
	if err != nil || r.ExitCode != 0 {
		return "", fmt.Errorf("boot_id of %s: %v\n%s", srv.Name, err, r)
	}
	return strings.TrimSpace(r.Stdout), nil
}

// testSRV02: a server created without SSH keys accepts an SSH login as root
// with the root_password the create returned.
func testSRV02(t *testing.T) {
	e := sharedEnv(t)
	takeServerSlot(t)
	name := harness.runID + "-srv02"
	t.Cleanup(func() { deleteIfCreated(t, e, name) })

	var body struct {
		Server struct {
			ID        int64 `json:"id"`
			PublicNet struct {
				IPv4 struct {
					IP string `json:"ip"`
				} `json:"ipv4"`
			} `json:"public_net"`
		} `json:"server"`
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
		RootPassword *string `json:"root_password"`
	}
	status, aerr, err := e.apiDo(context.Background(), http.MethodPost, "/servers", map[string]any{
		"name": name, "server_type": serverType, "location": serverLocation, "image": serverImage,
	}, &body)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusCreated {
		t.Fatalf("POST /servers without SSH keys: status %d %s: %s", status, aerr.Code, aerr.Message)
	}
	if body.RootPassword == nil || *body.RootPassword == "" {
		t.Fatal("create without SSH keys returned no root_password")
	}
	if err := e.waitAction(body.Action.ID, 45*time.Minute); err != nil {
		t.Fatal(err)
	}
	srv := &server{ID: body.Server.ID, Name: name, IPv4: body.Server.PublicNet.IPv4.IP}

	// cloud-init sets the password during the first boot; until then the
	// login is refused.
	var last result
	if err := waitFor(15*time.Minute, 5*time.Second, func() (bool, error) {
		r, err := sshPassword(srv, *body.RootPassword, 30*time.Second, "id -un")
		last = r
		return err == nil && strings.TrimSpace(r.Stdout) == "root", nil
	}); err != nil {
		t.Fatalf("ssh root@%s with the returned password: %v\n%s", srv.IPv4, err, last)
	}
}

// testSRV07: power actions change the VM's real state and the status.
func testSRV07(t *testing.T) {
	e := sharedEnv(t)
	srv := ownServer(t, e, "srv07")

	e.hcloudAction(t, 5*time.Minute, "server", "poweroff", srv.Name)
	if err := e.waitStatus(srv.ID, "off", 2*time.Minute); err != nil {
		t.Fatalf("poweroff: %v", err)
	}
	if err := waitFor(2*time.Minute, 5*time.Second, func() (bool, error) {
		r, err := e.ssh(srv, 30*time.Second, "", "true")
		return err != nil || r.ExitCode != 0, nil
	}); err != nil {
		t.Fatalf("SSH still answers after poweroff: %v", err)
	}

	e.hcloudAction(t, 5*time.Minute, "server", "poweron", srv.Name)
	if err := e.waitStatus(srv.ID, "running", 5*time.Minute); err != nil {
		t.Fatalf("poweron: %v", err)
	}
	e.waitSSH(t, srv, 10*time.Minute)

	// shutdown succeeds once the request is sent; the guest powers off itself.
	e.hcloudAction(t, 5*time.Minute, "server", "shutdown", srv.Name)
	if err := e.waitStatus(srv.ID, "off", 5*time.Minute); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	e.hcloudAction(t, 5*time.Minute, "server", "poweron", srv.Name)
	if err := e.waitStatus(srv.ID, "running", 5*time.Minute); err != nil {
		t.Fatalf("poweron after shutdown: %v", err)
	}
	e.waitSSH(t, srv, 10*time.Minute)

	for _, op := range []string{"reboot", "reset"} {
		before, err := e.bootID(srv)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		e.hcloudAction(t, 5*time.Minute, "server", op, srv.Name)
		if err := waitFor(10*time.Minute, 5*time.Second, func() (bool, error) {
			id, err := e.bootID(srv)
			return err == nil && id != "" && id != before, nil
		}); err != nil {
			t.Fatalf("%s: SSH did not come back with a new boot_id: %v", op, err)
		}
		t.Logf("%s: SSH back with a new boot_id after %s", op, time.Since(start).Round(time.Second))
		if err := e.waitStatus(srv.ID, "running", 2*time.Minute); err != nil {
			t.Fatalf("%s: %v", op, err)
		}
	}
}

// systemImage is a system image as GET /images lists it.
type systemImage struct {
	Name       string  `json:"name"`
	OSFlavor   string  `json:"os_flavor"`
	OSVersion  string  `json:"os_version"`
	Deprecated *string `json:"deprecated"`
}

// otherSystemImage is an x86 system image other than the run's image,
// preferring another distribution.
func otherSystemImage(t *testing.T, e *env) systemImage {
	t.Helper()
	var body struct {
		Images []systemImage `json:"images"`
	}
	status, aerr, err := e.apiDo(context.Background(), http.MethodGet, "/images?type=system&architecture=x86&per_page=50", nil, &body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET /images: %v (status %d %s)", err, status, aerr.Code)
	}
	var flavor string
	for _, img := range body.Images {
		if img.Name == serverImage {
			flavor = img.OSFlavor
		}
	}
	var pick *systemImage
	for i, img := range body.Images {
		if img.Name == serverImage || img.Deprecated != nil || img.Name == "" {
			continue
		}
		if pick == nil || (pick.OSFlavor == flavor && img.OSFlavor != flavor) {
			pick = &body.Images[i]
		}
	}
	if pick == nil {
		t.Fatalf("no x86 system image other than %s", serverImage)
	}
	return *pick
}

// publicNet is the server's public IPv4 and IPv6 as the API reports them.
func (e *env) publicNet(t *testing.T, id int64) (ipv4, ipv6 string) {
	t.Helper()
	var body struct {
		Server struct {
			PublicNet struct {
				IPv4 *struct {
					IP string `json:"ip"`
				} `json:"ipv4"`
				IPv6 *struct {
					IP string `json:"ip"`
				} `json:"ipv6"`
			} `json:"public_net"`
		} `json:"server"`
	}
	status, aerr, err := e.apiDo(context.Background(), http.MethodGet, fmt.Sprintf("/servers/%d", id), nil, &body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET /servers/%d: %v (status %d %s)", id, err, status, aerr.Code)
	}
	if p := body.Server.PublicNet.IPv4; p != nil {
		ipv4 = p.IP
	}
	if p := body.Server.PublicNet.IPv6; p != nil {
		ipv6 = p.IP
	}
	return ipv4, ipv6
}

const srv08Marker = "/root/hlab-srv08-marker"

// testSRV08: rebuild to another system image keeps ID and addresses, gives
// a fresh disk with the new image and runs the user_data again.
func testSRV08(t *testing.T) {
	e := sharedEnv(t)
	srv := ownServer(t, e, "srv08")
	img := otherSystemImage(t, e)
	ipv4, ipv6 := e.publicNet(t, srv.ID)

	if r, err := e.ssh(srv, time.Minute, "", "echo hlab-srv08 > "+srv08Marker); err != nil {
		t.Fatalf("write marker: %v\n%s", err, r)
	}

	// The first rebuild to an image downloads and converts it.
	e.hcloudAction(t, 45*time.Minute, "server", "rebuild", "--image", img.Name, srv.Name)

	after, err := e.describeServer(srv.Name)
	if err != nil {
		t.Fatal(err)
	}
	if after.ID != srv.ID {
		t.Errorf("ID %d after rebuild, want %d", after.ID, srv.ID)
	}
	gotV4, gotV6 := e.publicNet(t, srv.ID)
	if gotV4 != ipv4 || gotV6 != ipv6 {
		t.Errorf("public addresses after rebuild %s, %s; want %s, %s", gotV4, gotV6, ipv4, ipv6)
	}

	// The new disk has a new host key; the key login still proceeds.
	if err := e.waitReady(srv); err != nil {
		t.Fatalf("SSH with the key after rebuild: %v", err)
	}
	r, err := e.ssh(srv, time.Minute, "", `. /etc/os-release; echo "$ID $VERSION_ID"`)
	if err != nil {
		t.Fatalf("os-release: %v\n%s", err, r)
	}
	if got, want := strings.TrimSpace(r.Stdout), img.OSFlavor+" "+img.OSVersion; got != want {
		t.Errorf("/etc/os-release names %q, want %q (image %s)", got, want, img.Name)
	}
	r, err = e.ssh(srv, time.Minute, "", "if test -e "+srv08Marker+"; then echo present; else echo absent; fi")
	if err != nil || strings.TrimSpace(r.Stdout) != "absent" {
		t.Errorf("marker %s after rebuild: %v\n%s", srv08Marker, err, r)
	}
	r, err = e.ssh(srv, time.Minute, "", "cat "+userDataMarker)
	if err != nil || strings.TrimSpace(r.Stdout) != userDataValue {
		t.Errorf("user_data did not run again after rebuild: %v\n%s", err, r)
	}
}

// serverTypeInfo is a server type as GET /server_types lists it.
type serverTypeInfo struct {
	Name         string  `json:"name"`
	Cores        int     `json:"cores"`
	Memory       float64 `json:"memory"` // GB
	Disk         float64 `json:"disk"`   // GB
	StorageType  string  `json:"storage_type"`
	Architecture string  `json:"architecture"`
	Deprecated   bool    `json:"deprecated"`
	Locations    []struct {
		Name        string          `json:"name"`
		Deprecation json.RawMessage `json:"deprecation"`
	} `json:"locations"`
}

// usableIn reports whether st is offered and not deprecated in location.
func (st serverTypeInfo) usableIn(location string) bool {
	for _, l := range st.Locations {
		if l.Name == location {
			return len(l.Deprecation) == 0 || string(l.Deprecation) == "null"
		}
	}
	return false
}

// largerType returns the run's server type and the smallest x86 type with
// the same storage type and more cores, RAM and disk, offered and not
// deprecated in the run's location.
func largerType(t *testing.T, e *env) (small, big serverTypeInfo) {
	t.Helper()
	var body struct {
		ServerTypes []serverTypeInfo `json:"server_types"`
	}
	status, aerr, err := e.apiDo(context.Background(), http.MethodGet, "/server_types?per_page=50", nil, &body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET /server_types: %v (status %d %s)", err, status, aerr.Code)
	}
	found := false
	for _, st := range body.ServerTypes {
		if st.Name == serverType {
			small, found = st, true
		}
	}
	if !found {
		t.Fatalf("server type %s not listed", serverType)
	}
	var pick *serverTypeInfo
	for i, st := range body.ServerTypes {
		if st.Deprecated || !st.usableIn(serverLocation) || st.Architecture != "x86" || st.StorageType != small.StorageType ||
			st.Cores <= small.Cores || st.Memory <= small.Memory || st.Disk <= small.Disk {
			continue
		}
		if pick == nil || st.Memory < pick.Memory || (st.Memory == pick.Memory && st.Cores < pick.Cores) {
			pick = &body.ServerTypes[i]
		}
	}
	if pick == nil {
		t.Fatalf("no x86 server type larger than %s", serverType)
	}
	return small, *pick
}

// testSRV09: change_type with upgrade_disk on a stopped server gives the new
// cores, RAM and disk after the automatic start; a running server is refused.
func testSRV09(t *testing.T) {
	e := sharedEnv(t)
	small, big := largerType(t, e)
	srv := ownServer(t, e, "srv09")
	path := fmt.Sprintf("/servers/%d/actions/change_type", srv.ID)
	req := map[string]any{"server_type": big.Name, "upgrade_disk": true}

	status, aerr, err := e.apiDo(context.Background(), http.MethodPost, path, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusUnprocessableEntity || aerr.Code != "server_not_stopped" {
		t.Errorf("change_type on the running server: status %d %q, want 422 server_not_stopped", status, aerr.Code)
	}

	e.hcloudAction(t, 5*time.Minute, "server", "poweroff", srv.Name)
	if err := e.waitStatus(srv.ID, "off", 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	status, aerr, err = e.apiDo(context.Background(), http.MethodPost, path, req, &body)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusCreated {
		t.Fatalf("change_type to %s on the stopped server: status %d %s: %s", big.Name, status, aerr.Code, aerr.Message)
	}
	if err := e.waitAction(body.Action.ID, 15*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := e.waitStatus(srv.ID, "running", 5*time.Minute); err != nil {
		t.Fatalf("no automatic start after change_type: %v", err)
	}
	if err := e.waitReady(srv); err != nil {
		t.Fatal(err)
	}

	r, err := e.ssh(srv, time.Minute, "", "nproc; awk '/^MemTotal:/ {print $2}' /proc/meminfo; df -B1 --output=size / | tail -n 1")
	if err != nil {
		t.Fatalf("ssh: %v\n%s", err, r)
	}
	f := strings.Fields(r.Stdout)
	if len(f) != 3 {
		t.Fatalf("unexpected output:\n%s", r.Stdout)
	}
	nproc, err1 := strconv.Atoi(f[0])
	memKB, err2 := strconv.ParseFloat(f[1], 64)
	rootBytes, err3 := strconv.ParseFloat(f[2], 64)
	if err := errors.Join(err1, err2, err3); err != nil {
		t.Fatalf("unexpected output: %v\n%s", err, r.Stdout)
	}
	if nproc != big.Cores {
		t.Errorf("nproc %d, want %d of %s", nproc, big.Cores, big.Name)
	}
	// MemTotal lacks what firmware and kernel keep; it must exceed the old
	// type's RAM and come close to the new one's.
	const gib = 1 << 30
	mem := memKB * 1024
	if mem <= small.Memory*gib || mem < 0.85*big.Memory*gib || mem > big.Memory*gib {
		t.Errorf("MemTotal %.0f bytes, want close below %.0f GiB of %s", mem, big.Memory, big.Name)
	}
	// The root filesystem lacks the boot partitions and metadata; it must
	// exceed the old disk and come close to the new one.
	if rootBytes <= small.Disk*gib || rootBytes < 0.9*big.Disk*1e9 {
		t.Errorf("root filesystem %.0f bytes, want close to %.0f GB of %s", rootBytes, big.Disk, big.Name)
	}
}

// testSRV10: reset_password on the shared server gives a password that
// logs in as root via SSH once the action succeeded.
func testSRV10(t *testing.T) {
	e, srv := sharedServer(t)
	var body struct {
		RootPassword string `json:"root_password"`
		Action       struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	status, aerr, err := e.apiDo(context.Background(), http.MethodPost,
		fmt.Sprintf("/servers/%d/actions/reset_password", srv.ID), nil, &body)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusCreated {
		t.Fatalf("reset_password: status %d %s: %s", status, aerr.Code, aerr.Message)
	}
	if body.RootPassword == "" {
		t.Fatal("reset_password returned no root_password")
	}
	if err := e.waitAction(body.Action.ID, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	r, err := sshPassword(srv, body.RootPassword, time.Minute, "id -un")
	if err != nil || strings.TrimSpace(r.Stdout) != "root" {
		t.Errorf("ssh root@%s with the new password: %v\n%s", srv.IPv4, err, r)
	}
}

// srv21Series are the nine series of type=cpu,disk,network.
var srv21Series = []string{
	"cpu",
	"disk.0.iops.read", "disk.0.iops.write", "disk.0.bandwidth.read", "disk.0.bandwidth.write",
	"network.0.pps.in", "network.0.pps.out", "network.0.bandwidth.in", "network.0.bandwidth.out",
}

// testSRV21: after disk and network load the metrics have all nine series,
// with disk write and network bandwidth above zero.
func testSRV21(t *testing.T) {
	e, srv := sharedServer(t)
	load := "dd if=/dev/zero of=/var/tmp/hlab-srv21 bs=1M count=256 oflag=direct status=none; rm -f /var/tmp/hlab-srv21; " +
		"ping -q -c 20 -i 0.2 -s 1200 172.31.1.1 >/dev/null 2>&1; true"
	if r, err := e.ssh(srv, 5*time.Minute, "", load); err != nil {
		t.Fatalf("load: %v\n%s", err, r)
	}

	var problem string
	// Samples are taken every 10 s; rates need two of them after the load.
	err := waitFor(3*time.Minute, 10*time.Second, func() (bool, error) {
		end := time.Now().UTC()
		q := url.Values{}
		q.Set("type", "cpu,disk,network")
		q.Set("start", end.Add(-10*time.Minute).Format(time.RFC3339))
		q.Set("end", end.Format(time.RFC3339))
		var body struct {
			Metrics struct {
				TimeSeries map[string]struct {
					Values [][]any `json:"values"`
				} `json:"time_series"`
			} `json:"metrics"`
		}
		status, aerr, err := e.apiDo(context.Background(), http.MethodGet,
			fmt.Sprintf("/servers/%d/metrics?%s", srv.ID, q.Encode()), nil, &body)
		if err != nil {
			problem = err.Error()
			return false, nil
		}
		if status != http.StatusOK {
			problem = fmt.Sprintf("status %d %s: %s", status, aerr.Code, aerr.Message)
			return false, nil
		}
		peak := map[string]float64{}
		for _, name := range srv21Series {
			s, ok := body.Metrics.TimeSeries[name]
			if !ok || len(s.Values) == 0 {
				problem = "series " + name + " missing or without points"
				return false, nil
			}
			for _, v := range s.Values {
				if len(v) != 2 {
					return false, fmt.Errorf("series %s: point %v is not [time, value]", name, v)
				}
				str, ok := v[1].(string)
				if !ok {
					return false, fmt.Errorf("series %s: value %v is not a string", name, v[1])
				}
				f, err := strconv.ParseFloat(str, 64)
				if err != nil {
					return false, fmt.Errorf("series %s: value %q: %w", name, str, err)
				}
				peak[name] = max(peak[name], f)
			}
		}
		for _, name := range []string{"disk.0.bandwidth.write", "network.0.bandwidth.in", "network.0.bandwidth.out"} {
			if peak[name] <= 0 {
				problem = "series " + name + " never above zero"
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		t.Errorf("metrics of %s: %v (last: %s)", srv.Name, err, problem)
	}
}
