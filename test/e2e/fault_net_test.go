//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
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
	// faultDNS and faultMetadata are the appliance's DNS resolver and
	// metadata service a server reaches over its public network.
	faultDNS      = "185.12.64.1"
	faultMetadata = "169.254.169.254"
	// faultOtherLocation is the location of server C.
	faultOtherLocation = "hel1"
	// faultLossPings is the number of pings of a loss measurement.
	faultLossPings = 300
	// faultApplyTimeout is the wait until a fault created or deleted shows
	// in the traffic.
	faultApplyTimeout = 30 * time.Second
	// faultAttemptTimeout limits one storage box attempt during the fault.
	faultAttemptTimeout = 30 * time.Second
	// faultBoxKey is the copy of the run's key on server B for SFTP.
	faultBoxKey = "/root/.ssh/hlab-e2e-faultbox"
	// faultProbe is the script on A that logs DNS, metadata and pings.
	faultProbe = "/root/hlab-e2e-fault-probe.sh"
)

// faultNetFixture adds to the fault fixture's server (A, fsn1) server B
// (fsn1) and server C (hel1), one private network with all three and one
// storage box with SSH, Samba and WebDAV reachable from outside; the FAULT
// rows of network, latency and storage_box_unavailable share it.
type faultNetFixture struct {
	once    sync.Once
	err     error
	e       *env
	slots   int
	names   []string // B and C
	netID   int64
	a, b, c *member
	box     *storageBox
	boxIP   string

	// The baseline of A to C before the shared restart, for FAULT-08/restart.
	restartPub, restartPriv float64

	mu       sync.Mutex
	released bool
}

var faultNetFix faultNetFixture

// sharedFaultNet returns the net fixture, created once next to the fault
// fixture; releaseFaultNetFixture deletes it after the FAULT rows.
func sharedFaultNet(t *testing.T) (*faultFixture, *faultNetFixture) {
	t.Helper()
	f := sharedFault(t)
	n, err := f.net()
	if err != nil {
		t.Fatalf("fault net fixture: %v", err)
	}
	return f, n
}

// net creates the net fixture once; the restart of the fault fixture uses
// it without a test.
func (f *faultFixture) net() (*faultNetFixture, error) {
	n := &faultNetFix
	n.once.Do(func() {
		n.e = f.e
		n.slots = serverSlots.acquire(2)
		n.err = n.create(f)
	})
	return n, n.err
}

func (n *faultNetFixture) create(f *faultFixture) error {
	e := n.e
	var err error
	if n.netID, err = e.createNetwork(harness.runID+"-faultnet", "10.70.0.0/16", "10.70.1.0/24", nil); err != nil {
		return err
	}
	netArg := strconv.FormatInt(n.netID, 10)
	specs := []struct {
		suffix string
		extra  []string
	}{
		{"fault-b", []string{"--network", netArg}},
		{"fault-c", []string{"--network", netArg, "--location", faultOtherLocation}},
	}
	srvs := make([]*server, len(specs))
	errs := make([]error, len(specs)+1)
	var wg sync.WaitGroup
	for i, s := range specs {
		name := harness.runID + "-" + s.suffix
		n.names = append(n.names, name)
		wg.Add(1)
		go func() {
			defer wg.Done()
			srvs[i], errs[i] = e.createServerImage(name, faultImage, s.extra...)
			if errs[i] == nil {
				errs[i] = e.waitReady(srvs[i])
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		errs[len(specs)] = n.createBox()
	}()
	if err := e.postAction(fmt.Sprintf("/servers/%d/actions/attach_to_network", f.srv.ID),
		map[string]any{"network": n.netID}, 10*time.Minute); err != nil {
		wg.Wait()
		return fmt.Errorf("attach A to the fault network: %w", err)
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}

	all := []*server{f.srv, srvs[0], srvs[1]}
	install := "apt-get update -q && DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends" +
		" iputils-ping dnsutils curl smbclient openssh-client"
	for i, s := range all {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r, err := e.ssh(s, 5*time.Minute, "", install); err != nil {
				errs[i] = fmt.Errorf("%s: install the probe tools: %v\n%s", s.Name, err, r)
			}
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}
	members := make([]*member, len(all))
	for i, s := range all {
		if members[i], err = e.loadMember(s, n.netID); err != nil {
			return err
		}
	}
	n.a, n.b, n.c = members[0], members[1], members[2]

	key, err := os.ReadFile(e.key)
	if err != nil {
		return err
	}
	if r, err := e.ssh(n.b.server, time.Minute, string(key),
		"umask 077; mkdir -p /root/.ssh; cat > "+faultBoxKey); err != nil {
		return fmt.Errorf("copy the run's key to %s: %v\n%s", n.b.Name, err, r)
	}
	return nil
}

// createBox creates the storage box with the run's key and resolves its
// address from the host.
func (n *faultNetFixture) createBox() error {
	e := n.e
	key, err := os.ReadFile(e.pubKey)
	if err != nil {
		return err
	}
	box, err := e.createStorageBox(harness.runID+"-faultbox", "bx11", map[string]bool{
		"reachable_externally": true, "ssh_enabled": true, "samba_enabled": true, "webdav_enabled": true,
	}, []string{strings.TrimSpace(string(key))})
	n.box = box
	if err != nil {
		return err
	}
	if err := waitFor(boxReadyTimeout, 3*time.Second, func() (bool, error) {
		_, err := hostBanner(box.Server)
		return err == nil, nil
	}); err != nil {
		return fmt.Errorf("box %s does not answer on port 22 from the host: %w", box.Server, err)
	}
	addrs, err := net.DefaultResolver.LookupHost(context.Background(), box.Server)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", box.Server, err)
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			n.boxIP = a
			return nil
		}
	}
	return fmt.Errorf("%s resolves to no IPv4 address: %v", box.Server, addrs)
}

