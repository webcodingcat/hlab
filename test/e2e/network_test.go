//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// netFixtureUsers is the number of rows using the shared network fixture
// (NET-02, NET-03, NET-04, HOST-01, FIP-02).
const netFixtureUsers = 5

// member is a server of the network fixture with its private address as
// the API shows it and the guest's interfaces.
type member struct {
	*server
	privIP  string // private IP as the API shows it
	privMAC string // private adapter MAC as the API shows it, lower case
	privIf  string // guest interface with privMAC
	pubIf   string // guest interface with the public IPv4
	pubMAC  string // MAC of pubIf
}

// netFixture is network N1 (10.50.0.0/16) with servers A (attached at
// create) and B (attached while running), and network N2 (10.60.0.0/16)
// with server C, created once for the rows that need them.
type netFixture struct {
	once     sync.Once
	e        *env
	names    []string
	networks []int64
	n1, n2   int64
	gw       string // N1's gateway as the API shows it
	a, b, c  *member
	slot     bool
	err      error

	// direct is held exclusively by HOST-01 while it adds hand-made routes
	// on A; the rows measuring routed paths hold it shared (E1).
	direct sync.RWMutex

	mu       sync.Mutex
	done     int
	released bool
}

var netFix netFixture

// sharedNetwork returns the network fixture, created once; when every row
// using it has ended, its servers and networks are deleted (or at the end
// of TestE2E when a filter runs only some of them).
func sharedNetwork(t *testing.T) *netFixture {
	t.Helper()
	e := sharedEnv(t)
	f := &netFix
	f.once.Do(func() {
		f.e = e
		serverSlots.acquire(1)
		f.slot = true
		f.err = f.create()
	})
	t.Cleanup(func() {
		f.mu.Lock()
		f.done++
		last := f.done == netFixtureUsers
		f.mu.Unlock()
		if last {
			releaseNetFixture(t)
		}
	})
	if f.err != nil {
		t.Fatalf("network fixture: %v", f.err)
	}
	return f
}

func (f *netFixture) create() error {
	e := f.e
	var err error
	if f.n1, err = e.createNetwork(harness.runID+"-n1", "10.50.0.0/16", "10.50.1.0/24", &f.gw); err != nil {
		return err
	}
	f.networks = append(f.networks, f.n1)
	if f.n2, err = e.createNetwork(harness.runID+"-n2", "10.60.0.0/16", "10.60.1.0/24", nil); err != nil {
		return err
	}
	f.networks = append(f.networks, f.n2)

	specs := []struct {
		suffix  string
		network int64 // attached at create; 0: none
	}{
		{"net-a", f.n1},
		{"net-b", 0},
		{"net-c", f.n2},
	}
	srvs := make([]*server, len(specs))
	errs := make([]error, len(specs))
	var wg sync.WaitGroup
	for i, s := range specs {
		name := harness.runID + "-" + s.suffix
		f.names = append(f.names, name)
		wg.Add(1)
		go func() {
			defer wg.Done()
			var extra []string
			if s.network != 0 {
				extra = []string{"--network", strconv.FormatInt(s.network, 10)}
			}
			srvs[i], errs[i] = e.createServerImage(name, serverImage, extra...)
			if errs[i] == nil {
				errs[i] = e.waitReady(srvs[i])
			}
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}

	// B is attached while it runs.
	if err := e.postAction(fmt.Sprintf("/servers/%d/actions/attach_to_network", srvs[1].ID),
		map[string]any{"network": f.n1}, 10*time.Minute); err != nil {
		return fmt.Errorf("attach B to N1: %w", err)
	}

	for i, net := range []int64{f.n1, f.n1, f.n2} {
		m, err := e.loadMember(srvs[i], net)
		if err != nil {
			return err
		}
		switch i {
		case 0:
			f.a = m
		case 1:
			f.b = m
		case 2:
			f.c = m
		}
	}
	return nil
}

// releaseNetFixture deletes the fixture's servers and networks once and
// gives back the server slot.
func releaseNetFixture(t *testing.T) {
	t.Helper()
	f := &netFix
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.released || f.e == nil {
		return
	}
	f.released = true
	var wg sync.WaitGroup
	for _, name := range f.names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			deleteIfCreated(t, f.e, name)
		}()
	}
	wg.Wait()
	for _, id := range f.networks {
		f.e.deleteNetwork(t, id)
	}
	if f.slot {
		serverSlots.release(1)
	}
}

