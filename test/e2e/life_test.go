//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	// hostAdapter is the host adapter of the dev profile's switch.
	hostAdapter = "vEthernet (hlab-dev)"
	// publicRange is the dev cloud's public range (the default).
	publicRange = "198.19.0.0/16"
	// lifeData is what the fixture writes to its volume.
	lifeData = "hlab-e2e-life-volume"
)

// baseNamespaces are the NRPT namespaces every start adds for the default
// public range: hetzner.internal and its reverse zone.
var baseNamespaces = []string{".hetzner.internal", ".19.198.in-addr.arpa"}

// lifeUserData runs a web server answering the guest's hostname on ports
// 80 and 8081 as enabled units, so they come back after a reboot.
const lifeUserData = "#cloud-config\nwrite_files:\n" +
	"  - path: /etc/systemd/system/hlab-life-web@.service\n" +
	"    content: |\n" +
	"      [Unit]\n" +
	"      Description=hlab e2e web server on port %i\n" +
	"      After=network.target\n" +
	"      [Service]\n" +
	"      ExecStartPre=/bin/sh -c 'mkdir -p /srv/life && hostname > /srv/life/index.html'\n" +
	"      ExecStart=/usr/bin/python3 -m http.server %i --directory /srv/life\n" +
	"      Restart=always\n" +
	"      [Install]\n" +
	"      WantedBy=multi-user.target\n" +
	"runcmd:\n" +
	"  - [systemctl, daemon-reload]\n" +
	"  - [systemctl, enable, --now, hlab-life-web@80.service, hlab-life-web@8081.service]\n"

// netplanID prints the netplan ID of the ethernet with MAC $1 or name $2.
const netplanID = `python3 -c 'import glob, sys, yaml
mac, dev = sys.argv[1], sys.argv[2]
for f in sorted(glob.glob("/etc/netplan/*.yaml")):
    doc = yaml.safe_load(open(f)) or {}
    for k, v in ((doc.get("network") or {}).get("ethernets") or {}).items():
        m = (v or {}).get("match") or {}
        if k == dev or str(m.get("macaddress", "")).lower() == mac:
            print(k)
            sys.exit(0)
sys.exit(1)' `

// lifeFixture is what the LIFE rows keep across the cloud's stop and
// start: servers A and B running in a network, server C off, a floating
// IP configured on A through netplan, a firewall on A allowing tcp 22 and
// 80, a load balancer with a tcp service on A and B, a volume automounted
// on A with data, and a zone with a record pointing at A.
type lifeFixture struct {
	once sync.Once
	e    *env
	err  error

	names   []string
	a, b    *member
	c       *server
	network int64
	fipID   int64
	fip     string
	fwID    int64
	lbID    int64
	lbIP    string
	volID   int64
	zone    string
	zoneID  int64
	slots   int
	// SBOX-07: a box with data, a subaccount and a snapshot, added by
	// testSBOX07 before the save cycle.
	box      *storageBox
	boxSub   *storageBox
	boxSnap  string
	boxData  string // local copy of life.bin in the box
	mu       sync.Mutex
	released bool
}

var lifeFix lifeFixture

// lifeCycle is one stop and start of the cloud with a stop behaviour and
// what the rows check about it.
type lifeCycle struct {
	once sync.Once
	err  error // the cycle could not be run

	before, after map[int64]boot   // A and B
	status        map[int64]string // A, B and C after the start
	cState        string           // C's VM state after the start
	stopped       []string         // LIFE-07 findings while stopped
	started       []string         // LIFE-07 findings after the start
	fixture       []string         // LIFE-04 findings after the start
}

type boot struct {
	id     string
	uptime float64
}

var lifeCycles = map[string]*lifeCycle{"save": {}, "shutdown": {}}

// settings.toml of the dev profile as found before the first cycle.
var lifeSettings struct {
	mu      sync.Mutex
	saved   bool
	existed bool
	orig    []byte
}

// testLIFE02: with "save", the running server resumes after stop and
// start with the same boot and a grown uptime.
func testLIFE02(t *testing.T) {
	f := sharedLife(t)
	c := runLifeCycle(t, f, "save")
	for _, m := range []*member{f.a, f.b} {
		before, after := c.before[m.ID], c.after[m.ID]
		if after.id != before.id {
			t.Errorf("%s: boot_id %s after stop and start with save, want the same boot %s", m.Name, after.id, before.id)
		}
		if after.uptime <= before.uptime {
			t.Errorf("%s: uptime %.2f s after stop and start with save, want more than %.2f s", m.Name, after.uptime, before.uptime)
		}
	}
}

