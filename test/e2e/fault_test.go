//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	// faultImage is the image of the fault fixture's server (E38, E39).
	faultImage = "debian-13"
	// faultData is what the fixture writes to its volume.
	faultData = "hlab-e2e-fault-volume"
	// faultIOPS is the limit the disk_slow rows set.
	faultIOPS = 200
)

// faultFixture is the one debian-13 server with one mounted ext4 volume
// and fio that the FAULT rows share, and the one stop and start of the
// cloud the restart rows share.
type faultFixture struct {
	once  sync.Once
	e     *env
	err   error
	name  string
	srv   *server
	volID int64
	slots int

	restartOnce sync.Once
	restartErr  error
	volDrive    vmDrive // the volume's drive on the VM before the restart

	mu       sync.Mutex
	released bool
}

var faultFix faultFixture

// vmDrive is a hard disk drive of a VM as Get-VMHardDiskDrive reports it.
type vmDrive struct {
	Path               string
	ControllerNumber   int
	ControllerLocation int
	MaximumIOPS        int64
}

// faultInfo is a fault as `hlab fault list --output json` prints it.
type faultInfo struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	Target *struct {
		Type string `json:"type"`
		ID   int64  `json:"id"`
	} `json:"target"`
}

// sharedFault returns the fault fixture, created once;
// releaseFaultFixture deletes it after the FAULT rows.
func sharedFault(t *testing.T) *faultFixture {
	t.Helper()
	e := sharedEnv(t)
	f := &faultFix
	f.once.Do(func() {
		f.e = e
		f.slots = serverSlots.acquire(1)
		f.err = f.create()
	})
	if f.err != nil {
		t.Fatalf("fault fixture: %v", f.err)
	}
	return f
}

func (f *faultFixture) mount() string { return fmt.Sprintf("/mnt/HC_Volume_%d", f.volID) }

func (f *faultFixture) dataFile() string { return f.mount() + "/fault-data" }

// create makes the server, attaches a formatted volume with automount,
// writes faultData to it and installs fio (cloud-init done, apt-get update
// first, E38).
func (f *faultFixture) create() error {
	e := f.e
	ctx := context.Background()
	f.name = harness.runID + "-fault"
	srv, err := e.createServerImage(f.name, faultImage)
	if err != nil {
		return err
	}
	f.srv = srv
	if err := e.waitReady(srv); err != nil {
		return err
	}
	volName := harness.runID + "-fault"
	if r, err := e.hcloudRun(ctx, 15*time.Minute, "volume", "create", "--name", volName, "--size", "10",
		"--server", f.name, "--automount", "--format", "ext4"); err != nil {
		return fmt.Errorf("hcloud volume create: %v\n%s", err, r)
	}
	r, err := e.hcloudRun(ctx, 2*time.Minute, "volume", "describe", volName, "-o", "json")
	if err != nil {
		return fmt.Errorf("hcloud volume describe %s: %v\n%s", volName, err, r)
	}
	var v struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil || v.ID == 0 {
		return fmt.Errorf("hcloud volume describe %s: no ID (%v)\n%s", volName, err, r.Stdout)
	}
	f.volID = v.ID
	var last result
	if err := waitFor(2*time.Minute, 2*time.Second, func() (bool, error) {
		r, err := e.ssh(srv, 30*time.Second, "", "mountpoint -q "+f.mount())
		last = r
		return err == nil && r.ExitCode == 0, nil
	}); err != nil {
		return fmt.Errorf("%s not mounted: %v\n%s", f.mount(), err, last)
	}
	if r, err := e.ssh(srv, time.Minute, "", "echo "+faultData+" > "+f.dataFile()+" && sync"); err != nil || r.ExitCode != 0 {
		return fmt.Errorf("write %s: %v\n%s", f.dataFile(), err, r)
	}
	if r, err := e.ssh(srv, 3*time.Minute, "",
		"apt-get update -q && DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends fio"); err != nil || r.ExitCode != 0 {
		return fmt.Errorf("install fio: %v\n%s", err, r)
	}
	return nil
}

