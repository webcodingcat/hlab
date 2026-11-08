//go:build live && windows

package itest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFirewallsFilterServersFloatingIPsAndTheHost runs the lab with two
// Debian servers, a client and a target with a Floating IPv4 and a second
// TCP port, 8001, beside its 8000. Before any firewall, a connection to an
// address that nobody holds gets no answer, as on Hetzner. A firewall on
// the target lets in SSH and 8000 only: from Windows (over IPv4, IPv6 and
// the Floating IP) and from the client, 8000 answers and 8001 does not.
// set_rules opens 8001, and the test measures how soon a new connection
// from Windows gets through. Then a firewall on the client allows only
// TCP 8000 to the target's Primary IPv4 out: 8001 of the target, its
// Floating IP and the Internet are closed to the client, while DNS, a lab
// endpoint, still answers. Last the host must be clean after the lab's
// stop.
func TestFirewallsFilterServersFloatingIPsAndTheHost(t *testing.T) {
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

	client := createServer(ctx, t, api, "itest-client", "debian-13")
	target := createServer(ctx, t, api, "itest-target", "debian-13")
	for _, cache := range run.caches {
		cache.keep(t, l)
	}
	floating := createFloatingIP(ctx, t, api, "ipv4", target.id)
	floating6 := createFloatingIP(ctx, t, api, "ipv6", target.id)
	keys.run(t, target.ipv4, "", addFloatingIPs(floating.ip, strings.TrimSuffix(floating6.ip, "/64")+"1"))
	keys.run(t, target.ipv4, "", "cd /tmp && (setsid nohup python3 -m http.server 8001 --bind :: > /dev/null 2>&1 &)")
	waitForPort(ctx, t, target.ipv4, 8001)

	// nobody holds these addresses: no answer, neither from the host nor
	// from a guest (measured on Hetzner 2026-11-08)
	nobody4, nobody6 := "198.19.255.250", "2001:db8:1:ffff::1"
	for _, address := range []string{nobody4, nobody6} {
		if got := dialOutcome(ctx, address, 80); got != "timeout" {
			t.Errorf("from Windows, %s:80 nobody holds is %q, want a timeout", address, got)
		}
	}
	checkProbes(t, keys, client.ipv4, map[string]string{nobody4 + ":80": "timeout", nobody6 + ":80": "timeout"})

	// the target's firewall: SSH and 8000 in, nothing else
	targetWall := createFirewall(ctx, t, api, "itest-target", []map[string]any{inRule("22"), inRule("8000")}, target.id)
	for _, address := range []string{target.ipv4, target.ipv6, floating.ip} {
		for port, want := range map[int]string{22: "open", 8000: "open", 8001: "timeout"} {
			if got := dialOutcome(ctx, address, port); got != want {
				t.Errorf("from Windows through the target's firewall, %s:%d is %q, want %q", address, port, got, want)
			}
		}
	}
	keys.fromWSL(t, target.ipv4)
	checkProbes(t, keys, client.ipv4, map[string]string{
		target.ipv4 + ":8000": "open", target.ipv4 + ":8001": "timeout",
		floating.ip + ":8000": "open", floating.ip + ":8001": "timeout",
	})

	// set_rules opens 8001: how soon does a new connection get through?
	opened := time.Now()
	setFirewallRules(t, api, targetWall, []map[string]any{inRule("22"), inRule("8000"), inRule("8001")})
	waitForPort(ctx, t, target.ipv4, 8001)
	t.Logf("a new connection to 8001 got through %s after set_rules was sent", time.Since(opened).Round(10*time.Millisecond))

	// the client's firewall: out only TCP 8000 to the target's Primary IPv4
	createFirewall(ctx, t, api, "itest-client", []map[string]any{inRule("22"), {
		"direction": "out", "protocol": "tcp", "port": "8000", "destination_ips": []string{target.ipv4 + "/32"},
	}}, client.id)
	checkProbes(t, keys, client.ipv4, map[string]string{
		target.ipv4 + ":8000": "open", target.ipv4 + ":8001": "timeout",
		floating.ip + ":8000": "timeout", "deb.debian.org:80": "timeout",
	})
	if got := dialOutcome(ctx, target.ipv4, 8001); got != "open" {
		t.Errorf("from Windows, the target's 8001 is %q after set_rules, want open", got)
	}

	for _, server := range []guest{client, target} {
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

// inRule is a firewall rule that lets TCP to a port in from everywhere.
func inRule(port string) map[string]any {
	return map[string]any{"direction": "in", "protocol": "tcp", "port": port, "source_ips": []string{"0.0.0.0/0", "::/0"}}
}

// createFirewall creates a firewall with rules applied to a server, waits
// for its actions and returns its ID.
func createFirewall(ctx context.Context, t *testing.T, api cloudAPI, name string, rules []map[string]any, serverID int64) int64 {
	t.Helper()
	var created struct {
		Firewall struct{ ID int64 }   `json:"firewall"`
		Actions  []struct{ ID int64 } `json:"actions"`
	}
	api.do(t, http.MethodPost, "/firewalls", map[string]any{
		"name": name, "rules": rules,
		"apply_to": []any{map[string]any{"type": "server", "server": map[string]any{"id": serverID}}},
	}, http.StatusCreated, &created)
	for _, action := range created.Actions {
		waitForAction(ctx, t, api, action.ID)
	}
	return created.Firewall.ID
}

// setFirewallRules replaces a firewall's rules without waiting for its
// actions.
func setFirewallRules(t *testing.T, api cloudAPI, id int64, rules []map[string]any) {
	t.Helper()
	api.do(t, http.MethodPost, fmt.Sprintf("/firewalls/%d/actions/set_rules", id), map[string]any{"rules": rules}, http.StatusCreated, nil)
}

// dialOutcome connects to a TCP port from the host and tells how it went:
// open, refused, timeout (after 2 s), or the error.
func dialOutcome(ctx context.Context, address string, port int) string {
	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(address, fmt.Sprint(port)))
	var netErr net.Error
	switch {
	case err == nil:
		_ = conn.Close()
		return "open"
	case errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	case strings.Contains(err.Error(), "refused"):
		return "refused"
	}
	return err.Error()
}