// createNetwork creates a network with one eu-central subnet; gw, if not
// nil, gets the subnet's gateway.
func (e *env) createNetwork(name, ipRange, subnet string, gw *string) (int64, error) {
	var body struct {
		Network struct {
			ID      int64 `json:"id"`
			Subnets []struct {
				Gateway string `json:"gateway"`
			} `json:"subnets"`
		} `json:"network"`
	}
	status, aerr, err := e.apiDo(context.Background(), http.MethodPost, "/networks", map[string]any{
		"name": name, "ip_range": ipRange,
		"subnets": []map[string]any{{"type": "cloud", "ip_range": subnet, "network_zone": "eu-central"}},
	}, &body)
	if err != nil {
		return 0, err
	}
	if status != http.StatusCreated {
		return 0, fmt.Errorf("POST /networks %s: status %d %s: %s", name, status, aerr.Code, aerr.Message)
	}
	if gw != nil {
		if len(body.Network.Subnets) != 1 || body.Network.Subnets[0].Gateway == "" {
			return 0, fmt.Errorf("POST /networks %s: no subnet gateway in the answer", name)
		}
		*gw = body.Network.Subnets[0].Gateway
	}
	return body.Network.ID, nil
}

// deleteNetwork deletes the network, trying again while an action of it runs.
func (e *env) deleteNetwork(t *testing.T, id int64) {
	t.Helper()
	path := fmt.Sprintf("/networks/%d", id)
	var last int
	err := waitFor(5*time.Minute, 5*time.Second, func() (bool, error) {
		status, _, err := e.apiDo(context.Background(), http.MethodDelete, path, nil, nil)
		last = status
		return err == nil && (status == http.StatusNoContent || status == http.StatusOK || status == http.StatusNotFound), nil
	})
	if err != nil {
		t.Errorf("DELETE %s (last status %d): %v", path, last, err)
	}
}

// postAction posts an action, trying again while the API answers that a
// resource is locked by another action, and waits for its success.
func (e *env) postAction(path string, body any, timeout time.Duration) error {
	return e.postActionInto(path, body, nil, timeout)
}

// postActionInto posts an action, trying again while the API answers
// locked or conflict, decodes the 201 body into out as well (when out is
// not nil) and waits for the action's success.
func (e *env) postActionInto(path string, body, out any, timeout time.Duration) error {
	var raw json.RawMessage
	var b struct {
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	var last string
	err := waitFor(5*time.Minute, 3*time.Second, func() (bool, error) {
		status, aerr, err := e.apiDo(context.Background(), http.MethodPost, path, body, &raw)
		if err != nil {
			return false, err
		}
		if status == http.StatusCreated {
			return true, nil
		}
		last = fmt.Sprintf("status %d %s: %s", status, aerr.Code, aerr.Message)
		if aerr.Code == "locked" || aerr.Code == "conflict" {
			return false, nil
		}
		return false, errors.New(last)
	})
	if err != nil {
		return fmt.Errorf("POST %s: %w (%s)", path, err, last)
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("POST %s: %w", path, err)
		}
	}
	return e.waitAction(b.Action.ID, timeout)
}