// releaseFaultFixture ends the project's faults and deletes the fixture
// once, giving back its slot.
func releaseFaultFixture(t *testing.T) {
	t.Helper()
	f := &faultFix
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.released || f.e == nil {
		return
	}
	f.released = true
	e := f.e
	if r, err := runHlab(context.Background(), 2*time.Minute, "fault", "delete", "--project", e.project, "--all"); err != nil || r.ExitCode != 0 {
		t.Errorf("hlab fault delete --all: %v\n%s", err, r)
	}
	if f.volID != 0 {
		e.deleteVolume(t, f.volID)
	}
	if f.name != "" {
		deleteIfCreated(t, e, f.name)
	}
	serverSlots.release(f.slots)
}

// createFault runs `hlab fault create` in the run's project and returns
// the new fault's ID.
func createFault(e *env, kind string, args ...string) (int64, error) {
	full := append([]string{"fault", "create", kind, "--project", e.project}, args...)
	r, err := runHlab(context.Background(), 2*time.Minute, full...)
	if err != nil || r.ExitCode != 0 {
		return 0, fmt.Errorf("hlab %s: %v\n%s", strings.Join(full, " "), err, r)
	}
	m := regexp.MustCompile(`fault (\d+) created`).FindStringSubmatch(r.Stdout)
	if m == nil {
		return 0, fmt.Errorf("hlab %s printed no fault ID\n%s", strings.Join(full, " "), r)
	}
	return strconv.ParseInt(m[1], 10, 64)
}

func (f *faultFixture) createFault(t *testing.T, kind string, args ...string) int64 {
	t.Helper()
	id, err := createFault(f.e, kind, args...)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *faultFixture) deleteFault(t *testing.T, id int64) {
	t.Helper()
	if r, err := runHlab(context.Background(), 2*time.Minute, "fault", "delete", "--project", f.e.project,
		strconv.FormatInt(id, 10)); err != nil || r.ExitCode != 0 {
		t.Fatalf("hlab fault delete %d: %v\n%s", id, err, r)
	}
}

// listFaults returns the project's active faults.
func listFaults(e *env) ([]faultInfo, error) {
	r, err := runHlab(context.Background(), 2*time.Minute, "fault", "list", "--project", e.project, "--output", "json")
	if err != nil || r.ExitCode != 0 {
		return nil, fmt.Errorf("hlab fault list: %v\n%s", err, r)
	}
	var body struct {
		Faults []faultInfo `json:"faults"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &body); err != nil {
		return nil, fmt.Errorf("hlab fault list --output json: %w\n%s", err, r.Stdout)
	}
	return body.Faults, nil
}

// hostPing reports whether one ping from the host to ip gets an answer.
func hostPing(ip string) bool {
	ping := filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE")
	r, _ := command(context.Background(), 30*time.Second, nil, "", ping, "-n", "1", "-w", "1000", ip)
	return strings.Contains(strings.ToUpper(r.Stdout), "TTL=")
}

// waitHostPing waits until the host's ping to the server gets an answer.
func waitHostPing(t *testing.T, srv *server, timeout time.Duration, when string) {
	t.Helper()
	if err := waitFor(timeout, time.Second, func() (bool, error) { return hostPing(srv.IPv4), nil }); err != nil {
		t.Fatalf("ping %s %s: no answer: %v", srv.IPv4, when, err)
	}
}

// noHostPing checks that three pings from the host to the server all go
// unanswered.
func noHostPing(t *testing.T, srv *server, when string) {
	t.Helper()
	for i := 0; i < 3; i++ {
		if hostPing(srv.IPv4) {
			t.Fatalf("ping %s %s: answered, want no answer", srv.IPv4, when)
		}
	}
}

// fioIOPS measures the random 8 KiB read IOPS of the raw device dev (a
// shell expression) in the guest as in E39.
func (f *faultFixture) fioIOPS(t *testing.T, dev string) float64 {
	t.Helper()
	cmd := "dev=" + dev + "; fio --name=iops --filename=\"$dev\" --readonly --rw=randread --bs=8k --direct=1" +
		" --ioengine=libaio --iodepth=16 --ramp_time=2 --runtime=5 --time_based --output-format=json"
	r, err := f.e.ssh(f.srv, 2*time.Minute, "", cmd)
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("fio on %s: %v\n%s", dev, err, r)
	}
	out := r.Stdout
	if i := strings.Index(out, "{"); i >= 0 {
		out = out[i:]
	}
	var res struct {
		Jobs []struct {
			Read struct {
				IOPS float64 `json:"iops"`
			} `json:"read"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res.Jobs) == 0 {
		t.Fatalf("fio on %s: no result (%v)\n%s", dev, err, r)
	}
	return res.Jobs[0].Read.IOPS
}