// testLIFE03: with "shutdown", the servers that ran run again from a new
// boot after stop and start; the server that was off stays off.
func testLIFE03(t *testing.T) {
	f := sharedLife(t)
	c := runLifeCycle(t, f, "shutdown")
	for _, m := range []*member{f.a, f.b} {
		if c.after[m.ID].id == c.before[m.ID].id {
			t.Errorf("%s: boot_id %s unchanged after stop and start with shutdown, want a new boot", m.Name, c.before[m.ID].id)
		}
		if got := c.status[m.ID]; got != "running" {
			t.Errorf("%s: status %q after stop and start with shutdown, want running", m.Name, got)
		}
	}
	if got := c.status[f.c.ID]; got != "off" {
		t.Errorf("%s: status %q after stop and start with shutdown, want off", f.c.Name, got)
	}
	if c.cState != "Off" {
		t.Errorf("%s: VM state %q after stop and start with shutdown, want Off", f.c.Name, c.cState)
	}
}

// testLIFE04: after each stop and start every resource of the fixture
// works.
func testLIFE04(t *testing.T) {
	f := sharedLife(t)
	for _, b := range []string{"save", "shutdown"} {
		for _, msg := range runLifeCycle(t, f, b).fixture {
			t.Errorf("after stop and start with %s: %s", b, msg)
		}
	}
}

// testLIFE07: while the cloud is stopped no route of the public range via
// the hlab adapter and no tagged NRPT rule exist and the forwarding of the
// hlab and WSL adapters is as recorded in hostnet-restore.json; after the
// start the route and the NRPT rules are back.
func testLIFE07(t *testing.T) {
	f := sharedLife(t)
	for _, b := range []string{"save", "shutdown"} {
		c := runLifeCycle(t, f, b)
		for _, msg := range c.stopped {
			t.Errorf("stopped (%s): %s", b, msg)
		}
		for _, msg := range c.started {
			t.Errorf("started again (%s): %s", b, msg)
		}
	}
}

// testSBOX07: a box with data, a subaccount and a snapshot keeps all
// three over a stop and start of the cloud: data over SFTP, the
// subaccount's login, the snapshot in the list and under .zfs/snapshot.
func testSBOX07(t *testing.T) {
	f := sharedLife(t)
	if lifeCycles["save"].before != nil {
		t.Fatal("the save cycle ran before SBOX-07 added its storage box to the fixture")
	}
	f.addStorageBox(t)
	runLifeCycle(t, f, "save")
	e := f.e

	owner := passwordLogin(f.box, 22)
	back := filepath.Join(t.TempDir(), "back.bin")
	if eventually(t, "box reads life.bin over SFTP after stop and start", func() (result, error) {
		return hostSFTP(owner, fmt.Sprintf("get life.bin %s\n", sftpPath(back)))
	}) {
		sameFile(t, f.boxData, back, "life.bin after stop and start")
	}
	eventually(t, "subaccount logs in after stop and start", func() (result, error) {
		return hostSFTP(passwordLogin(f.boxSub, 22), "ls\n")
	})
	if names := snapshotNames(e.listSnapshots(t, f.box)); !slices.Contains(names, f.boxSnap) {
		t.Errorf("snapshots after stop and start %v, want %s", names, f.boxSnap)
	}
	in := ".zfs/snapshot/" + f.boxSnap + "/life.bin"
	zfsBack := filepath.Join(t.TempDir(), "zfs-back.bin")
	if eventually(t, "box reads "+in+" after stop and start", func() (result, error) {
		return hostSFTP(owner, fmt.Sprintf("get %s %s\n", in, sftpPath(zfsBack)))
	}) {
		sameFile(t, f.boxData, zfsBack, in+" after stop and start")
	}
}

