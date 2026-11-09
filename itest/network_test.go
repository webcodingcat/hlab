//go:build live && windows

package itest

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPrivateNetworksConnectServersAndStayApart runs the lab with three
// servers: a Debian and an Ubuntu server in one project, a Debian server in
// another, and a network 10.0.0.0/16 in each project. The servers join
// their networks while they run, so each VM gets its NIC hot-plugged, and
// the guests configure it by DHCP. The first two reach each other over
// their private addresses through the gateway; the third holds the first
// one's address in its own network and reaches only itself and its
// gateway. A detach takes each NIC out of the running guests again, with
// its address and the way to the other server.
func TestPrivateNetworksConnectServersAndStayApart(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	here := filepath.Dir(executable)
	release := filepath.Join(here, "hlab-appliance.vhdx")
	if _, err := os.Stat(release); err != nil {
		t.Skipf("no appliance to boot: %v (bin/dev appliance)", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Minute)
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
	api := newCloudAPI(t, l, "itest")
	other := newCloudAPI(t, l, "itest-other")

	first := createServer(ctx, t, api, "itest-first", "debian-13")
	second := createServer(ctx, t, api, "itest-second", "ubuntu-26.04")
	third := createServer(ctx, t, other, "itest-third", "debian-13")
	for _, cache := range run.caches {
		cache.keep(t, l)
	}
	network := createNetwork(t, api, "itest-net")
	otherNetwork := createNetwork(t, other, "itest-net")

	started := time.Now()
	attachToNetwork(ctx, t, api, first.id, network, "10.0.1.2")
	keys.run(t, first.ipv4, "", waitForPrivateAddress("10.0.1.2"))
	t.Logf("the first server's guest had its private address %s after the attach was sent",
		time.Since(started).Round(100*time.Millisecond))
	attachToNetwork(ctx, t, api, second.id, network, "10.0.1.3")
	attachToNetwork(ctx, t, other, third.id, otherNetwork, "10.0.1.2")
	keys.run(t, second.ipv4, "", waitForPrivateAddress("10.0.1.3"))
	keys.run(t, third.ipv4, "", waitForPrivateAddress("10.0.1.2"))
	t.Logf("the first ping from the second server to the first reached it %s after the attach was sent",
		pingTime(t, keys, second, "10.0.1.2", started))

	for _, check := range []struct {
		from    guest
		address string
		want    string // the instance ID that answers on port 8000, "" for none
	}{
		{second, "10.0.1.2", fmt.Sprint(first.id)},
		{first, "10.0.1.3", fmt.Sprint(second.id)},
		{third, "10.0.1.2", fmt.Sprint(third.id)}, // its own address, not the first's
		{third, "10.0.1.3", ""},                   // in the other project's network
	} {
		if got := privateInstanceID(t, keys, check.from, check.address); got != check.want {
			t.Errorf("%s reached %s:8000 as %q, want %q", check.from.name, check.address, got, check.want)
		}
	}
	for _, server := range []guest{first, third} {
		if !privatePings(t, keys, server, "10.0.0.1") {
			t.Errorf("%s does not reach its network's gateway 10.0.0.1", server.name)
		}
	}
	if t.Failed() {
		logPrivateNetworks(t, keys, first, second, third)
	}
	for _, server := range []guest{first, second} {
		if mtu := keys.run(t, server.ipv4, "", "ip -o link show | awk '/86:00:/ {print $5}'"); mtu != "1450" {
			t.Errorf("%s's private NIC has the MTU %q, want Hetzner's 1450", server.name, mtu)
		}
	}

	// both images let the NIC go while they run
	detachFromNetwork(ctx, t, api, second.id, network)
	if privatePings(t, keys, second, "10.0.1.2") {
		t.Error("the second server reaches the first after its detach")
	}
	detachFromNetwork(ctx, t, api, first.id, network)
	for _, server := range []guest{first, second} {
		if got := keys.run(t, server.ipv4, "", "ip -o link show | grep -c 86:00: || true"); got != "0" {
			t.Errorf("%s's guest still has %s private NICs after the detach", server.name, got)
		}
		if got := keys.run(t, server.ipv4, "", "ip -o -4 address show | grep -c ' 10.0.1.' || true"); got != "0" {
			t.Errorf("%s's guest still has %s private addresses after the detach", server.name, got)
		}
	}

	for _, server := range []struct {
		api cloudAPI
		guest
	}{{api, first}, {api, second}, {other, third}} {
		var deleted struct {
			Action struct{ ID int64 } `json:"action"`
		}
		server.api.do(t, http.MethodDelete, fmt.Sprintf("/servers/%d", server.id), nil, http.StatusOK, &deleted)
		waitForAction(ctx, t, server.api, deleted.Action.ID)
	}
	api.do(t, http.MethodDelete, fmt.Sprintf("/networks/%d", network), nil, http.StatusNoContent, nil)
	other.do(t, http.MethodDelete, fmt.Sprintf("/networks/%d", otherNetwork), nil, http.StatusNoContent, nil)
	run.close(t, l)
	for _, cache := range run.caches {
		cache.store(t)
	}
	checkHostIsClean(t)
}

// privateNetworkState is a guest's command that shows what a failed check
// in a private network needs: addresses, routes, neighbors, the units
// that run dhcpcd, dhcpcd's and the kernel's messages about the NICs.
const privateNetworkState = "ip address show; ip route show; ip neighbor show; " +
	"systemctl status --no-pager 'hlab-private-net@*' 2>&1; " +
	"journalctl --no-pager -b -o short-precise -t dhcpcd -t systemd -k 2>&1 | grep -i -E 'dhcpcd|eth[1-3]|hlab-private|hv_netvsc|carrier' | tail -n 80"

// logPrivateNetworks logs, after a failed check, each guest's state in
// its private networks and the ports of the servers' and the appliance's
// NICs on Hyper-V.
func logPrivateNetworks(t *testing.T, keys labKey, servers ...guest) {
	t.Helper()
	for _, server := range servers {
		t.Logf("%s (%d):\n%s", server.name, server.id, keys.run(t, server.ipv4, "", privateNetworkState))
	}
	script := `Get-VM -Name 'hlab-*' | ForEach-Object {
    $vm = $_
    Get-VMNetworkAdapter -VM $vm | ForEach-Object {
        $vlan = Get-VMNetworkAdapterVlan -VMNetworkAdapter $_
        '{0} {1} switch={2} connected={3} status={4} mode={5} access={6} trunk={7} native={8}' -f $vm.Name, $_.MacAddress,
            $_.SwitchName, $_.Connected, ($_.Status -join ','), $vlan.OperationMode, $vlan.AccessVlanId,
            $vlan.AllowedVlanIdListString, $vlan.NativeVlanId
    }
}`
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	t.Logf("the NICs on Hyper-V (%v):\n%s", err, output)
}

// createNetwork creates a network 10.0.0.0/16 with the subnet 10.0.1.0/24
// and returns its ID.
func createNetwork(t *testing.T, api cloudAPI, name string) int64 {
	t.Helper()
	var created struct {
		Network struct{ ID int64 } `json:"network"`
	}
	api.do(t, http.MethodPost, "/networks", map[string]any{
		"name": name, "ip_range": "10.0.0.0/16",
		"subnets": []map[string]any{{"type": "cloud", "network_zone": "eu-central", "ip_range": "10.0.1.0/24"}},
	}, http.StatusCreated, &created)
	return created.Network.ID
}

// attachToNetwork attaches a server to a network with an address and waits
// for the action.
func attachToNetwork(ctx context.Context, t *testing.T, api cloudAPI, serverID, networkID int64, ip string) {
	t.Helper()
	var answer struct {
		Action struct{ ID int64 } `json:"action"`
	}
	path := fmt.Sprintf("/servers/%d/actions/attach_to_network", serverID)
	api.do(t, http.MethodPost, path, map[string]any{"network": networkID, "ip": ip}, http.StatusCreated, &answer)
	waitForAction(ctx, t, api, answer.Action.ID)
}

// detachFromNetwork detaches a server from a network and waits for the
// action.
func detachFromNetwork(ctx context.Context, t *testing.T, api cloudAPI, serverID, networkID int64) {
	t.Helper()
	var answer struct {
		Action struct{ ID int64 } `json:"action"`
	}
	path := fmt.Sprintf("/servers/%d/actions/detach_from_network", serverID)
	api.do(t, http.MethodPost, path, map[string]any{"network": networkID}, http.StatusCreated, &answer)
	waitForAction(ctx, t, api, answer.Action.ID)
}

// waitForPrivateAddress returns a guest's command that waits up to a minute
// until a NIC holds the address as a /32, as the lab's DHCP gives it, and
// fails otherwise.
func waitForPrivateAddress(ip string) string {
	return fmt.Sprintf("for i in $(seq 120); do ip -o -4 address show | grep -q ' %s/32 ' && exit 0; sleep 0.5; done; "+
		"ip address show; exit 1", ip)
}

// pingTime pings an address from a guest until it answers and returns how
// long that took since started.
func pingTime(t *testing.T, keys labKey, from guest, address string, started time.Time) time.Duration {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for !privatePings(t, keys, from, address) {
		if time.Now().After(deadline) {
			t.Fatalf("%s does not reach %s within a minute", from.name, address)
		}
	}
	return time.Since(started).Round(100 * time.Millisecond)
}

// privatePings reports whether one ping from a guest to an address gets an
// answer within a second.
func privatePings(t *testing.T, keys labKey, from guest, address string) bool {
	t.Helper()
	return keys.run(t, from.ipv4, "", "ping -c 1 -W 1 "+address+" >/dev/null && echo yes || echo no") == "yes"
}

// privateInstanceID returns the instance ID that a guest reaches on port
// 8000 of an address, or "" if nothing answers within 3 s.
func privateInstanceID(t *testing.T, keys labKey, from guest, address string) string {
	t.Helper()
	command := `python3 -c "import sys, urllib.request as u; ` +
		`sys.stdout.write(u.urlopen('http://` + address + `:8000/instance-id', timeout=3).read().decode())" 2>/dev/null || true`
	return strings.TrimSpace(keys.run(t, from.ipv4, "", command))
}