// vmDrives reads the hard disk drives of a VM.
func vmDrives(name string) ([]vmDrive, error) {
	var drives []vmDrive
	script := fmt.Sprintf(`ConvertTo-Json -Compress -InputObject @(Get-VMHardDiskDrive -VMName '%s' | ForEach-Object { [pscustomobject]@{ Path = [string]$_.Path; ControllerNumber = [int]$_.ControllerNumber; ControllerLocation = [int]$_.ControllerLocation; MaximumIOPS = [long]$_.MaximumIOPS } })`, name)
	err := powershellJSON(context.Background(), script, &drives)
	return drives, err
}

// testFAULT10: disk_slow with iops 200 limits the volume and the system
// disk to about 200 IOPS; after the fault ends they are far above it.
func testFAULT10(t *testing.T) {
	f := sharedFault(t)
	cases := []struct {
		name string
		dev  string
		args []string
	}{
		{"volume", volumeDevice(f.volID), []string{"--volume", strconv.FormatInt(f.volID, 10)}},
		{"system_disk", `/dev/$(lsblk -n -o PKNAME "$(findmnt -n -o SOURCE /)")`, []string{"--server", strconv.FormatInt(f.srv.ID, 10)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := f.createFault(t, "disk_slow", append(tc.args, "--iops", strconv.Itoa(faultIOPS))...)
			if got := f.fioIOPS(t, tc.dev); got > faultIOPS*1.1 {
				t.Errorf("IOPS of %s with disk_slow iops %d: %.1f, want at most %d", tc.name, faultIOPS, got, faultIOPS*11/10)
			}
			f.deleteFault(t, id)
			if got := f.fioIOPS(t, tc.dev); got <= 1000 {
				t.Errorf("IOPS of %s after disk_slow ended: %.1f, want more than 1000", tc.name, got)
			}
		})
	}
}