// releaseFaultNetFixture deletes B, C, the network and the box once and
// gives back the slots; it runs after releaseFaultFixture, which ends the
// faults and deletes A.
func releaseFaultNetFixture(t *testing.T) {
	t.Helper()
	n := &faultNetFix
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.released || n.e == nil {
		return
	}
	n.released = true
	var wg sync.WaitGroup
	for _, name := range n.names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			deleteIfCreated(t, n.e, name)
		}()
	}
	wg.Wait()
	if n.netID != 0 {
		n.e.deleteNetwork(t, n.netID)
	}
	if n.box != nil {
		if err := n.e.deleteStorageBox(n.box.ID); err != nil {
			t.Errorf("deleting the fault storage box: %v", err)
		}
	}
	serverSlots.release(n.slots)
}

// netFault creates a fault for the row and ends it when the row ends, if
// the row did not end it itself.
func (f *faultFixture) netFault(t *testing.T, kind string, args ...string) int64 {
	t.Helper()
	id := f.createFault(t, kind, args...)
	t.Cleanup(func() {
		_, _ = runHlab(context.Background(), 2*time.Minute, "fault", "delete", "--project", f.e.project, strconv.FormatInt(id, 10))
	})
	return id
}

// pingStats is the average and mdev in ms of ten pings from the server.
type pingStats struct{ avg, mdev float64 }

var rttRe = regexp.MustCompile(`= ([\d.]+)/([\d.]+)/([\d.]+)/([\d.]+) ms`)

func (e *env) pingFrom(from *server, ip string) (pingStats, error) {
	r, err := e.ssh(from, time.Minute, "", "ping -c 10 -i 0.2 -W 2 -q "+ip)
	m := rttRe.FindStringSubmatch(r.Stdout)
	if err != nil || m == nil {
		return pingStats{}, fmt.Errorf("ping %s from %s: %v\n%s", ip, from.Name, err, r)
	}
	avg, _ := strconv.ParseFloat(m[2], 64)
	mdev, _ := strconv.ParseFloat(m[4], 64)
	return pingStats{avg, mdev}, nil
}