// addStorageBox adds SBOX-07's box to the fixture: zfs_enabled, life.bin
// written over SFTP, a subaccount and a snapshot; releaseLifeFixture
// deletes it.
func (f *lifeFixture) addStorageBox(t *testing.T) {
	t.Helper()
	e := f.e
	box, err := e.createStorageBox(harness.runID+"-life-sbox", "bx11",
		map[string]bool{"reachable_externally": true, "zfs_enabled": true}, nil)
	if box != nil {
		f.mu.Lock()
		f.box = box
		f.mu.Unlock()
	}
	if err != nil {
		t.Fatal(err)
	}
	f.boxData = randomFile(t, t.TempDir(), "life.bin", 1<<20)
	if !eventually(t, "sftp put life.bin into "+box.Username, func() (result, error) {
		return hostSFTP(passwordLogin(box, 22), fmt.Sprintf("put %s life.bin\n", sftpPath(f.boxData)))
	}) {
		t.FailNow()
	}
	f.boxSub = e.createSubaccount(t, box, "life-sub", map[string]bool{"reachable_externally": true})
	if !eventually(t, "subaccount logs in before stop and start", func() (result, error) {
		return hostSFTP(passwordLogin(f.boxSub, 22), "ls\n")
	}) {
		t.FailNow()
	}
	f.boxSnap = e.createSnapshot(t, box, "life", nil).Name
}

// sharedLife returns the LIFE fixture, created once; releaseLifeFixture
// deletes it after the LIFE rows.
func sharedLife(t *testing.T) *lifeFixture {
	t.Helper()
	e := sharedEnv(t)
	f := &lifeFix
	f.once.Do(func() {
		f.e = e
		f.slots = serverSlots.acquire(3)
		f.err = f.create()
	})
	if f.err != nil {
		t.Fatalf("life fixture: %v", f.err)
	}
	return f
}

// runLifeCycle runs the stop and start with behaviour once and returns
// what was found.
func runLifeCycle(t *testing.T, f *lifeFixture, behaviour string) *lifeCycle {
	t.Helper()
	c := lifeCycles[behaviour]
	c.once.Do(func() { c.err = c.run(f, behaviour) })
	if c.err != nil {
		t.Fatalf("stop and start with %s: %v", behaviour, c.err)
	}
	return c
}

