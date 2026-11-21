//go:build e2e

package e2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func init() {
	registerRow(clientRow{Req: "CLI-04", Name: "hcloud", Slots: 2, Run: testCLI04Hcloud})
}

// The example environment of IDEA.md "Success criterion", the same for
// every client (CONTEXT.md "CLI-04").
const (
	exampleRange   = "10.60.0.0/16"
	exampleSubnet  = "10.60.1.0/24"
	exampleLBType  = "lb11"
	exampleLBPort  = 2222 // the LB's TCP service to port 22 of both servers
	exampleVolSize = 10
)

// example names the resources of one client's example environment; each
// lives in a project of its own.
type example struct {
	client     string
	network    string
	firewall   string
	servers    [2]string
	lb         string
	volume     string
	floatingIP string
	zone       string
	records    [2]string // the A record of each server in the zone
}

func newExample(client string) example {
	n := func(part string) string { return resName("example", client, part) }
	return example{
		client: client, network: n("net"), firewall: n("ssh"),
		servers: [2]string{n("server-1"), n("server-2")},
		lb:      n("lb"), volume: n("vol"), floatingIP: n("fip"),
		zone: zoneName("example-" + client), records: [2]string{"server-1", "server-2"},
	}
}

// exampleRules is the firewall of the example: SSH in from everywhere.
func exampleRules() []map[string]any {
	return []map[string]any{{
		"direction": "in", "protocol": "tcp", "port": "22", "source_ips": []string{"0.0.0.0/0", "::/0"},
	}}
}

// byName decodes the one item of a list endpoint with the name into out.
func (e *env) byName(t *testing.T, path, key, name string, out any) {
	t.Helper()
	var body map[string]json.RawMessage
	e.getOK(t, path+"?name="+url.QueryEscape(name), &body)
	var items []json.RawMessage
	if err := json.Unmarshal(body[key], &items); err != nil || len(items) != 1 {
		t.Fatalf("GET %s?name=%s: %d items (%v), want 1", path, name, len(items), err)
	}
	if err := json.Unmarshal(items[0], out); err != nil {
		t.Fatalf("GET %s?name=%s: %v", path, name, err)
	}
}