// runLockedAction is postAction failing the row.
func (e *env) runLockedAction(t *testing.T, path string, body any) {
	t.Helper()
	if err := e.postAction(path, body, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
}

type privateNet struct {
	Network    int64  `json:"network"`
	IP         string `json:"ip"`
	MACAddress string `json:"mac_address"`
}

// privateNet is the server's private_net entry for the network.
func (e *env) privateNet(id, network int64) (privateNet, error) {
	var body struct {
		Server struct {
			PrivateNet []privateNet `json:"private_net"`
		} `json:"server"`
	}
	status, aerr, err := e.apiDo(context.Background(), http.MethodGet, fmt.Sprintf("/servers/%d", id), nil, &body)
	if err != nil {
		return privateNet{}, err
	}
	if status != http.StatusOK {
		return privateNet{}, fmt.Errorf("GET /servers/%d: status %d %s", id, status, aerr.Code)
	}
	for _, p := range body.Server.PrivateNet {
		if p.Network == network {
			if p.IP == "" || p.MACAddress == "" {
				return p, fmt.Errorf("server %d: private_net of network %d without ip or mac_address: %+v", id, network, p)
			}
			return p, nil
		}
	}
	return privateNet{}, fmt.Errorf("server %d: no private_net entry for network %d", id, network)
}

// loadMember reads the server's private address from the API and waits
// until the guest has it on the adapter with the API's MAC.
func (e *env) loadMember(s *server, network int64) (*member, error) {
	pn, err := e.privateNet(s.ID, network)
	if err != nil {
		return nil, err
	}
	m := &member{server: s, privIP: pn.IP, privMAC: strings.ToLower(pn.MACAddress)}
	script := `for d in /sys/class/net/*; do if [ "$(cat "$d/address" 2>/dev/null)" = "` + m.privMAC +
		`" ] && ip -o -4 addr show dev "${d##*/}" | grep -qF " inet ` + m.privIP + `/32 "; then echo "${d##*/}"; fi; done`
	var last result
	if err := waitFor(5*time.Minute, 5*time.Second, func() (bool, error) {
		r, err := e.ssh(s, 30*time.Second, "", script)
		last = r
		m.privIf = strings.TrimSpace(r.Stdout)
		return err == nil && m.privIf != "", nil
	}); err != nil {
		return nil, fmt.Errorf("%s: no adapter with MAC %s and %s/32 in the guest: %v\n%s", s.Name, m.privMAC, m.privIP, err, last)
	}
	pub := `d=$(ip -o -4 addr show | awk '{split($4,a,"/"); if (a[1]=="` + s.IPv4 + `") {print $2; exit}}'); ` +
		`[ -n "$d" ] && echo "$d $(cat /sys/class/net/$d/address)"`
	r, err := e.ssh(s, 30*time.Second, "", pub)
	fields := strings.Fields(r.Stdout)
	if err != nil || len(fields) != 2 {
		return nil, fmt.Errorf("%s: public interface of %s: %v\n%s", s.Name, s.IPv4, err, r)
	}
	m.pubIf, m.pubMAC = fields[0], strings.ToLower(fields[1])
	return m, nil
}

// guest runs a script as root on the server and fails the row if it fails.
func (e *env) guest(t *testing.T, s *server, script string) string {
	t.Helper()
	r, err := e.ssh(s, 2*time.Minute, "", script)
	if err != nil {
		t.Fatalf("%s: %s: %v\n%s", s.Name, script, err, r)
	}
	return r.Stdout
}

// waitPing waits until from answers a ping to ip.
func (e *env) waitPing(t *testing.T, from *server, ip string) {
	t.Helper()
	var last result
	if err := waitFor(3*time.Minute, 3*time.Second, func() (bool, error) {
		r, err := e.ssh(from, 30*time.Second, "", "ping -c 1 -W 2 "+ip)
		last = r
		return err == nil && r.ExitCode == 0, nil
	}); err != nil {
		t.Errorf("%s cannot ping %s: %v\n%s", from.Name, ip, err, last)
	}
}

// noPing checks that five pings from the server to ip all go unanswered.
func (e *env) noPing(t *testing.T, from *server, ip string) {
	t.Helper()
	r, _ := e.ssh(from, time.Minute, "", "ping -c 5 -W 2 "+ip)
	switch r.ExitCode {
	case 0:
		t.Errorf("%s reaches %s, want unreachable\n%s", from.Name, ip, r)
	case 1, 2: // no reply, or no route
	default:
		t.Errorf("ping %s on %s: %s", ip, from.Name, r)
	}
}

// testNET02: A and B (B attached while running) have their private IPs as
// the API shows them and ping each other on them; C in N2 cannot reach A.
func testNET02(t *testing.T) {
	f := sharedNetwork(t)
	f.direct.RLock()
	defer f.direct.RUnlock()
	e := f.e

	for _, m := range []*member{f.a, f.b, f.c} {
		out := e.guest(t, m.server, "ip -o -4 addr show dev "+m.privIf)
		if !strings.Contains(out, " inet "+m.privIP+"/32 ") {
			t.Errorf("%s: interface %s (MAC %s) holds %q, want %s/32 as the API shows", m.Name, m.privIf, m.privMAC, out, m.privIP)
		}
	}
	e.waitPing(t, f.a.server, f.b.privIP)
	e.waitPing(t, f.b.server, f.a.privIP)
	e.noPing(t, f.c.server, f.a.privIP)
}

// testNET03: an alias IP set through the API and configured by hand on B
// answers pings from A.
func testNET03(t *testing.T) {
	f := sharedNetwork(t)
	f.direct.RLock()
	defer f.direct.RUnlock()
	e := f.e
	const alias = "10.50.1.200"

	path := fmt.Sprintf("/servers/%d/actions/change_alias_ips", f.b.ID)
	e.runLockedAction(t, path, map[string]any{"network": f.n1, "alias_ips": []string{alias}})
	t.Cleanup(func() {
		_, _ = e.ssh(f.b.server, time.Minute, "", "ip addr del "+alias+"/32 dev "+f.b.privIf)
		if err := e.postAction(path, map[string]any{"network": f.n1, "alias_ips": []string{}}, 10*time.Minute); err != nil {
			t.Errorf("remove alias IP: %v", err)
		}
	})
	e.guest(t, f.b.server, "ip addr replace "+alias+"/32 dev "+f.b.privIf)
	e.waitPing(t, f.a.server, alias)
}

// testNET04: with a route 10.99.0.0/24 via B's private IP and 10.99.0.5/32
// on B's loopback, A reaches 10.99.0.5 and the answer comes from B.
func testNET04(t *testing.T) {
	f := sharedNetwork(t)
	f.direct.RLock()
	defer f.direct.RUnlock()
	e := f.e
	const (
		dest   = "10.99.0.0/24"
		target = "10.99.0.5"
		unit   = "hlab-net04"
		dir    = "/run/hlab-net04"
	)

	routes := fmt.Sprintf("/networks/%d/actions/", f.n1)
	route := map[string]any{"destination": dest, "gateway": f.b.privIP}
	e.runLockedAction(t, routes+"add_route", route)
	t.Cleanup(func() {
		if err := e.postAction(routes+"delete_route", route, 10*time.Minute); err != nil {
			t.Errorf("delete route: %v", err)
		}
	})

	// B owns the destination address and serves its hostname on it.
	t.Cleanup(func() {
		_, _ = e.ssh(f.b.server, time.Minute, "", "systemctl stop "+unit+"; ip addr del "+target+"/32 dev lo")
	})
	e.guest(t, f.b.server, "ip addr replace "+target+"/32 dev lo && mkdir -p "+dir+" && hostname > "+dir+"/hostname && "+
		"systemd-run --unit="+unit+" --collect python3 -m http.server 8004 --bind "+target+" --directory "+dir)

	// Routes outside the range are not given to guests (as at Hetzner);
	// A routes the destination via the network gateway itself.
	t.Cleanup(func() {
		_, _ = e.ssh(f.a.server, time.Minute, "", "ip route del "+dest)
	})
	e.guest(t, f.a.server, "ip route replace "+dest+" via "+f.gw+" dev "+f.a.privIf+" onlink")

	var last result
	if err := waitFor(3*time.Minute, 3*time.Second, func() (bool, error) {
		r, err := e.ssh(f.a.server, 30*time.Second, "", "curl -s --max-time 5 http://"+target+":8004/hostname")
		last = r
		return err == nil && strings.TrimSpace(r.Stdout) == f.b.Name, nil
	}); err != nil {
		t.Errorf("A did not get B's hostname %q from %s: %v\n%s", f.b.Name, target, err, last)
	}
}

// testHOST01: A reaches B on its public and private IP through the
// appliance; with hand-made on-link /32 routes to B on both interfaces A
// never learns B's MAC and B sees no frame from A's MAC addressed to it.
func testHOST01(t *testing.T) {
	f := sharedNetwork(t)
	f.direct.Lock()
	defer f.direct.Unlock()
	e := f.e
	a, b := f.a, f.b

	e.guest(t, b.server, "command -v tcpdump")
	srcA := "(ether src " + a.pubMAC + " or ether src " + a.privMAC + ")"
	captures := []struct{ unit, dev, dstMAC string }{
		{"hlab-host01-pub", b.pubIf, b.pubMAC},
		{"hlab-host01-priv", b.privIf, b.privMAC},
	}
	for _, c := range captures {
		t.Cleanup(func() { _, _ = e.ssh(b.server, time.Minute, "", "systemctl stop "+c.unit) })
		e.guest(t, b.server, "systemd-run --unit="+c.unit+" --collect sh -c 'exec tcpdump -p -lnei "+c.dev+
			" \""+srcA+" and ether dst "+c.dstMAC+"\" >/run/"+c.unit+".out 2>/run/"+c.unit+".err'")
		var last result
		if err := waitFor(time.Minute, time.Second, func() (bool, error) {
			r, err := e.ssh(b.server, 30*time.Second, "", "grep -q 'listening on' /run/"+c.unit+".err")
			last = r
			return err == nil, nil
		}); err != nil {
			t.Fatalf("capture on %s of B did not start: %v\n%s", c.dev, err, last)
		}
	}

	// Routed first (E1).
	e.waitPing(t, a.server, b.IPv4)
	e.waitPing(t, a.server, b.privIP)

	direct := []struct{ ip, dev, mac string }{
		{b.IPv4, a.pubIf, b.pubMAC},
		{b.privIP, a.privIf, b.privMAC},
	}
	for _, d := range direct {
		t.Cleanup(func() {
			_, _ = e.ssh(a.server, time.Minute, "", "ip route del "+d.ip+"/32 dev "+d.dev+"; ip neigh flush to "+d.ip)
		})
		e.guest(t, a.server, "ip route replace "+d.ip+"/32 dev "+d.dev)
	}
	for round := range 5 {
		for _, d := range direct {
			out := e.guest(t, a.server, "ping -c 2 -W 1 "+d.ip+" >/dev/null; ip neigh show to "+d.ip+" dev "+d.dev)
			if strings.Contains(strings.ToLower(out), d.mac) {
				t.Errorf("round %d: A's neighbour entry for %s on %s holds B's MAC %s: %q", round, d.ip, d.dev, d.mac, out)
			}
		}
	}

	for _, c := range captures {
		out := e.guest(t, b.server, "systemctl stop "+c.unit+"; cat /run/"+c.unit+".out")
		if s := strings.TrimSpace(out); s != "" {
			t.Errorf("B saw frames from A's MAC addressed to it on %s:\n%s", c.dev, s)
		}
	}
}