func (f *lifeFixture) create() error {
	e := f.e
	var err error
	if f.network, err = e.createNetwork(harness.runID+"-life", "10.80.0.0/16", "10.80.1.0/24", nil); err != nil {
		return err
	}
	file := filepath.Join(harness.tmp, "life-user-data.yaml")
	if err := os.WriteFile(file, []byte(lifeUserData), 0o600); err != nil {
		return err
	}
	suffixes := []string{"life-a", "life-b", "life-c"}
	srvs := make([]*server, len(suffixes))
	errs := make([]error, len(suffixes))
	var wg sync.WaitGroup
	for i, suffix := range suffixes {
		name := harness.runID + "-" + suffix
		f.names = append(f.names, name)
		wg.Add(1)
		go func() {
			defer wg.Done()
			srvs[i], errs[i] = e.createServerImage(name, serverImage,
				"--user-data-from-file", file, "--network", strconv.FormatInt(f.network, 10))
			if errs[i] == nil {
				errs[i] = e.waitReady(srvs[i])
			}
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}
	if f.a, err = e.loadMember(srvs[0], f.network); err != nil {
		return err
	}
	if f.b, err = e.loadMember(srvs[1], f.network); err != nil {
		return err
	}
	f.c = srvs[2]
	if err := e.postAction(fmt.Sprintf("/servers/%d/actions/poweroff", f.c.ID), nil, 5*time.Minute); err != nil {
		return err
	}
	if err := e.waitStatus(f.c.ID, "off", 2*time.Minute); err != nil {
		return err
	}

	steps := []func() error{f.createFloatingIP, f.createFirewall, f.createLB, f.createVolume, f.createZone}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	// Everything works before the first stop.
	if msgs := f.check(); len(msgs) > 0 {
		return fmt.Errorf("fixture does not work before the first stop: %s", strings.Join(msgs, "; "))
	}
	return nil
}

// createdRefs are the actions of a create answer.
type createdRefs struct {
	Action *struct {
		ID int64 `json:"id"`
	} `json:"action"`
	Actions []struct {
		ID int64 `json:"id"`
	} `json:"actions"`
	NextActions []struct {
		ID int64 `json:"id"`
	} `json:"next_actions"`
}

func (r createdRefs) wait(e *env, timeout time.Duration) error {
	var ids []int64
	if r.Action != nil {
		ids = append(ids, r.Action.ID)
	}
	for _, a := range r.Actions {
		ids = append(ids, a.ID)
	}
	for _, a := range r.NextActions {
		ids = append(ids, a.ID)
	}
	for _, id := range ids {
		if err := e.waitAction(id, timeout); err != nil {
			return err
		}
	}
	return nil
}

// createFloatingIP assigns a floating IP to A and configures it in A's
// netplan, so it survives a reboot.
func (f *lifeFixture) createFloatingIP() error {
	e := f.e
	var body struct {
		FloatingIP struct {
			ID int64  `json:"id"`
			IP string `json:"ip"`
		} `json:"floating_ip"`
		createdRefs
	}
	if err := e.postCreated("/floating_ips", map[string]any{
		"type": "ipv4", "server": f.a.ID, "name": harness.runID + "-life",
	}, &body); err != nil {
		return err
	}
	f.fipID, f.fip = body.FloatingIP.ID, body.FloatingIP.IP
	if err := body.wait(e, 5*time.Minute); err != nil {
		return fmt.Errorf("create floating IP: %w", err)
	}
	r, err := e.ssh(f.a.server, time.Minute, "", netplanID+f.a.pubMAC+" "+f.a.pubIf)
	id := strings.TrimSpace(r.Stdout)
	if err != nil || id == "" {
		return fmt.Errorf("%s: netplan ID of %s: %v\n%s", f.a.Name, f.a.pubIf, err, r)
	}
	conf := "network:\n  version: 2\n  ethernets:\n    " + id + ":\n      addresses:\n        - " + f.fip + "/32\n"
	if r, err := e.ssh(f.a.server, time.Minute, conf,
		"cat > /etc/netplan/60-hlab-life-fip.yaml && chmod 600 /etc/netplan/60-hlab-life-fip.yaml && "+
			"(nohup sh -c 'sleep 1; netplan apply' >/dev/null 2>&1 &)"); err != nil {
		return fmt.Errorf("%s: configure the floating IP: %v\n%s", f.a.Name, err, r)
	}
	return nil
}

func (f *lifeFixture) createFirewall() error {
	var body struct {
		Firewall struct {
			ID int64 `json:"id"`
		} `json:"firewall"`
		createdRefs
	}
	port80 := sshInRule()
	port80["port"] = "80"
	if err := f.e.postCreated("/firewalls", map[string]any{
		"name": harness.runID + "-life", "rules": []map[string]any{sshInRule(), port80},
		"apply_to": serverResource(f.a.server),
	}, &body); err != nil {
		return err
	}
	f.fwID = body.Firewall.ID
	return body.wait(f.e, 5*time.Minute)
}

func (f *lifeFixture) createLB() error {
	e := f.e
	target := func(s *server) map[string]any {
		return map[string]any{"type": "server", "server": map[string]any{"id": s.ID}, "use_private_ip": true}
	}
	var body struct {
		LoadBalancer struct {
			ID        int64 `json:"id"`
			PublicNet struct {
				IPv4 struct {
					IP string `json:"ip"`
				} `json:"ipv4"`
			} `json:"public_net"`
		} `json:"load_balancer"`
		createdRefs
	}
	if err := e.postCreated("/load_balancers", map[string]any{
		"name": harness.runID + "-life", "load_balancer_type": "lb11", "location": serverLocation,
		"network": f.network, "targets": []any{target(f.a.server), target(f.b.server)},
		"services": []any{map[string]any{"protocol": "tcp", "listen_port": 8080, "destination_port": 80, "proxyprotocol": false}},
	}, &body); err != nil {
		return err
	}
	f.lbID = body.LoadBalancer.ID
	if err := body.wait(e, 10*time.Minute); err != nil {
		return fmt.Errorf("create load balancer: %w", err)
	}
	var got struct {
		LoadBalancer struct {
			PublicNet struct {
				IPv4 struct {
					IP string `json:"ip"`
				} `json:"ipv4"`
			} `json:"public_net"`
		} `json:"load_balancer"`
	}
	if status, aerr, err := e.apiDo(context.Background(), http.MethodGet, fmt.Sprintf("/load_balancers/%d", f.lbID), nil, &got); err != nil || status != http.StatusOK {
		return fmt.Errorf("GET /load_balancers/%d: status %d %s: %v", f.lbID, status, aerr.Code, err)
	}
	f.lbIP = got.LoadBalancer.PublicNet.IPv4.IP
	return nil
}

// createVolume attaches a new ext4 volume to A with automount and writes
// lifeData to it.
func (f *lifeFixture) createVolume() error {
	e := f.e
	var body struct {
		Volume struct {
			ID int64 `json:"id"`
		} `json:"volume"`
		createdRefs
	}
	if err := e.postCreated("/volumes", map[string]any{
		"name": harness.runID + "-life", "size": 10, "location": serverLocation, "format": "ext4",
	}, &body); err != nil {
		return err
	}
	f.volID = body.Volume.ID
	if err := body.wait(e, 15*time.Minute); err != nil {
		return fmt.Errorf("create volume: %w", err)
	}
	if err := waitFor(15*time.Minute, 2*time.Second, func() (bool, error) {
		v, err := e.volume(f.volID)
		return err == nil && v.Status == "available", nil
	}); err != nil {
		return fmt.Errorf("volume %d not available: %w", f.volID, err)
	}
	if err := e.postAction(fmt.Sprintf("/volumes/%d/actions/attach", f.volID), map[string]any{
		"assignee": map[string]any{"id": f.a.ID, "type": "server"}, "automount": true,
	}, 15*time.Minute); err != nil {
		return err
	}
	mount := f.mount()
	var last result
	if err := waitFor(2*time.Minute, 2*time.Second, func() (bool, error) {
		r, err := e.ssh(f.a.server, 30*time.Second, "", "mountpoint -q "+mount)
		last = r
		return err == nil && r.ExitCode == 0, nil
	}); err != nil {
		return fmt.Errorf("%s not mounted on %s: %v\n%s", mount, f.a.Name, err, last)
	}
	if r, err := e.ssh(f.a.server, time.Minute, "", "echo "+lifeData+" > "+mount+"/life-data && sync"); err != nil {
		return fmt.Errorf("write to %s: %v\n%s", mount, err, r)
	}
	return nil
}

func (f *lifeFixture) mount() string { return fmt.Sprintf("/mnt/HC_Volume_%d", f.volID) }

func (f *lifeFixture) createZone() error {
	f.zone = zoneName("life")
	var body struct {
		Zone struct {
			ID int64 `json:"id"`
		} `json:"zone"`
		createdRefs
	}
	if err := f.e.postCreated("/zones", map[string]any{
		"name": f.zone, "mode": "primary",
		"rrsets": []map[string]any{{"name": "www", "type": "A", "records": []map[string]any{{"value": f.a.IPv4}}}},
	}, &body); err != nil {
		return err
	}
	f.zoneID = body.Zone.ID
	return body.wait(f.e, 5*time.Minute)
}

// check returns what of the fixture does not work: SSH to A and B, the
// private network, the floating IP, the firewall, the load balancer's tcp
// service, the volume with its data and the zone record from the host.
func (f *lifeFixture) check() []string {
	e := f.e
	var msgs []string
	fail := func(format string, a ...any) { msgs = append(msgs, fmt.Sprintf(format, a...)) }

	for _, m := range []*member{f.a, f.b} {
		if err := e.waitReady(m.server); err != nil {
			fail("SSH to %s on %s: %v", m.Name, m.IPv4, err)
		}
	}
	if len(msgs) > 0 {
		return msgs
	}

	var last result
	if err := waitFor(3*time.Minute, 3*time.Second, func() (bool, error) {
		r, err := e.ssh(f.a.server, 30*time.Second, "", "ping -c 1 -W 2 "+f.b.privIP)
		last = r
		return err == nil && r.ExitCode == 0, nil
	}); err != nil {
		fail("private network: %s cannot ping %s: %v\n%s", f.a.Name, f.b.privIP, err, last)
	}

	fipHost := &server{Name: f.fip, IPv4: f.fip}
	if err := waitFor(3*time.Minute, 5*time.Second, func() (bool, error) {
		r, err := e.ssh(fipHost, 30*time.Second, "", "hostname")
		last = r
		return err == nil && strings.TrimSpace(r.Stdout) == f.a.Name, nil
	}); err != nil {
		fail("floating IP: ssh root@%s did not answer with hostname %s: %v\n%s", f.fip, f.a.Name, err, last)
	}

	if err := waitFor(3*time.Minute, 3*time.Second, func() (bool, error) {
		return dialOK(f.a.IPv4, 80), nil
	}); err != nil {
		fail("firewall: allowed port 80 on %s does not connect: %v", f.a.IPv4, err)
	}
	if r, err := e.ssh(f.a.server, 30*time.Second, "", "ss -Hltn 'sport = :8081' | grep -q ."); err != nil || r.ExitCode != 0 {
		fail("firewall: nothing listens on port 8081 in %s, so the closed port proves nothing: %v\n%s", f.a.Name, err, r)
	} else if dialOK(f.a.IPv4, 8081) {
		fail("firewall: port 8081 on %s connects, the firewall allows only 22 and 80", f.a.IPv4)
	}

	var lbLast result
	if err := waitFor(3*time.Minute, 3*time.Second, func() (bool, error) {
		r, err := curlHost("http://" + f.lbIP + ":8080/")
		lbLast = r
		got := strings.TrimSpace(r.Stdout)
		return err == nil && (got == f.a.Name || got == f.b.Name), nil
	}); err != nil {
		fail("load balancer: tcp 8080 on %s did not answer with a target's hostname: %v\n%s", f.lbIP, err, lbLast)
	}

	if err := waitFor(3*time.Minute, 3*time.Second, func() (bool, error) {
		r, err := e.ssh(f.a.server, 30*time.Second, "", "cat "+f.mount()+"/life-data")
		last = r
		return err == nil && strings.TrimSpace(r.Stdout) == lifeData, nil
	}); err != nil {
		fail("volume: %s/life-data on %s does not hold %q: %v\n%s", f.mount(), f.a.Name, lifeData, err, last)
	}

	var got []string
	var lerr error
	if err := waitFor(dnsWait, 3*time.Second, func() (bool, error) {
		got, lerr = hostLookup("www."+f.zone, false)
		return lerr == nil && slices.Contains(got, f.a.IPv4), nil
	}); err != nil {
		fail("zone: www.%s from the host gave %v (%v), want %s: %v", f.zone, got, lerr, f.a.IPv4, err)
	}
	return msgs
}

// run stops the cloud with behaviour and starts it again, recording what
// the rows check.
func (c *lifeCycle) run(f *lifeFixture, behaviour string) error {
	e := f.e
	c.before, c.after, c.status = map[int64]boot{}, map[int64]boot{}, map[int64]string{}
	for _, m := range []*member{f.a, f.b} {
		b, err := readBoot(e, m.server)
		if err != nil {
			return err
		}
		c.before[m.ID] = b
	}
	recorded, err := readRestoreFile()
	if err != nil {
		return err
	}
	if err := setStopBehaviour(behaviour); err != nil {
		return err
	}

	if r, err := runHlab(context.Background(), 10*time.Minute, "cloud", "stop"); err != nil || r.ExitCode != 0 {
		return fmt.Errorf("hlab cloud stop: %v\n%s", err, r)
	}
	if err := waitCloudState("stopped", 15*time.Minute); err != nil {
		return err
	}
	c.stopped = observeStopped(recorded)

	if r, err := runHlab(context.Background(), 10*time.Minute, "cloud", "start"); err != nil || r.ExitCode != 0 {
		return fmt.Errorf("hlab cloud start: %v\n%s", err, r)
	}
	if err := waitCloudState("running", 15*time.Minute); err != nil {
		return err
	}
	c.started = observeStarted(f.zone)

	for _, m := range []*member{f.a, f.b} {
		if err := e.waitReady(m.server); err != nil {
			return err
		}
		b, err := readBoot(e, m.server)
		if err != nil {
			return err
		}
		c.after[m.ID] = b
		// The status watch follows the VM within its interval.
		_ = e.waitStatus(m.ID, "running", 2*time.Minute)
	}
	for _, s := range []*server{f.a.server, f.b.server, f.c} {
		st, err := e.serverStatus(s.ID)
		if err != nil {
			return err
		}
		c.status[s.ID] = st
	}
	if err := powershellJSON(context.Background(),
		fmt.Sprintf(`ConvertTo-Json -InputObject ([string](Get-VM -Name '%s').State)`, vmName(f.c.ID)), &c.cState); err != nil {
		return err
	}
	c.fixture = f.check()
	return nil
}

// readBoot reads the guest's boot_id and uptime.
func readBoot(e *env, s *server) (boot, error) {
	r, err := e.ssh(s, 30*time.Second, "", "cat /proc/sys/kernel/random/boot_id; cut -d' ' -f1 /proc/uptime")
	fields := strings.Fields(r.Stdout)
	if err != nil || r.ExitCode != 0 || len(fields) != 2 {
		return boot{}, fmt.Errorf("boot_id and uptime of %s: %v\n%s", s.Name, err, r)
	}
	up, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return boot{}, fmt.Errorf("uptime of %s: %w", s.Name, err)
	}
	return boot{id: fields[0], uptime: up}, nil
}