func (e *env) pingStats(t *testing.T, from *server, ip string) pingStats {
	t.Helper()
	s, err := e.pingFrom(from, ip)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var hostRTTRe = regexp.MustCompile(`[=<](\d+)ms`)

// hostPingStats is the average and mdev in ms of ten pings from the host.
func hostPingStats(t *testing.T, ip string) pingStats {
	t.Helper()
	ping := filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE")
	r, err := command(context.Background(), time.Minute, nil, "", ping, "-n", "10", "-w", "2000", ip)
	var times []float64
	for _, line := range strings.Split(r.Stdout, "\n") {
		if !strings.Contains(strings.ToUpper(line), "TTL=") {
			continue
		}
		if m := hostRTTRe.FindStringSubmatch(line); m != nil {
			v, _ := strconv.ParseFloat(m[1], 64)
			times = append(times, v)
		}
	}
	if err != nil || len(times) < 8 {
		t.Fatalf("ping %s from the host: %v, %d answers\n%s", ip, err, len(times), r)
	}
	var sum, sq float64
	for _, v := range times {
		sum += v
		sq += v * v
	}
	avg := sum / float64(len(times))
	return pingStats{avg, math.Sqrt(math.Max(0, sq/float64(len(times))-avg*avg))}
}

// checkRise checks that got is between lo and hi ms above base.
func checkRise(t *testing.T, what string, base, got pingStats, lo, hi float64) {
	t.Helper()
	if d := got.avg - base.avg; d < lo || d > hi {
		t.Errorf("average ping %s: %.1f ms over the baseline %.1f ms is %.1f ms, want %.0f to %.0f", what, got.avg, base.avg, d, lo, hi)
	}
}

// checkSame checks that got is within 5 ms of base.
func checkSame(t *testing.T, what string, base, got pingStats) {
	t.Helper()
	if d := math.Abs(got.avg - base.avg); d >= 5 {
		t.Errorf("average ping %s: %.1f ms, baseline %.1f ms, want within 5 ms", what, got.avg, base.avg)
	}
}

var replySeqRe = regexp.MustCompile(`bytes from .*icmp_seq=(\d+)`)

// lostSeqs pings ip from the server faultLossPings times at 10 ms and
// returns the icmp_seq without an answer.
func (e *env) lostSeqs(t *testing.T, from *server, ip string) []int {
	t.Helper()
	r, err := e.ssh(from, 2*time.Minute, "", fmt.Sprintf("ping -c %d -i 0.01 -W 2 %s", faultLossPings, ip))
	if !strings.Contains(r.Stdout, "packets transmitted") || (err != nil && r.ExitCode != 1) {
		t.Fatalf("ping %s from %s: %v\n%s", ip, from.Name, err, r)
	}
	got := map[int]bool{}
	for _, m := range replySeqRe.FindAllStringSubmatch(r.Stdout, -1) {
		s, _ := strconv.Atoi(m[1])
		got[s] = true
	}
	var lost []int
	for s := 1; s <= faultLossPings; s++ {
		if !got[s] {
			lost = append(lost, s)
		}
	}
	return lost
}

var pingLossRe = regexp.MustCompile(`\d+ packets transmitted, \d+ received.*packet loss`)

// waitSteadyPing waits until one continuous run of 800 pings at 10 ms (8 s)
// from the server to ip loses none, so a measurement after a start does not
// see the platform's drops while the VMs resume (E47: the longest gap
// between drop windows was about 3 s). A leftover 20 % loss cannot pass.
func (e *env) waitSteadyPing(t *testing.T, from *server, ip string) {
	t.Helper()
	lastLoss, lastErr := "none", "none"
	if err := waitFor(2*time.Minute, time.Second, func() (bool, error) {
		r, sshErr := e.ssh(from, 30*time.Second, "", "ping -c 800 -i 0.01 -W 1 -q "+ip)
		if r.ExitCode == 0 && strings.Contains(r.Stdout, " 0% packet loss") {
			return true, nil
		}
		if m := pingLossRe.FindString(r.Stdout); m != "" {
			lastLoss = m
			t.Logf("%s ping %s at %s: run with loss: %s", from.Name, ip, time.Now().Format(time.TimeOnly), m)
		} else {
			lastErr = fmt.Sprintf("%v (exit %d): %s", sshErr, r.ExitCode, strings.TrimSpace(r.Stdout+" "+r.Stderr))
			t.Logf("%s ping %s at %s: no ping summary: %s", from.Name, ip, time.Now().Format(time.TimeOnly), lastErr)
		}
		return false, nil
	}); err != nil {
		t.Fatalf("pings from %s to %s found no 8 s without loss: %v; last loss: %s; last ssh error: %s", from.Name, ip, err, lastLoss, lastErr)
	}
}

// waitNoPing waits until one ping from the server to ip fails, then checks
// that five fail.
func (e *env) waitNoPing(t *testing.T, from *server, ip string) {
	t.Helper()
	if err := waitFor(faultApplyTimeout, time.Second, func() (bool, error) {
		r, _ := e.ssh(from, 30*time.Second, "", "ping -c 1 -W 1 "+ip)
		return r.ExitCode == 1 || r.ExitCode == 2, nil
	}); err != nil {
		t.Errorf("%s still reaches %s: %v", from.Name, ip, err)
		return
	}
	e.noPing(t, from, ip)
}

// waitNoHostPing waits until one ping from the host to the server fails,
// then checks that three fail.
func waitNoHostPing(t *testing.T, srv *server, when string) {
	t.Helper()
	if err := waitFor(faultApplyTimeout, time.Second, func() (bool, error) { return !hostPing(srv.IPv4), nil }); err != nil {
		t.Fatalf("ping %s %s: still answered: %v", srv.IPv4, when, err)
	}
	noHostPing(t, srv, when)
}

// probeLine is one line of the probe log on A.
type probeLine struct{ dns, meta, pub, priv bool }

func (p probeLine) ok() bool { return p.dns && p.meta && p.pub && p.priv }

// startProbe starts the script on A that logs once a second whether DNS,
// the metadata service and pings to B's public and private IP answer; it
// stops when the row ends.
func (n *faultNetFixture) startProbe(t *testing.T, log string) {
	t.Helper()
	script := "#!/bin/sh\nwhile :; do\n" +
		"  d=fail; dig +time=2 +tries=1 @" + faultDNS + " api.hetzner.internal >/dev/null 2>&1 && d=ok\n" +
		"  m=fail; curl -s -f -m 2 -o /dev/null http://" + faultMetadata + "/hetzner/v1/metadata/instance-id && m=ok\n" +
		"  p=fail; ping -c 1 -W 2 " + n.b.IPv4 + " >/dev/null 2>&1 && p=ok\n" +
		"  q=fail; ping -c 1 -W 2 " + n.b.privIP + " >/dev/null 2>&1 && q=ok\n" +
		"  echo \"dns=$d meta=$m pub=$p priv=$q\" >> " + log + "\n" +
		"  sleep 1\ndone\n"
	start := "cat > " + faultProbe + " && chmod 700 " + faultProbe + " && rm -f " + log +
		" && (setsid nohup " + faultProbe + " >/dev/null 2>&1 </dev/null &)"
	if r, err := n.e.ssh(n.a.server, time.Minute, script, start); err != nil {
		t.Fatalf("start the probe on %s: %v\n%s", n.a.Name, err, r)
	}
	t.Cleanup(func() { _, _ = n.e.ssh(n.a.server, time.Minute, "", "pkill -f "+faultProbe+"; true") })
}

// readProbe reads the probe log on A.
func (n *faultNetFixture) readProbe(log string) ([]probeLine, error) {
	r, err := n.e.ssh(n.a.server, time.Minute, "", "cat "+log)
	if err != nil {
		return nil, fmt.Errorf("read %s on %s: %v\n%s", log, n.a.Name, err, r)
	}
	var lines []probeLine
	for _, l := range strings.Split(strings.TrimSpace(r.Stdout), "\n") {
		if l == "" {
			continue
		}
		lines = append(lines, probeLine{
			dns:  strings.Contains(l, "dns=ok"),
			meta: strings.Contains(l, "meta=ok"),
			pub:  strings.Contains(l, "pub=ok"),
			priv: strings.Contains(l, "priv=ok"),
		})
	}
	return lines, nil
}

// testFAULT07Cut: a network cut of public, private or all traffic on A
// stops the pings to A (and A's DNS and metadata on public); after the
// delete everything answers again in the same boot.
func testFAULT07Cut(t *testing.T) {
	f, n := sharedFaultNet(t)
	e := f.e
	a, b := n.a, n.b
	for _, traffic := range []string{"public", "private", "all"} {
		t.Run(traffic, func(t *testing.T) {
			public := traffic != "private"
			private := traffic != "public"
			e.waitSSH(t, a.server, 3*time.Minute)
			before, err := e.bootID(a.server)
			if err != nil {
				t.Fatal(err)
			}
			log := "/root/hlab-e2e-fault07-" + traffic + ".log"
			n.startProbe(t, log)
			if err := waitFor(time.Minute, 2*time.Second, func() (bool, error) {
				lines, err := n.readProbe(log)
				return err == nil && len(lines) > 0 && lines[len(lines)-1].ok(), nil
			}); err != nil {
				t.Fatalf("the probe on A never logged DNS, metadata and both pings to B answering before the cut: %v", err)
			}

			id := f.netFault(t, "network", "--server", strconv.FormatInt(a.ID, 10), "--traffic", traffic, "--loss-percent", "100")
			mark := 0
			if public {
				waitNoHostPing(t, a.server, "with the "+traffic+" cut")
				e.noPing(t, b.server, a.IPv4)
			}
			if private {
				e.waitNoPing(t, b.server, a.privIP)
			}
			if !public {
				e.waitPing(t, b.server, a.IPv4)
				lines, err := n.readProbe(log)
				if err != nil {
					t.Fatal(err)
				}
				mark = len(lines)
			}

			f.deleteFault(t, id)
			waitHostPing(t, a.server, 2*time.Minute, "after the "+traffic+" cut ended")
			e.waitPing(t, b.server, a.IPv4)
			e.waitPing(t, b.server, a.privIP)
			e.waitSSH(t, a.server, 3*time.Minute)

			var lines []probeLine
			cutSeen, okAfter := !public, false
			_ = waitFor(time.Minute, 2*time.Second, func() (bool, error) {
				lines, err = n.readProbe(log)
				if err != nil {
					return false, nil
				}
				last := mark
				for i, l := range lines {
					if !l.dns && !l.meta {
						cutSeen = true
					}
					if !l.ok() {
						last = max(last, i+1)
					}
				}
				okAfter = false
				for _, l := range lines[min(last, len(lines)):] {
					okAfter = okAfter || l.ok()
				}
				return cutSeen && okAfter, nil
			})
			if !cutSeen {
				t.Errorf("the probe log on A shows no line with DNS (%s) and metadata (%s) failing during the %s cut (%d lines)", faultDNS, faultMetadata, traffic, len(lines))
			}
			if !okAfter {
				t.Errorf("the probe log on A shows no line with DNS, metadata and the pings to B answering after the %s cut ended (%d lines)", traffic, len(lines))
			}
			after, err := e.bootID(a.server)
			if err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Errorf("boot_id after the %s cut: %s, want the same boot %s", traffic, after, before)
			}
		})
	}
}

