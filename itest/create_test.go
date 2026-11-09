//go:build live && windows

package itest

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServersJoinNetworksAndFirewallsAtTheirCreate runs the lab with two
// servers that join a network in their create: the first with public
// addresses under a firewall that lets in only SSH, the second without
// public addresses. The firewall holds from the first boot on: SSH
// answers, the guest's port 8000 does not. The first guest has its private
// address and reads its networks from the metadata service, which leaves
// them out of its summary, as Hetzner's does. The second guest reaches the
// metadata service through the network, as on Hetzner, and serves what it
// read there on port 8000 of its private address, where the first reads
// it.
func TestServersJoinNetworksAndFirewallsAtTheirCreate(t *testing.T) {
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
	network := createNetwork(t, api, "itest-net")
	var firewall struct {
		Firewall struct{ ID int64 } `json:"firewall"`
	}
	api.do(t, http.MethodPost, "/firewalls", map[string]any{"name": "itest-ssh", "rules": []map[string]any{inRule("22")}},
		http.StatusCreated, &firewall)

	public := createJoinedServer(ctx, t, api, "itest-public", true, network, firewall.Firewall.ID)
	waitForSSH(ctx, t, public.ipv4)
	for _, cache := range run.caches {
		cache.keep(t, l)
	}
	if got := dialOutcome(ctx, public.ipv4, 8000); got != "timeout" {
		t.Errorf("port 8000 behind the firewall: %s, want timeout", got)
	}
	keys.run(t, public.ipv4, "", waitForPrivateAddress("10.0.1.1"))
	networks := keys.run(t, public.ipv4, "", readMetadata("/private-networks"))
	for _, want := range []string{"- ip: 10.0.1.1\n", "  interface_num: 1\n", "  network_name: itest-net\n", "  gateway: 10.0.0.1"} {
		if !strings.Contains(networks, want) {
			t.Errorf("the metadata's private-networks lack %q:\n%s", want, networks)
		}
	}
	if summary := keys.run(t, public.ipv4, "", readMetadata("")); strings.Contains(summary, "private-networks") {
		t.Errorf("the metadata's summary has private-networks:\n%s", summary)
	}

	private := createJoinedServer(ctx, t, api, "itest-private", false, network, 0)
	deadline := time.Now().Add(3 * time.Minute)
	for privateInstanceID(t, keys, public, "10.0.1.2") != fmt.Sprint(private.id) {
		if time.Now().After(deadline) {
			logPrivateNetworks(t, keys, public)
			t.Logf("itest-private through itest-public:\n%s", keys.run(t, public.ipv4, string(keys.private),
				"cat > /tmp/lab-key && chmod 600 /tmp/lab-key && echo "+base64.StdEncoding.EncodeToString([]byte(privateGuestState))+
					" | base64 -d | ssh -i /tmp/lab-key -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null "+
					"-o BatchMode=yes -o ConnectTimeout=5 root@10.0.1.2 sh 2>&1"))
			t.Fatal("the server without public addresses does not serve its metadata on 10.0.1.2:8000")
		}
		time.Sleep(2 * time.Second)
	}

	for _, server := range []guest{public, private} {
		var deleted struct {
			Action struct{ ID int64 } `json:"action"`
		}
		api.do(t, http.MethodDelete, fmt.Sprintf("/servers/%d", server.id), nil, http.StatusOK, &deleted)
		waitForAction(ctx, t, api, deleted.Action.ID)
	}
	api.do(t, http.MethodDelete, fmt.Sprintf("/firewalls/%d", firewall.Firewall.ID), nil, http.StatusNoContent, nil)
	api.do(t, http.MethodDelete, fmt.Sprintf("/networks/%d", network), nil, http.StatusNoContent, nil)
	run.close(t, l)
	for _, cache := range run.caches {
		cache.store(t)
	}
	checkHostIsClean(t)
}

// privateGuestState is a guest's script that shows why a server without
// public addresses does not serve its metadata: its network, cloud-init's
// result and errors, and the metadata service's answer from there.
const privateGuestState = `ip address; ip route; cloud-init status --long
grep -B 3 -A 12 -E 'Traceback|Error' /var/log/cloud-init-output.log | tail -n 60
python3 -c 'import urllib.request as u; print(u.urlopen("http://169.254.169.254/hetzner/v1/metadata/instance-id", timeout=3).read())'
journalctl -b --no-pager -o short-precise | grep -E 'dhcpcd|cloud-init|hlab-private' | head -n 40
`

// readMetadata returns a guest's command that prints the metadata
// service's answer for a path below /hetzner/v1/metadata, with Python, as
// Debian's cloud image has it for cloud-init.
func readMetadata(path string) string {
	return `python3 -c "import sys, urllib.request as u; ` +
		`sys.stdout.write(u.urlopen('http://169.254.169.254/hetzner/v1/metadata` + path + `').read().decode())"`
}

// createJoinedServer creates a Debian server with the user data that serves
// its metadata on port 8000, in a network and, if firewall is not 0, under
// a firewall; with public false it has no public addresses. It waits for
// the create's actions.
func createJoinedServer(ctx context.Context, t *testing.T, api cloudAPI, name string, public bool, network, firewall int64) guest {
	t.Helper()
	body := map[string]any{
		"name": name, "server_type": "cpx22", "image": "debian-13", "location": "fsn1", "user_data": checkUserData,
		"networks": []int64{network},
	}
	if firewall != 0 {
		body["firewalls"] = []map[string]int64{{"firewall": firewall}}
	}
	if !public {
		body["public_net"] = map[string]bool{"enable_ipv4": false, "enable_ipv6": false}
	}
	var created struct {
		Server struct {
			ID        int64 `json:"id"`
			PublicNet struct {
				IPv4 *struct{ IP string } `json:"ipv4"`
			} `json:"public_net"`
		} `json:"server"`
		Action      struct{ ID int64 }   `json:"action"`
		NextActions []struct{ ID int64 } `json:"next_actions"`
	}
	api.do(t, http.MethodPost, "/servers", body, http.StatusCreated, &created)
	server := guest{name: name, id: created.Server.ID}
	if created.Server.PublicNet.IPv4 != nil {
		server.ipv4 = created.Server.PublicNet.IPv4.IP
	}
	waitForAction(ctx, t, api, created.Action.ID)
	for _, action := range created.NextActions {
		waitForAction(ctx, t, api, action.ID)
	}
	return server
}