// testFAULT09: volume_unavailable removes the disk while the API keeps it
// attached; after the fault ends and a remount the data reads the same.
func testFAULT09(t *testing.T) {
	f := sharedFault(t)
	e := f.e
	dev := volumeDevice(f.volID)
	id := f.createFault(t, "volume_unavailable", "--volume", strconv.FormatInt(f.volID, 10))

	r, err := e.hcloudRun(context.Background(), 2*time.Minute, "volume", "describe", strconv.FormatInt(f.volID, 10), "-o", "json")
	if err != nil {
		t.Fatalf("hcloud volume describe %d: %v\n%s", f.volID, err, r)
	}
	var v struct {
		Server *int64 `json:"server"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
		t.Fatalf("hcloud volume describe %d: %v\n%s", f.volID, err, r.Stdout)
	}
	if v.Server == nil || *v.Server != f.srv.ID {
		t.Errorf("hcloud volume describe with volume_unavailable: server %v, want %d", v.Server, f.srv.ID)
	}

	read := "sync; echo 3 > /proc/sys/vm/drop_caches; timeout 20 cat " + f.dataFile()
	// Exit 255 is ssh's own failure, not the guest's read.
	var last result
	if err := waitFor(time.Minute, 2*time.Second, func() (bool, error) {
		r, _ := e.ssh(f.srv, time.Minute, "", read)
		last = r
		return r.ExitCode > 0 && r.ExitCode != 255, nil
	}); err != nil {
		t.Errorf("reading %s with volume_unavailable still works, want an I/O error or the mount gone: %v\n%s", f.dataFile(), err, last)
	}

	f.deleteFault(t, id)
	e.waitDevice(t, f.srv, dev, true)
	remount := "umount -l " + f.mount() + "; mount " + dev + " " + f.mount() + " && cat " + f.dataFile()
	r, err = e.ssh(f.srv, time.Minute, "", remount)
	if err != nil || strings.TrimSpace(r.Stdout) != faultData {
		t.Errorf("data after volume_unavailable ended and a remount: %v\n%s\nwant %q", err, r, faultData)
	}
}

// testFAULT06: server_freeze keeps the server running in the API but it
// answers no ping; reset ends the fault, and a fault delete resumes the
// same boot.
func testFAULT06(t *testing.T) {
	f := sharedFault(t)
	e := f.e
	srv := f.srv
	srvID := strconv.FormatInt(srv.ID, 10)
	waitHostPing(t, srv, time.Minute, "before the freeze")

	id := f.createFault(t, "server_freeze", "--server", srvID)
	noHostPing(t, srv, "with server_freeze")
	if st, err := e.serverStatus(srv.ID); err != nil || st != "running" {
		t.Errorf("status with server_freeze: %q (%v), want running", st, err)
	}
	e.hcloudAction(t, 10*time.Minute, "server", "reset", srvID)
	list, err := listFaults(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, fl := range list {
		if fl.ID == id {
			t.Errorf("fault %d still listed after hcloud server reset", id)
		}
	}
	waitHostPing(t, srv, 3*time.Minute, "after the reset")

	e.waitSSH(t, srv, 3*time.Minute)
	before, err := e.bootID(srv)
	if err != nil {
		t.Fatal(err)
	}
	id = f.createFault(t, "server_freeze", "--server", srvID)
	noHostPing(t, srv, "with the second server_freeze")
	f.deleteFault(t, id)
	waitHostPing(t, srv, time.Minute, "after the second freeze ended")
	after, err := e.bootID(srv)
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("boot_id after the freeze ended: %s, want the same boot %s", after, before)
	}
}

// testFAULT05: server_crash turns the server off; poweron fails while it
// is active; after it ends the server stays off until a poweron.
func testFAULT05(t *testing.T) {
	f := sharedFault(t)
	e := f.e
	srv := f.srv
	srvID := strconv.FormatInt(srv.ID, 10)

	id := f.createFault(t, "server_crash", "--server", srvID)
	if err := e.waitStatus(srv.ID, "off", 30*time.Second); err != nil {
		t.Errorf("with server_crash: %v", err)
	}
	r, err := e.hcloudRun(context.Background(), 5*time.Minute, "server", "poweron", srvID)
	if err == nil || !strings.Contains(r.Stdout+r.Stderr, "server_error") {
		t.Errorf("hcloud server poweron with server_crash: %v\n%s\nwant a failure with server_error", err, r)
	}
	f.deleteFault(t, id)
	if st, err := e.serverStatus(srv.ID); err != nil || st != "off" {
		t.Errorf("status after server_crash ended: %q (%v), want off", st, err)
	}
	e.hcloudAction(t, 10*time.Minute, "server", "poweron", srvID)
	if err := e.waitStatus(srv.ID, "running", 2*time.Minute); err != nil {
		t.Error(err)
	}
	e.waitSSH(t, srv, 5*time.Minute)
}

// runFaultRestart creates a server_freeze, a volume_unavailable and a
// disk_slow on the system disk, then stops and starts the cloud once for
// the three restart rows.
func runFaultRestart(t *testing.T, f *faultFixture) {
	t.Helper()
	f.restartOnce.Do(func() { f.restartErr = f.restart() })
	if f.restartErr != nil {
		t.Fatalf("stop and start with active faults: %v", f.restartErr)
	}
}

func (f *faultFixture) restart() error {
	e := f.e
	if err := e.waitStatus(f.srv.ID, "running", 5*time.Minute); err != nil {
		return err
	}
	if err := e.waitReady(f.srv); err != nil {
		return err
	}
	// The net faults FAULT-07/restart and FAULT-08/restart check, before the
	// freeze stops A from answering.
	if err := f.netFaultsBeforeRestart(); err != nil {
		return err
	}
	drives, err := vmDrives(vmName(f.srv.ID))
	if err != nil {
		return err
	}
	var systemPaths []string
	for _, d := range drives {
		systemPaths = append(systemPaths, d.Path)
	}
	srvID := strconv.FormatInt(f.srv.ID, 10)
	volID := strconv.FormatInt(f.volID, 10)
	if _, err := createFault(e, "volume_unavailable", "--volume", volID); err != nil {
		return err
	}
	// The volume's drive is the one gone from the VM.
	after, err := vmDrives(vmName(f.srv.ID))
	if err != nil {
		return err
	}
	for _, d := range drives {
		gone := true
		for _, a := range after {
			if strings.EqualFold(a.Path, d.Path) {
				gone = false
			}
		}
		if gone {
			f.volDrive = d
		}
	}
	if f.volDrive.Path == "" {
		return fmt.Errorf("no drive left VM %s with volume_unavailable (drives %v)", vmName(f.srv.ID), systemPaths)
	}
	if _, err := createFault(e, "disk_slow", "--server", srvID, "--iops", strconv.Itoa(faultIOPS)); err != nil {
		return err
	}
	if _, err := createFault(e, "server_freeze", "--server", srvID); err != nil {
		return err
	}

	if r, err := runHlab(context.Background(), 10*time.Minute, "cloud", "stop"); err != nil || r.ExitCode != 0 {
		return fmt.Errorf("hlab cloud stop: %v\n%s", err, r)
	}
	if err := waitCloudState("stopped", 15*time.Minute); err != nil {
		return err
	}
	if r, err := runHlab(context.Background(), 10*time.Minute, "cloud", "start"); err != nil || r.ExitCode != 0 {
		return fmt.Errorf("hlab cloud start: %v\n%s", err, r)
	}
	return waitCloudState("running", 15*time.Minute)
}

// testFAULT06Restart: after the stop and start no fault is listed, no
// server VM is Paused and the server answers SSH.
func testFAULT06Restart(t *testing.T) {
	f := sharedFault(t)
	runFaultRestart(t, f)
	e := f.e
	list, err := listFaults(e)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("hlab fault list after stop and start: %+v, want none", list)
	}
	var paused []string
	script := fmt.Sprintf(`ConvertTo-Json -Compress -InputObject @(Get-VM -Name '%s-server-*' | Where-Object { [string]$_.State -eq 'Paused' } | ForEach-Object { [string]$_.Name })`, vmPrefix)
	if err := powershellJSON(context.Background(), script, &paused); err != nil {
		t.Fatal(err)
	}
	if len(paused) != 0 {
		t.Errorf("server VMs Paused after stop and start: %v, want none", paused)
	}
	e.waitSSH(t, f.srv, 5*time.Minute)
}

// testFAULT09Restart: after the stop and start the volume's VHDX is on the
// VM at its LUN and the guest sees the volume.
func testFAULT09Restart(t *testing.T) {
	f := sharedFault(t)
	runFaultRestart(t, f)
	drives, err := vmDrives(vmName(f.srv.ID))
	if err != nil {
		t.Fatal(err)
	}
	var found *vmDrive
	for i := range drives {
		if strings.EqualFold(drives[i].Path, f.volDrive.Path) {
			found = &drives[i]
		}
	}
	want := f.volDrive
	switch {
	case found == nil:
		t.Errorf("volume VHDX %s not on VM %s after stop and start (drives %+v)", want.Path, vmName(f.srv.ID), drives)
	case found.ControllerNumber != want.ControllerNumber || found.ControllerLocation != want.ControllerLocation:
		t.Errorf("volume VHDX %s at controller %d location %d after stop and start, want %d location %d",
			want.Path, found.ControllerNumber, found.ControllerLocation, want.ControllerNumber, want.ControllerLocation)
	}
	f.e.waitSSH(t, f.srv, 5*time.Minute)
	f.e.waitDevice(t, f.srv, volumeDevice(f.volID), true)
}

// testFAULT10Restart: after the stop and start no drive of the server's VM
// has a limit.
func testFAULT10Restart(t *testing.T) {
	f := sharedFault(t)
	runFaultRestart(t, f)
	drives, err := vmDrives(vmName(f.srv.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(drives) == 0 {
		t.Fatal("the server's VM has no drives")
	}
	for _, d := range drives {
		if d.MaximumIOPS != 0 {
			t.Errorf("drive %s: MaximumIOPS %d after stop and start, want 0", d.Path, d.MaximumIOPS)
		}
	}
}