// testFAULT07Loss: a private loss of 20 % with seed 42 drops 10 to 30 % of
// the pings, the same icmp_seq again with the same seed; a fault with a
// duration ends by itself and leaves no loss.
func testFAULT07Loss(t *testing.T) {
	f, n := sharedFaultNet(t)
	e := f.e
	args := []string{"--server", strconv.FormatInt(n.a.ID, 10), "--traffic", "private", "--loss-percent", "20", "--seed", "42"}
	e.waitPing(t, n.b.server, n.a.privIP)

	id := f.netFault(t, "network", args...)
	first := e.lostSeqs(t, n.b.server, n.a.privIP)
	if share := len(first) * 100 / faultLossPings; share < 10 || share > 30 {
		t.Errorf("loss 20 %% seed 42: %d of %d pings lost (%d %%), want 10 to 30 %%", len(first), faultLossPings, share)
	}
	f.deleteFault(t, id)

	id = f.netFault(t, "network", args...)
	second := e.lostSeqs(t, n.b.server, n.a.privIP)
	if !slices.Equal(first, second) {
		t.Errorf("loss 20 %% seed 42 created again lost icmp_seq %v, the first time %v", second, first)
	}
	f.deleteFault(t, id)

	id = f.netFault(t, "network", append(args, "--duration", "15s")...)
	if err := waitFor(time.Minute, 2*time.Second, func() (bool, error) {
		list, err := listFaults(e)
		if err != nil {
			return false, err
		}
		return !slices.ContainsFunc(list, func(fl faultInfo) bool { return fl.ID == id }), nil
	}); err != nil {
		t.Fatalf("fault %d with --duration 15s still listed: %v", id, err)
	}
	if lost := e.lostSeqs(t, n.b.server, n.a.privIP); len(lost) != 0 {
		t.Errorf("after the fault with --duration 15s ran out %d pings lost (icmp_seq %v), want none", len(lost), lost)
	}
}