// checkExample checks through the API that the client built the example
// environment, then that both servers answer SSH from the host and from
// WSL2 on their public IPv4 and one of them through the LB's TCP service.
// It returns the server IDs.
func checkExample(t *testing.T, e *env, ex example) []int64 {
	t.Helper()
	var nw networkDetail
	e.byName(t, "/networks", "networks", ex.network, &nw)
	if !slices.Equal(nw.subnetRanges(), []string{exampleSubnet}) {
		t.Errorf("network %s: subnets %v, want %s", ex.network, nw.subnetRanges(), exampleSubnet)
	}
	var srvs [2]*server
	var ids []int64
	for i, name := range ex.servers {
		var s serverDetail
		e.byName(t, "/servers", "servers", name, &s)
		if s.PublicNet.IPv4 == nil {
			t.Fatalf("server %s has no public IPv4", name)
		}
		srvs[i] = &server{ID: s.ID, Name: s.Name, IPv4: s.PublicNet.IPv4.IP}
		ids = append(ids, s.ID)
		if s.Status != "running" || s.ServerType.Name != serverType || s.Image == nil || s.Image.Name != serverImage {
			t.Errorf("server %s: status %s, type %s, image %+v; want running %s from %s", name, s.Status, s.ServerType.Name, s.Image, serverType, serverImage)
		}
		if _, ok := e.serverNetIn(t, s.ID, nw.ID); !ok {
			t.Errorf("server %s is not in network %s", name, ex.network)
		}
	}
	ref := func(i int) string { return fmt.Sprintf("server:%d", srvs[i].ID) }

	var fw firewallDetail
	e.byName(t, "/firewalls", "firewalls", ex.firewall, &fw)
	if got := e.wantFirewall(t, "example firewall", fw.ID, []string{sshRuleIn}, []string{ref(0), ref(1)}); got.Name != ex.firewall {
		t.Errorf("firewall %d is named %q, want %q", fw.ID, got.Name, ex.firewall)
	}

	var lb lbDetail
	e.byName(t, "/load_balancers", "load_balancers", ex.lb, &lb)
	lb = e.wantLB(t, "example load balancer", lb.ID, []string{fmt.Sprintf("tcp:%d>22", exampleLBPort)},
		[]string{ref(0) + ":private", ref(1) + ":private"})
	if !lb.inNetwork(nw.ID) || lb.PublicNet.IPv4.IP == "" {
		t.Errorf("load balancer %s: in network %t, public IPv4 %q; want in %s with a public IPv4", ex.lb, lb.inNetwork(nw.ID), lb.PublicNet.IPv4.IP, ex.network)
	}

	var vol volumeDetail
	e.byName(t, "/volumes", "volumes", ex.volume, &vol)
	e.wantVolume(t, "example volume", vol.ID, ex.volume, exampleVolSize, vol.Labels, srvs[0])
	var fip ipDetail
	e.byName(t, "/floating_ips", "floating_ips", ex.floatingIP, &fip)
	if fip.Server == nil || *fip.Server != srvs[0].ID {
		t.Errorf("floating IP %s on server %v, want %d", ex.floatingIP, fip.Server, srvs[0].ID)
	}
	zone := e.zoneByName(t, ex.zone)
	for i, rec := range ex.records {
		wantValues(t, e, zone, rec, "A", "example zone", srvs[i].IPv4)
	}

	_, w := sharedWSL(t)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i, s := range srvs {
		wg.Add(2)
		go func() {
			defer wg.Done()
			errs[2*i] = waitAnswer("host", s.Name, func() (result, error) { return e.sshAddr(s.IPv4, 22, 30*time.Second, "hostname") }, s.Name)
		}()
		go func() {
			defer wg.Done()
			errs[2*i+1] = waitAnswer("WSL2", s.Name, func() (result, error) { return w.sshAddr(s.IPv4, 22, 30*time.Second, "hostname") }, s.Name)
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Error(err)
	}
	if err := waitAnswer("host", "the load balancer", func() (result, error) {
		return e.sshAddr(lb.PublicNet.IPv4.IP, exampleLBPort, 30*time.Second, "hostname")
	}, srvs[0].Name, srvs[1].Name); err != nil {
		t.Error(err)
	}
	return ids
}

// waitAnswer runs an SSH command until it prints one of want, at most ten
// minutes (a fresh server boots and the LB's health checks pass first).
func waitAnswer(from, to string, run func() (result, error), want ...string) error {
	var last result
	var lastErr error
	err := waitFor(10*time.Minute, 5*time.Second, func() (bool, error) {
		last, lastErr = run()
		return lastErr == nil && slices.Contains(want, strings.TrimSpace(last.Stdout)), nil
	})
	if err != nil {
		return fmt.Errorf("SSH from %s to %s did not answer with one of %v: %v (last: %v)\n%s", from, to, want, err, lastErr, last)
	}
	return nil
}

// testCLI04Hcloud: in its own project, hcloud builds the example
// environment; both servers answer SSH from host and WSL2 on their public
// IPv4 and once through the LB's TCP service; hcloud deletes it, every list
// of the project is empty and no VM of its servers remains.
func testCLI04Hcloud(t *testing.T, _ *rowCtx) {
	e := newProjectEnv(t, "cli04-hcloud")
	ex := newExample("hcloud")

	e.hcloudOK(t, "network", "create", "--name", ex.network, "--ip-range", exampleRange)
	e.hcloudOK(t, "network", "add-subnet", "--type", "cloud", "--network-zone", "eu-central", "--ip-range", exampleSubnet, ex.network)
	e.hcloudOK(t, "firewall", "create", "--name", ex.firewall, "--rules-file", writeRules(t, "example-rules.json", exampleRules()))

	// Both servers at once, as two shells would.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, name := range ex.servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = e.createServerImage(name, serverImage, "--network", ex.network, "--firewall", ex.firewall)
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}

	e.hcloudOK(t, "load-balancer", "create", "--name", ex.lb, "--type", exampleLBType, "--location", serverLocation)
	e.hcloudOK(t, "load-balancer", "attach-to-network", "--network", ex.network, ex.lb)
	e.hcloudOK(t, "load-balancer", "add-service", "--protocol", "tcp", "--listen-port", fmt.Sprint(exampleLBPort), "--destination-port", "22", ex.lb)
	for _, name := range ex.servers {
		e.hcloudOK(t, "load-balancer", "add-target", "--server", name, "--use-private-ip", ex.lb)
	}
	e.hcloudOK(t, "volume", "create", "--name", ex.volume, "--size", fmt.Sprint(exampleVolSize), "--server", ex.servers[0],
		"--automount", "--format", "ext4")
	e.hcloudOK(t, "floating-ip", "create", "--type", "ipv4", "--name", ex.floatingIP, "--server", ex.servers[0])
	e.hcloudOK(t, "zone", "create", "--name", ex.zone)
	for i, name := range ex.servers {
		s, err := e.describeServer(name)
		if err != nil {
			t.Fatal(err)
		}
		e.hcloudOK(t, "zone", "rrset", "create", "--name", ex.records[i], "--type", "A", "--record", s.IPv4, ex.zone)
	}

	ids := checkExample(t, e, ex)

	e.hcloudOK(t, "zone", "delete", ex.zone)
	e.hcloudOK(t, "floating-ip", "unassign", ex.floatingIP)
	e.hcloudOK(t, "floating-ip", "delete", ex.floatingIP)
	e.hcloudOK(t, "volume", "detach", ex.volume)
	e.hcloudOK(t, "volume", "delete", ex.volume)
	e.hcloudOK(t, "load-balancer", "delete", ex.lb)
	e.hcloudOK(t, "server", "delete", ex.servers[0], ex.servers[1])
	e.hcloudOK(t, "firewall", "delete", ex.firewall)
	e.hcloudOK(t, "network", "delete", ex.network)
	assertProjectEmpty(t, e, ids)
}