// forwardingEntry is an adapter's IPv4 forwarding as hostnet-restore.json
// records it and Get-NetIPInterface reports it.
type forwardingEntry struct {
	Alias      string `json:"alias"`
	Forwarding string `json:"forwarding"`
}

// readRestoreFile reads the forwarding values the running cloud recorded
// before changing them.
func readRestoreFile() ([]forwardingEntry, error) {
	path := filepath.Join(os.Getenv("ProgramData"), "hlab-dev", "hostnet-restore.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s while the cloud runs: %w", path, err)
	}
	var st struct {
		Forwarding []forwardingEntry `json:"forwarding"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return st.Forwarding, nil
}

// publicRoutes lists the next hops of routes of the public range via the
// hlab adapter.
func publicRoutes() ([]string, error) {
	var hops []string
	err := powershellJSON(context.Background(), `ConvertTo-Json -Compress -InputObject @(Get-NetRoute -AddressFamily IPv4 -ErrorAction SilentlyContinue |`+
		` Where-Object { $_.InterfaceAlias -eq '`+hostAdapter+`' -and $_.DestinationPrefix -eq '`+publicRange+`' } |`+
		` ForEach-Object { [string]$_.NextHop })`, &hops)
	return hops, err
}

func observeStopped(recorded []forwardingEntry) []string {
	var msgs []string
	fail := func(format string, a ...any) { msgs = append(msgs, fmt.Sprintf(format, a...)) }

	if hops, err := publicRoutes(); err != nil {
		fail("read routes: %v", err)
	} else if len(hops) != 0 {
		fail("routes of %s via %s exist: next hops %v", publicRange, hostAdapter, hops)
	}
	if rules, err := taggedNRPTRules(); err != nil {
		fail("read NRPT rules: %v", err)
	} else if len(rules) != 0 {
		fail("%d NRPT rules tagged %s exist: %+v", len(rules), nrptTag, rules)
	}

	if !slices.ContainsFunc(recorded, func(r forwardingEntry) bool { return r.Alias == hostAdapter }) {
		fail("hostnet-restore.json of the running cloud does not record %s: %+v", hostAdapter, recorded)
	}
	var now []forwardingEntry
	if err := powershellJSON(context.Background(), `ConvertTo-Json -Compress -InputObject @(Get-NetIPInterface -AddressFamily IPv4 -ErrorAction SilentlyContinue |`+
		` ForEach-Object { [pscustomobject]@{ alias = [string]$_.InterfaceAlias; forwarding = [string]$_.Forwarding } })`, &now); err != nil {
		fail("read forwarding: %v", err)
		return msgs
	}
	for _, r := range recorded {
		i := slices.IndexFunc(now, func(n forwardingEntry) bool { return n.Alias == r.Alias })
		if i < 0 {
			continue // the adapter is gone (WSL stopped), nothing to restore
		}
		if now[i].Forwarding != r.Forwarding {
			fail("forwarding on %s is %s, hostnet-restore.json recorded %s", r.Alias, now[i].Forwarding, r.Forwarding)
		}
	}
	return msgs
}

func observeStarted(zone string) []string {
	var msgs []string
	fail := func(format string, a ...any) { msgs = append(msgs, fmt.Sprintf(format, a...)) }

	var hops []string
	if err := waitFor(2*time.Minute, 3*time.Second, func() (bool, error) {
		var err error
		hops, err = publicRoutes()
		return err == nil && slices.Contains(hops, applianceDNS), nil
	}); err != nil {
		fail("no route of %s via %s to %s (next hops %v): %v", publicRange, hostAdapter, applianceDNS, hops, err)
	}

	want := append(slices.Clone(baseNamespaces), "."+zone)
	var rules []nrptRule
	if err := waitFor(2*time.Minute, 3*time.Second, func() (bool, error) {
		var err error
		rules, err = taggedNRPTRules()
		if err != nil {
			return false, nil
		}
		for _, ns := range want {
			if len(rulesFor(rules, ns)) != 1 {
				return false, nil
			}
		}
		return true, nil
	}); err != nil {
		fail("NRPT rules tagged %s do not hold exactly one rule each for %v: %+v: %v", nrptTag, want, rules, err)
	}
	return msgs
}

var (
	stopBehaviourRe = regexp.MustCompile(`(?m)^[ \t]*stop_behaviour[ \t]*=.*$`)
	cloudSectionRe  = regexp.MustCompile(`(?m)^[ \t]*\[cloud\][ \t]*\r?$`)
)

// setStopBehaviour writes stop_behaviour into the dev settings, keeping
// the rest; the first call keeps the file as found for restoreSettings.
func setStopBehaviour(behaviour string) error {
	s := &lifeSettings
	s.mu.Lock()
	defer s.mu.Unlock()
	path := settingsPath()
	cur, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		cur = nil
	case err != nil:
		return err
	}
	if !s.saved {
		s.saved, s.existed, s.orig = true, err == nil, slices.Clone(cur)
	}
	line := `stop_behaviour = "` + behaviour + `"`
	var next string
	switch text := string(cur); {
	case stopBehaviourRe.MatchString(text):
		next = stopBehaviourRe.ReplaceAllLiteralString(text, line)
	case cloudSectionRe.MatchString(text):
		loc := cloudSectionRe.FindStringIndex(text)
		next = text[:loc[1]] + "\n" + line + text[loc[1]:]
	default:
		next = text + "\n[cloud]\n" + line + "\n"
	}
	return os.WriteFile(path, []byte(next), 0o644)
}

// restoreSettings puts the dev settings back as found before the first
// cycle.
func restoreSettings() error {
	s := &lifeSettings
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.saved {
		return nil
	}
	s.saved = false
	if !s.existed {
		return os.Remove(settingsPath())
	}
	return os.WriteFile(settingsPath(), s.orig, 0o644)
}

func settingsPath() string {
	return filepath.Join(os.Getenv("ProgramData"), "hlab-dev", "settings.toml")
}

// releaseLifeFixture deletes the LIFE fixture once, gives back its slots
// and restores the settings.
func releaseLifeFixture(t *testing.T) {
	t.Helper()
	if err := restoreSettings(); err != nil {
		t.Errorf("restore %s: %v", settingsPath(), err)
	}
	f := &lifeFix
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.released || f.e == nil {
		return
	}
	f.released = true
	e := f.e
	if f.lbID != 0 {
		var b actionRef
		status, aerr, err := e.apiDo(context.Background(), http.MethodDelete, fmt.Sprintf("/load_balancers/%d", f.lbID), nil, &b)
		switch {
		case err != nil:
			t.Errorf("DELETE load balancer %d: %v", f.lbID, err)
		case status >= 300 && status != http.StatusNotFound:
			t.Errorf("DELETE load balancer %d: status %d %s", f.lbID, status, aerr.Code)
		case b.Action != nil:
			if err := e.waitAction(b.Action.ID, 5*time.Minute); err != nil {
				t.Errorf("delete load balancer %d: %v", f.lbID, err)
			}
		}
	}
	if f.fipID != 0 {
		e.deleteFloatingIP(t, f.fipID)
	}
	if f.box != nil {
		if err := e.deleteStorageBox(f.box.ID); err != nil {
			t.Errorf("delete storage box %s: %v", f.box.Username, err)
		}
	}
	if f.volID != 0 {
		e.deleteVolume(t, f.volID)
	}
	var wg sync.WaitGroup
	for _, name := range f.names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			deleteIfCreated(t, e, name)
		}()
	}
	wg.Wait()
	if f.fwID != 0 {
		path := fmt.Sprintf("/firewalls/%d", f.fwID)
		if err := waitFor(5*time.Minute, 3*time.Second, func() (bool, error) {
			status, _, err := e.apiDo(context.Background(), http.MethodDelete, path, nil, nil)
			return err == nil && (status == http.StatusNoContent || status == http.StatusNotFound), nil
		}); err != nil {
			t.Errorf("DELETE %s: %v", path, err)
		}
	}
	if f.network != 0 {
		e.deleteNetwork(t, f.network)
	}
	if f.zoneID != 0 {
		if err := e.deleteZone(f.zoneID); err != nil {
			t.Errorf("delete zone %s: %v", f.zone, err)
		}
	}
	serverSlots.release(f.slots)
}