// testFAULT08Locations: latency between fsn1 and hel1 adds about 100 ms to
// a round trip from A to C and nothing from A to B.
func testFAULT08Locations(t *testing.T) {
	f, n := sharedFaultNet(t)
	e := f.e
	a := n.a.server
	baseCPub := e.pingStats(t, a, n.c.IPv4)
	baseCPriv := e.pingStats(t, a, n.c.privIP)
	baseBPub := e.pingStats(t, a, n.b.IPv4)
	baseBPriv := e.pingStats(t, a, n.b.privIP)

	id := f.netFault(t, "latency", "--from", serverLocation, "--to", faultOtherLocation, "--delay-ms", "100")
	checkRise(t, "A to C public", baseCPub, e.pingStats(t, a, n.c.IPv4), 80, 120)
	checkRise(t, "A to C private", baseCPriv, e.pingStats(t, a, n.c.privIP), 80, 120)
	checkRise(t, "A to B public", baseBPub, e.pingStats(t, a, n.b.IPv4), math.Inf(-1), 5)
	checkRise(t, "A to B private", baseBPriv, e.pingStats(t, a, n.b.privIP), math.Inf(-1), 5)

	f.deleteFault(t, id)
	checkSame(t, "A to C public after the latency ended", baseCPub, e.pingStats(t, a, n.c.IPv4))
	checkSame(t, "A to C private after the latency ended", baseCPriv, e.pingStats(t, a, n.c.privIP))
}

