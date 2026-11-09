//go:build live && windows

package itest

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServerWithIPv6OnlyReachesOnlyTheMetadataService runs the lab with a
// Debian server without a Primary IPv4. As on Hetzner (measured
// 2026-11-09), its public NIC gets an IPv4 /32 of 100.64.0.0/10 by DHCP,
// with routes only to 172.31.1.1, the metadata service and Hetzner's DHCP
// server and no default route; with it the guest reads its metadata and
// cloud-init finishes. The address reaches nothing else, even with a route
// the guest adds itself: neither the host, the lab's resolver nor
// Hetzner's DHCP server.
func TestServerWithIPv6OnlyReachesOnlyTheMetadataService(t *testing.T) {
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
	parts := newParts(t.TempDir())
	t.Cleanup(func() { _ = parts.Close() })
	keys := newLabKey(t)
	run := labRun{
		dataDir: t.TempDir(),
		release: release,
		labKey:  keys.public,
		parts:   parts,
		caches:  []*imageCache{{dir: filepath.Join(here, "image-cache"), name: "debian-13"}},
	}
	l := run.open(ctx, t)
	t.Cleanup(func() {
		run.close(t, l)
		run.removeVMs(t)
	})
	api := newCloudAPI(t, l, "itest")

	var created struct {
		Server struct {
			ID        int64 `json:"id"`
			PublicNet struct {
				IPv6 struct{ IP string } `json:"ipv6"`
			} `json:"public_net"`
		} `json:"server"`
		Action      struct{ ID int64 }   `json:"action"`
		NextActions []struct{ ID int64 } `json:"next_actions"`
	}
	api.do(t, http.MethodPost, "/servers", map[string]any{
		"name": "itest-six", "server_type": "cpx22", "image": "debian-13", "location": "fsn1",
		"user_data": checkUserData, "public_net": map[string]bool{"enable_ipv4": false, "enable_ipv6": true},
	}, http.StatusCreated, &created)
	waitForAction(ctx, t, api, created.Action.ID)
	for _, action := range created.NextActions {
		waitForAction(ctx, t, api, action.ID)
	}
	address := strings.TrimSuffix(created.Server.PublicNet.IPv6.IP, "/64") + "1"
	waitForSSH(ctx, t, address)
	for _, cache := range run.caches {
		cache.keep(t, l)
	}
	checkMetadata(ctx, t, address, map[string]string{
		"instance-id": fmt.Sprint(created.Server.ID), "hostname": "itest-six", "public-ipv4": "",
	})
	if status := keys.run(t, address, "", "cloud-init status --wait"); !strings.Contains(status, "status: done") {
		t.Errorf("cloud-init: %s", status)
	}

	addresses := keys.run(t, address, "", "ip -4 -o address show dev eth0")
	_, inet, _ := strings.Cut(addresses, " inet ")
	serviceAddress, _, _ := strings.Cut(inet, " ")
	if !strings.HasPrefix(serviceAddress, "100.64.") || !strings.HasSuffix(serviceAddress, "/32") {
		t.Fatalf("eth0 has no /32 of 100.64.0.0/10:\n%s", addresses)
	}
	routes := keys.run(t, address, "", "ip -4 route show dev eth0")
	for _, want := range []string{"169.254.169.254 via 172.31.1.1", "172.31.1.1 ", "188.40.24.200 via 172.31.1.1"} {
		if !strings.Contains(routes, want) {
			t.Errorf("eth0's routes lack %q:\n%s", want, routes)
		}
	}
	if strings.Contains(routes, "default") {
		t.Errorf("eth0 has a default route:\n%s", routes)
	}

	source := strings.TrimSuffix(serviceAddress, "/32")
	keys.run(t, address, "", "ip route add 198.18.0.0/15 via 172.31.1.1 dev eth0 src "+source)
	checkProbes(t, keys, address, map[string]string{
		"169.254.169.254:80": "open",
		"198.18.0.1:8080":    "timeout", // the host's API
		"198.18.0.2:53":      "timeout", // the lab's resolver
		"188.40.24.200:80":   "timeout", // Hetzner's DHCP server
	})

	var deleted struct {
		Action struct{ ID int64 } `json:"action"`
	}
	api.do(t, http.MethodDelete, fmt.Sprintf("/servers/%d", created.Server.ID), nil, http.StatusOK, &deleted)
	waitForAction(ctx, t, api, deleted.Action.ID)
	run.close(t, l)
	for _, cache := range run.caches {
		cache.store(t)
	}
	checkHostIsClean(t)
}

