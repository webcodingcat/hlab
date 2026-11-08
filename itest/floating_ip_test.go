//go:build live && windows

package itest

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFloatingIPsReachTheirServerAndMove runs the lab with two Debian
// servers and a Floating IPv4 and IPv6 assigned to the first. Both guests
// configure both addresses, as a pair of servers for failover does on
// Hetzner. The Floating IPs must answer SSH and TCP (the guest's port
// 8000) from Windows over IPv4 and IPv6, and SSH from WSL2 over IPv4, and
// lead to the first server. Then the IPv4 moves to the second server while
// an SSH session through it runs: a new connection soon reaches the second
// server; the old one may break, as on Hetzner, where the new server knows
// nothing of it. Last the IPv6 moves too, and the host must be clean after
// the lab's stop.
func TestFloatingIPsReachTheirServerAndMove(t *testing.T) {
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
	api := newCloudAPI(t, l)

	first := createServer(ctx, t, api, "itest-first", "debian-13")
	second := createServer(ctx, t, api, "itest-second", "debian-13")
	for _, cache := range run.caches {
		cache.keep(t, l)
	}
	floating4 := createFloatingIP(ctx, t, api, "ipv4", first.id)
	floating6 := createFloatingIP(ctx, t, api, "ipv6", first.id)
	address4, address6 := floating4.ip, strings.TrimSuffix(floating6.ip, "/64")+"1"
	for _, server := range []guest{first, second} {
		keys.run(t, server.ipv4, "", addFloatingIPs(address4, address6))
	}

	for _, address := range []string{address4, address6} {
		if got := keys.run(t, address, "", "hostname"); got != first.name {
			t.Errorf("SSH to the Floating IP %s reached %q, want %q", address, got, first.name)
		}
		if got := instanceID(ctx, t, address); got != fmt.Sprint(first.id) {
			t.Errorf("TCP to the Floating IP %s reached the server %q, want %d", address, got, first.id)
		}
	}
	keys.fromWSL(t, address4)

	// the move with a session running through the Floating IP
	session := keys.watchSession(t, address4, tickingSession)
	action := assignFloatingIP(t, api, floating4, second.id)
	reached := waitForInstance(ctx, t, address4, second.id)
	waitForAction(ctx, t, api, action)
	t.Logf("a new connection to %s reached the second server %s after the assign was sent",
		address4, reached.Round(10*time.Millisecond))
	if _, lived := session.end(); lived {
		t.Log("the SSH session to the first server lived on through the move")
	} else {
		t.Log("the SSH session to the first server broke with the move, as on Hetzner")
	}
	if got := keys.run(t, address4, "", "hostname"); got != second.name {
		t.Errorf("SSH to %s after the move reached %q, want %q", address4, got, second.name)
	}
	keys.fromWSL(t, address4)

	action = assignFloatingIP(t, api, floating6, second.id)
	reached = waitForInstance(ctx, t, address6, second.id)
	waitForAction(ctx, t, api, action)
	t.Logf("a new connection to %s reached the second server %s after the assign was sent",
		address6, reached.Round(10*time.Millisecond))
	if got := keys.run(t, address6, "", "hostname"); got != second.name {
		t.Errorf("SSH to %s after the move reached %q, want %q", address6, got, second.name)
	}

	for _, server := range []guest{first, second} {
		var deleted struct {
			Action struct{ ID int64 } `json:"action"`
		}
		api.do(t, http.MethodDelete, fmt.Sprintf("/servers/%d", server.id), nil, http.StatusOK, &deleted)
		waitForAction(ctx, t, api, deleted.Action.ID)
	}
	run.close(t, l)
	for _, cache := range run.caches {
		cache.store(t)
	}
	checkHostIsClean(t)
}

// floatingIP is a Floating IP the test created.
type floatingIP struct {
	id int64
	ip string // an IPv6 as its /64
}

// createFloatingIP creates a Floating IP of a type assigned to a server
// and waits for its assign.
func createFloatingIP(ctx context.Context, t *testing.T, api cloudAPI, ipType string, serverID int64) floatingIP {
	t.Helper()
	var created struct {
		FloatingIP struct {
			ID int64  `json:"id"`
			IP string `json:"ip"`
		} `json:"floating_ip"`
		Action struct{ ID int64 } `json:"action"`
	}
	api.do(t, http.MethodPost, "/floating_ips", map[string]any{"type": ipType, "server": serverID}, http.StatusCreated, &created)
	waitForAction(ctx, t, api, created.Action.ID)
	return floatingIP{id: created.FloatingIP.ID, ip: created.FloatingIP.IP}
}

// assignFloatingIP assigns a Floating IP to a server and returns the
// action's ID.
func assignFloatingIP(t *testing.T, api cloudAPI, floating floatingIP, serverID int64) int64 {
	t.Helper()
	var answer struct {
		Action struct{ ID int64 } `json:"action"`
	}
	path := fmt.Sprintf("/floating_ips/%d/actions/assign", floating.id)
	api.do(t, http.MethodPost, path, map[string]any{"server": serverID}, http.StatusCreated, &answer)
	return answer.Action.ID
}

// addFloatingIPs returns the commands that configure a guest's Floating
// IPs as Hetzner's documentation shows: the IPv4 as a /32 and an address
// of the IPv6 /64 on the public NIC.
func addFloatingIPs(address4, address6 string) string {
	return "dev=$(ip -o -4 route show default | awk '{print $5}') && " +
		"ip address add " + address4 + "/32 dev $dev && ip address add " + address6 + "/64 dev $dev"
}

// instanceID returns the instance ID that the guest behind the address
// serves on port 8000, or "" if it does not answer.
func instanceID(ctx context.Context, t *testing.T, address string) string {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	url := "http://" + net.JoinHostPort(address, "8000") + "/instance-id"
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	response, err := client.Do(request)
	if err != nil {
		return ""
	}
	defer func() { _ = response.Body.Close() }()
	data, _ := io.ReadAll(response.Body)
	return strings.TrimSpace(string(data))
}

// waitForInstance waits until a new connection to the address reaches the
// server and returns how long that took; it tries every 100 ms.
func waitForInstance(ctx context.Context, t *testing.T, address string, serverID int64) time.Duration {
	t.Helper()
	started := time.Now()
	deadline := started.Add(time.Minute)
	for instanceID(ctx, t, address) != fmt.Sprint(serverID) {
		if time.Now().After(deadline) {
			t.Fatalf("a connection to %s did not reach the server %d within a minute", address, serverID)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return time.Since(started)
}