// testFAULT08Server: latency of 100 ms with jitter 20 on A adds about
// 100 ms with a spread to the host's pings to A and nothing from B to C.
func testFAULT08Server(t *testing.T) {
	f, n := sharedFaultNet(t)
	e := f.e
	baseHost := hostPingStats(t, n.a.IPv4)
	baseBC := e.pingStats(t, n.b.server, n.c.IPv4)

	id := f.netFault(t, "latency", "--server", strconv.FormatInt(n.a.ID, 10), "--delay-ms", "100", "--jitter-ms", "20")
	got := hostPingStats(t, n.a.IPv4)
	checkRise(t, "host to A", baseHost, got, 80, 120)
	if got.mdev <= 3 {
		t.Errorf("ping host to A with jitter 20 ms: mdev %.1f ms, want more than 3 ms", got.mdev)
	}
	checkSame(t, "B to C", baseBC, e.pingStats(t, n.b.server, n.c.IPv4))

	f.deleteFault(t, id)
	checkSame(t, "host to A after the latency ended", baseHost, hostPingStats(t, n.a.IPv4))
}

// faultBoxAccess is one way to reach the fault fixture's box; err is nil when
// the access worked.
type faultBoxAccess struct {
	name           string
	blocked, works func() error
}

// faultBoxAccesses are SFTP, SMB and WebDAV from server B, from WSL2 and
// from the host.
func (n *faultNetFixture) faultBoxAccesses(t *testing.T) []faultBoxAccess {
	t.Helper()
	e := n.e
	box := n.box
	_, w := sharedWSL(t)
	dir := wslWorkDir(t, w)
	c := hostHTTPS(t)
	b := n.b.server
	sshOpts := " -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null" +
		" -o ConnectTimeout=10 -o LogLevel=ERROR -P 22 -b - " + box.Username + "@" + box.Server
	propfind := "-k -X PROPFIND -H 'Depth: 0' -o /dev/null -w '%{http_code}' https://" + box.Server + "/"
	check := func(what string, r result, err error, want string) error {
		if err == nil && want != "" && strings.TrimSpace(r.Stdout) != want {
			err = fmt.Errorf("answered %q, want %s", strings.TrimSpace(r.Stdout), want)
		}
		if err == nil && strings.Contains(r.Stdout+r.Stderr, "NT_STATUS_") {
			err = errors.New("smbclient reported an NT_STATUS")
		}
		if err != nil {
			return fmt.Errorf("%s: %v\n%s", what, err, r)
		}
		return nil
	}
	deadline := func(what string, f func() error) error { return withDeadline(faultAttemptTimeout, what, f) }

	serverSFTP := func() error {
		r, err := e.ssh(b, time.Minute, "ls\n", "timeout 30 sftp -i "+faultBoxKey+sshOpts)
		return check("sftp from B", r, err, "")
	}
	serverSMB := func() error {
		creds := fmt.Sprintf("username=%s\npassword=%s\n", box.Username, box.Password)
		r, err := e.ssh(b, time.Minute, creds, "umask 077; f=$(mktemp); cat > \"$f\"; timeout 30 smbclient '//"+box.Server+
			"/backup' -A \"$f\" -m SMB3 -c ls 2>&1; rc=$?; rm -f \"$f\"; exit $rc")
		return check("smbclient from B", r, err, "")
	}
	serverDAV := func() error {
		r, err := e.serverCurl(b, box, propfind)
		return check("WebDAV from B", r, err, "207")
	}
	wslSFTP := func() error {
		r, err := w.run(context.Background(), time.Minute, nil, "printf 'ls\\n' | timeout 30 sftp -i '"+w.key+"'"+sshOpts+"\n")
		return check("sftp from WSL2", r, err, "")
	}
	wslSMB := func() error {
		return deadline("smbclient from WSL2", func() error {
			r, err := w.smbclient(dir, box, "backup", "ls")
			return check("smbclient from WSL2", r, err, "")
		})
	}
	wslDAV := func() error {
		script := "umask 077\nprintf 'machine %s login %s password %s\\n' '" + box.Server + "' '" + box.Username +
			"' \"$HLAB_E2E_PASSWORD\" > netrc\ncode=$(timeout 30 curl -sS --max-time 30 --netrc-file netrc " + propfind +
			") || { rm -f netrc; exit 1; }\nrm -f netrc\necho \"$code\"\n"
		r, err := w.wslPassword(dir, box, script)
		return check("WebDAV from WSL2", r, err, "207")
	}
	hostSFTPAccess := func() error {
		return deadline("sftp from the host", func() error {
			r, err := hostSFTP(login{user: box.Username, server: box.Server, key: e.key, port: 22}, "ls\n")
			return check("sftp from the host", r, err, "")
		})
	}
	hostDAV := func() error {
		return deadline("WebDAV from the host", func() error {
			status, _, err := davDo(c, box, "PROPFIND", "", nil, map[string]string{"Depth": "0"})
			if err == nil && status != 207 {
				err = fmt.Errorf("status %d, want 207", status)
			}
			if err != nil {
				return fmt.Errorf("WebDAV from the host: %w", err)
			}
			return nil
		})
	}
	unc := `\\` + box.Server + `\backup`
	t.Cleanup(func() { netDelete(unc) })
	hostSMBBlocked := func() error {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(n.boxIP, "445"), 3*time.Second)
		if err != nil {
			return err
		}
		_ = conn.Close()
		return nil
	}
	hostSMBWorks := func() error {
		var last error
		if err := waitFor(2*time.Minute, 5*time.Second, func() (bool, error) {
			_, last = hostNet(box.Password, "use", unc, box.Password, "/user:"+box.Username, "/persistent:no")
			return last == nil, nil
		}); err != nil {
			return fmt.Errorf("net use %s: %v (last: %v)", unc, err, last)
		}
		netDelete(unc)
		return nil
	}
	return []faultBoxAccess{
		{"sftp/server", serverSFTP, serverSFTP},
		{"sftp/wsl", wslSFTP, wslSFTP},
		{"sftp/host", hostSFTPAccess, hostSFTPAccess},
		{"smb/server", serverSMB, serverSMB},
		{"smb/wsl", wslSMB, wslSMB},
		{"smb/host", hostSMBBlocked, hostSMBWorks},
		{"webdav/server", serverDAV, serverDAV},
		{"webdav/wsl", wslDAV, wslDAV},
		{"webdav/host", hostDAV, hostDAV},
	}
}

