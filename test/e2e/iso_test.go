//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// srv15ISO names the catalog ISO SRV-15 boots: Ubuntu 22.04 live-server,
// the smallest of the live ISOs.
const srv15ISO = "ubuntu-22.04"

// liveRoot is a root filesystem of a live session.
func liveRoot(out string) bool {
	return strings.Contains(out, "overlay") || strings.Contains(out, "casper")
}

// testSRV15: an attached ISO boots after reset; after detach_iso and reset
// the installed system answers again.
func testSRV15(t *testing.T) {
	e := sharedEnv(t)
	takeServerSlots(t, 1)
	srv := ownServer(t, e, "srv15")
	iso := catalogISO(t, e, srv15ISO)
	before, err := e.bootID(srv)
	if err != nil {
		t.Fatal(err)
	}

	// The first attach downloads the ISO.
	e.hcloudAction(t, 60*time.Minute, "server", "attach-iso", srv.Name, iso)
	e.hcloudAction(t, 5*time.Minute, "server", "reset", srv.Name)

	// Proof by SSH into the live session; if the live session never
	// answers, the installed system must stay silent and Hyper-V must show
	// the ISO in slot 1 as the first boot device.
	var live, installed bool
	var last result
	_ = waitFor(15*time.Minute, 5*time.Second, func() (bool, error) {
		r, err := e.ssh(srv, 30*time.Second, "", "cat /proc/sys/kernel/random/boot_id; findmnt -n -o FSTYPE,SOURCE /")
		last = r
		if err != nil || r.ExitCode != 0 {
			return false, nil
		}
		lines := strings.SplitN(strings.TrimSpace(r.Stdout), "\n", 2)
		if len(lines) < 2 || lines[0] == before {
			return false, nil
		}
		live = liveRoot(lines[1])
		installed = !live
		return true, nil
	})
	switch {
	case installed:
		t.Fatalf("after attach_iso and reset the installed system answers, not the live ISO:\n%s", last)
	case !live:
		t.Logf("live session does not answer SSH; checking the indirect proof")
		vm := vmName(srv.ID)
		var fw struct {
			Kind string
			Slot int
			Path string
		}
		script := fmt.Sprintf(`$d = (Get-VMFirmware -VMName '%s').BootOrder[0].Device; ConvertTo-Json -Compress -InputObject ([pscustomobject]@{ Kind = [string]$d.GetType().Name; Slot = [int]$d.ControllerLocation; Path = [string]$d.Path })`, vm)
		if err := powershellJSON(context.Background(), script, &fw); err != nil {
			t.Fatalf("firmware of %s: %v", vm, err)
		}
		if !strings.Contains(fw.Kind, "DvdDrive") || fw.Slot != 1 || !strings.Contains(strings.ToLower(fw.Path), ".iso") {
			t.Errorf("first boot device of %s %+v, want DVD slot 1 holding the ISO", vm, fw)
		}
	}

	e.hcloudAction(t, 5*time.Minute, "server", "detach-iso", srv.Name)
	e.hcloudAction(t, 5*time.Minute, "server", "reset", srv.Name)
	e.waitNewBoot(t, srv, before, 10*time.Minute)
	r, err := e.ssh(srv, time.Minute, "", "findmnt -n -o FSTYPE,SOURCE /; cat "+userDataMarker)
	if err != nil || liveRoot(r.Stdout) || !strings.Contains(r.Stdout, userDataValue) {
		t.Errorf("after detach_iso and reset: %v\n%s\nwant the installed system with the user_data marker", err, r)
	}
}

// catalogISO is the name of the catalog ISO whose name starts with prefix.
func catalogISO(t *testing.T, e *env, prefix string) string {
	t.Helper()
	var body struct {
		ISOs []struct {
			Name string `json:"name"`
		} `json:"isos"`
	}
	status, aerr, err := e.apiDo(context.Background(), http.MethodGet, "/isos?per_page=50", nil, &body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET /isos: %v (status %d %s)", err, status, aerr.Code)
	}
	for _, iso := range body.ISOs {
		if strings.HasPrefix(iso.Name, prefix) {
			return iso.Name
		}
	}
	t.Fatalf("no catalog ISO named %s*", prefix)
	return ""
}