// hostSMBConnections returns the host's TCP connections to ip:445 as
// netstat lists them.
func hostSMBConnections(ip string) ([]string, error) {
	r, err := command(context.Background(), 30*time.Second, nil, "", "netstat", "-n", "-p", "tcp")
	if err != nil {
		return nil, fmt.Errorf("netstat: %v\n%s", err, r)
	}
	var conns []string
	for _, l := range strings.Split(r.Stdout, "\n") {
		if strings.Contains(l, " "+ip+":445 ") {
			conns = append(conns, strings.TrimSpace(l))
		}
	}
	return conns, nil
}

// testFAULT11: storage_box_unavailable makes the box unreachable over
// SFTP, SMB and WebDAV from B, WSL2 and the host; after the delete all
// work again, SMB from the host within 2 minutes.
func testFAULT11(t *testing.T) {
	f, n := sharedFaultNet(t)
	accesses := n.faultBoxAccesses(t)

	netDelete(`\\` + n.box.Server)
	var conns []string
	if err := waitFor(2*time.Minute, 2*time.Second, func() (bool, error) {
		var err error
		conns, err = hostSMBConnections(n.boxIP)
		return err == nil && len(conns) == 0, err
	}); err != nil {
		t.Fatalf("host TCP connections to %s:445 before the fault: %v (%v)", n.boxIP, conns, err)
	}

	id := f.netFault(t, "storage_box_unavailable", "--storage-box", strconv.FormatInt(n.box.ID, 10))
	t.Run("blocked", func(t *testing.T) {
		for _, a := range accesses {
			t.Run(a.name, func(t *testing.T) {
				t.Parallel()
				if err := a.blocked(); err == nil {
					t.Errorf("%s works with storage_box_unavailable, want a failure", a.name)
				}
			})
		}
	})

	f.deleteFault(t, id)
	t.Run("restored", func(t *testing.T) {
		for _, a := range accesses {
			t.Run(a.name, func(t *testing.T) {
				t.Parallel()
				if err := a.works(); err != nil {
					t.Errorf("%s after storage_box_unavailable ended: %v", a.name, err)
				}
			})
		}
	})
}

// netFaultsBeforeRestart measures A to C and creates a private loss fault
// on A and a latency fault between fsn1 and hel1 for the restart rows.
func (f *faultFixture) netFaultsBeforeRestart() error {
	n, err := f.net()
	if err != nil {
		return err
	}
	e := f.e
	pub, err := e.pingFrom(n.a.server, n.c.IPv4)
	if err != nil {
		return err
	}
	priv, err := e.pingFrom(n.a.server, n.c.privIP)
	if err != nil {
		return err
	}
	n.restartPub, n.restartPriv = pub.avg, priv.avg
	if _, err := createFault(e, "network", "--server", strconv.FormatInt(n.a.ID, 10), "--traffic", "private",
		"--loss-percent", "20", "--seed", "42"); err != nil {
		return err
	}
	_, err = createFault(e, "latency", "--from", serverLocation, "--to", faultOtherLocation, "--delay-ms", "100")
	return err
}

// testFAULT07Restart: after the stop and start no fault is listed and the
// pings from B to A's private IP lose none.
func testFAULT07Restart(t *testing.T) {
	f, n := sharedFaultNet(t)
	runFaultRestart(t, f)
	e := f.e
	list, err := listFaults(e)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("hlab fault list after stop and start: %+v, want none", list)
	}
	e.waitSSH(t, n.a.server, 5*time.Minute)
	e.waitSSH(t, n.b.server, 5*time.Minute)
	e.waitPing(t, n.b.server, n.a.privIP)
	e.waitSteadyPing(t, n.b.server, n.a.privIP)
	if lost := e.lostSeqs(t, n.b.server, n.a.privIP); len(lost) != 0 {
		t.Errorf("after stop and start with a loss fault before %d pings lost (icmp_seq %v), want none", len(lost), lost)
	}
}

// testFAULT08Restart: after the stop and start the pings from A to C are
// back at their baseline.
func testFAULT08Restart(t *testing.T) {
	f, n := sharedFaultNet(t)
	runFaultRestart(t, f)
	e := f.e
	e.waitSSH(t, n.a.server, 5*time.Minute)
	e.waitSSH(t, n.c.server, 5*time.Minute)
	e.waitPing(t, n.a.server, n.c.privIP)
	e.waitSteadyPing(t, n.a.server, n.c.privIP)
	checkSame(t, "A to C public after stop and start", pingStats{avg: n.restartPub}, e.pingStats(t, n.a.server, n.c.IPv4))
	checkSame(t, "A to C private after stop and start", pingStats{avg: n.restartPriv}, e.pingStats(t, n.a.server, n.c.privIP))
}
