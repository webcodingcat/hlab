//go:build linux

package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// lbRouteProtocol tags the routes of the load balancer veths. It differs
// from routeProtocol, so the network and VRF appliers leave them alone.
const lbRouteProtocol netlink.RouteProtocol = 201

// lbPeerAddr is the address of the root end of every load balancer veth
// (decision D1); the netns ends route to it onlink.
var lbPeerAddr = netip.MustParseAddr("169.254.0.1")

// Names of the netns ends of the veths.
const (
	lbPublicPeer  = "pub"
	lbNetworkPeer = "net"
)

// lbSocketTimeout bounds one command on an HAProxy socket.
const lbSocketTimeout = 10 * time.Second

// LoadBalancers is the Applier of the load balancers. Each LB gets the
// netns lb<ID> with
//   - the veth lbp<ID> to the root namespace while it has a public IPv4:
//     root end 169.254.0.1/32 with the route <public IP>/32, netns end
//     "pub" with the public IP /32 and a default route via 169.254.0.1
//   - the veth lbn<ID> while it is attached to a network: root end enslaved to
//     vrf<vlan> with 169.254.0.1/32 and the VRF route <private IP>/32,
//     netns end "net" with the private IP /32 and a route to the network's
//     range via 169.254.0.1 with the private IP as source
//
// and one HAProxy master-worker running in the netns, configured in
// <Dir>/lb<ID>/. A changed configuration is checked with `haproxy -c` and
// then loaded through the master CLI's reload. LBs no longer in the
// document are stopped and their netns deleted. A failing HAProxy (not
// installed, a configuration it refuses) is logged and leaves the other LBs
// and appliers working.
type LoadBalancers struct {
	Dir string // "/run/hlab"

	mu  sync.Mutex
	ids []int64 // LBs of the last apply, for LBStatus

	procs map[int64]*lbProc // HAProxy masters started by this agent; Apply only
}

// lbProc is an HAProxy master started by the agent; done closes when it
// exits.
type lbProc struct {
	pid  int
	done chan struct{}
}

// NewLoadBalancers returns the load balancer Applier keeping its files
// below dir.
func NewLoadBalancers(dir string) *LoadBalancers { return &LoadBalancers{Dir: dir} }

// Name implements Applier.
func (l *LoadBalancers) Name() string { return "loadbalancers" }

// Apply implements Applier.
func (l *LoadBalancers) Apply(ctx context.Context, cfg agentapi.Config) error {
	want := map[int64]bool{}
	ranges := map[int64]netip.Prefix{}
	for _, n := range cfg.Networks {
		ranges[n.ID] = n.IPRange
	}
	ids := make([]int64, 0, len(cfg.LoadBalancers))
	for _, lb := range cfg.LoadBalancers {
		want[lb.ID] = true
		ids = append(ids, lb.ID)
	}
	if err := l.removeStale(ctx, want); err != nil {
		return err
	}
	for _, lb := range cfg.LoadBalancers {
		if err := l.setupNetns(ctx, lb, ranges); err != nil {
			return fmt.Errorf("loadbalancers: lb %d: %w", lb.ID, err)
		}
		if err := l.runHAProxy(ctx, lb); err != nil {
			slog.Error("load balancer haproxy", "lb", lb.ID, "err", err)
		}
	}
	l.mu.Lock()
	l.ids = ids
	l.mu.Unlock()
	return nil
}

// LBStatus returns the target health and counters of every running LB,
// read from `show stat` on its stats socket. An LB whose HAProxy does not
// answer is left out.
func (l *LoadBalancers) LBStatus() (map[int64][]agentapi.TargetHealth, map[int64]agentapi.LBCounters) {
	l.mu.Lock()
	ids := append([]int64(nil), l.ids...)
	l.mu.Unlock()
	health := map[int64][]agentapi.TargetHealth{}
	counters := map[int64]agentapi.LBCounters{}
	for _, id := range ids {
		out, err := haproxyCommand(filepath.Join(l.lbDir(id), "haproxy.sock"), "show stat")
		if err != nil {
			slog.Debug("load balancer stats", "lb", id, "err", err)
			continue
		}
		h, c, err := ParseStat(out)
		if err != nil {
			slog.Warn("load balancer stats", "lb", id, "err", err)
			continue
		}
		health[id] = h
		counters[id] = c
	}
	return health, counters
}

func (l *LoadBalancers) lbDir(id int64) string {
	return filepath.Join(l.Dir, "lb"+strconv.FormatInt(id, 10))
}

func lbNetns(id int64) string { return "lb" + strconv.FormatInt(id, 10) }

// lbID reports the LB ID of a name made of prefix and a decimal ID.
func lbID(name, prefix string) (int64, bool) {
	s, ok := strings.CutPrefix(name, prefix)
	if !ok || s == "" || s[0] == '0' {
		return 0, false
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || strconv.FormatInt(id, 10) != s {
		return 0, false
	}
	return id, true
}

// removeStale stops the HAProxy, deletes the netns and the files of every
// LB not in want, and deletes root veths of LBs not in want.
func (l *LoadBalancers) removeStale(ctx context.Context, want map[int64]bool) error {
	stale := map[int64]bool{}
	for _, dir := range []string{"/run/netns", l.Dir} {
		entries, err := os.ReadDir(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("loadbalancers: %w", err)
		}
		for _, e := range entries {
			if id, ok := lbID(e.Name(), "lb"); ok && !want[id] {
				stale[id] = true
			}
		}
	}
	for id := range stale {
		dir := l.lbDir(id)
		l.stopHAProxy(id)
		if _, err := os.Stat(filepath.Join("/run/netns", lbNetns(id))); err == nil {
			if out, err := exec.CommandContext(ctx, "ip", "netns", "del", lbNetns(id)).CombinedOutput(); err != nil {
				return fmt.Errorf("loadbalancers: delete netns %s: %v: %s", lbNetns(id), err, bytes.TrimSpace(out))
			}
		}
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("loadbalancers: %w", err)
		}
		slog.Info("load balancer removed", "lb", id)
	}

	links, err := dump(netlink.LinkList)
	if err != nil {
		return fmt.Errorf("loadbalancers: list links: %w", err)
	}
	for _, lk := range links {
		name := lk.Attrs().Name
		id, ok := lbID(name, "lbp")
		if !ok {
			id, ok = lbID(name, "lbn")
		}
		if ok && !want[id] && lk.Type() == "veth" {
			if err := linkDel(lk); err != nil {
				return fmt.Errorf("loadbalancers: delete link %s: %w", name, err)
			}
		}
	}
	return nil
}

// setupNetns makes the netns of lb and its veths, addresses and routes.
func (l *LoadBalancers) setupNetns(ctx context.Context, lb agentapi.LoadBalancer, ranges map[int64]netip.Prefix) error {
	name := lbNetns(lb.ID)
	if _, err := os.Stat(filepath.Join("/run/netns", name)); err != nil {
		if out, err := exec.CommandContext(ctx, "ip", "netns", "add", name).CombinedOutput(); err != nil {
			return fmt.Errorf("add netns %s: %v: %s", name, err, bytes.TrimSpace(out))
		}
	}
	ns, err := netns.GetFromName(name)
	if err != nil {
		return fmt.Errorf("open netns %s: %w", name, err)
	}
	defer func() { _ = ns.Close() }()
	h, err := netlink.NewHandleAt(ns)
	if err != nil {
		return fmt.Errorf("netlink in netns %s: %w", name, err)
	}
	defer h.Close()

	lo, err := h.LinkByName("lo")
	if err != nil {
		return fmt.Errorf("lo in %s: %w", name, err)
	}
	if err := h.LinkSetUp(lo); err != nil {
		return fmt.Errorf("lo in %s up: %w", name, err)
	}

	pubName := "lbp" + strconv.FormatInt(lb.ID, 10)
	if lb.PublicIPv4.Is4() {
		root, peer, err := ensureVeth(h, ns, pubName, lbPublicPeer, 0)
		if err != nil {
			return err
		}
		if err := syncDevRoutes(nil, root, unix.RT_TABLE_MAIN, []netlink.Route{{
			LinkIndex: root.Attrs().Index,
			Dst:       hostNet(lb.PublicIPv4),
			Scope:     netlink.SCOPE_LINK,
		}}); err != nil {
			return err
		}
		if err := syncPeerAddr(h, peer, lb.PublicIPv4); err != nil {
			return err
		}
		if err := syncDevRoutes(h, peer, unix.RT_TABLE_MAIN, []netlink.Route{{
			LinkIndex: peer.Attrs().Index,
			Dst:       &net.IPNet{IP: net.IPv4zero.To4(), Mask: net.CIDRMask(0, 32)},
			Gw:        lbPeerAddr.AsSlice(),
		}}); err != nil {
			return err
		}
	} else if err := deleteLink(pubName); err != nil {
		return err
	}

	netName := "lbn" + strconv.FormatInt(lb.ID, 10)
	// An LB is attached to one network at most.
	if len(lb.Private) == 0 {
		return deleteLink(netName)
	}
	priv := lb.Private[0]
	rng, ok := ranges[priv.NetworkID]
	if !ok || !priv.IP.Is4() {
		return fmt.Errorf("network %d: not in the document or no IPv4", priv.NetworkID)
	}
	rng = rng.Masked()
	vrf, err := netlink.LinkByName("vrf" + strconv.Itoa(priv.VLAN))
	if err != nil {
		return fmt.Errorf("vrf of vlan %d: %w", priv.VLAN, err)
	}
	root, peer, err := ensureVeth(h, ns, netName, lbNetworkPeer, vrf.Attrs().Index)
	if err != nil {
		return err
	}
	if err := syncDevRoutes(nil, root, vrfTableBase+priv.VLAN, []netlink.Route{{
		LinkIndex: root.Attrs().Index,
		Dst:       hostNet(priv.IP),
		Scope:     netlink.SCOPE_LINK,
	}}); err != nil {
		return err
	}
	if err := syncPeerAddr(h, peer, priv.IP); err != nil {
		return err
	}
	return syncDevRoutes(h, peer, unix.RT_TABLE_MAIN, []netlink.Route{{
		LinkIndex: peer.Attrs().Index,
		Dst:       &net.IPNet{IP: rng.Addr().AsSlice(), Mask: net.CIDRMask(rng.Bits(), 32)},
		Gw:        lbPeerAddr.AsSlice(),
		Src:       priv.IP.AsSlice(),
	}})
}

// ensureVeth makes the veth rootName in the root namespace with its peer
// peerName in the netns of h, the root end enslaved to the device master
// (0: none) and carrying 169.254.0.1/32, both ends up. A root end with
// another master is made anew.
func ensureVeth(h *netlink.Handle, ns netns.NsHandle, rootName, peerName string, master int) (netlink.Link, netlink.Link, error) {
	root, err := netlink.LinkByName(rootName)
	missing := errors.As(err, &netlink.LinkNotFoundError{})
	if err != nil && !missing {
		return nil, nil, fmt.Errorf("link %s: %w", rootName, err)
	}
	if err == nil && root.Attrs().MasterIndex != master {
		if err := linkDel(root); err != nil {
			return nil, nil, fmt.Errorf("delete link %s: %w", rootName, err)
		}
		missing = true
	}
	if missing {
		veth := &netlink.Veth{
			LinkAttrs:     netlink.LinkAttrs{Name: rootName},
			PeerName:      peerName,
			PeerNamespace: netlink.NsFd(ns),
		}
		if err := netlink.LinkAdd(veth); err != nil {
			return nil, nil, fmt.Errorf("add veth %s: %w", rootName, err)
		}
		if root, err = netlink.LinkByName(rootName); err != nil {
			return nil, nil, fmt.Errorf("link %s: %w", rootName, err)
		}
		if master != 0 {
			if err := netlink.LinkSetMasterByIndex(root, master); err != nil {
				return nil, nil, fmt.Errorf("enslave %s: %w", rootName, err)
			}
		}
	}
	if err := netlink.AddrReplace(root, &netlink.Addr{IPNet: hostNet(lbPeerAddr)}); err != nil {
		return nil, nil, fmt.Errorf("address on %s: %w", rootName, err)
	}
	if err := netlink.LinkSetUp(root); err != nil {
		return nil, nil, fmt.Errorf("link %s up: %w", rootName, err)
	}
	peer, err := h.LinkByName(peerName)
	if err != nil {
		return nil, nil, fmt.Errorf("peer %s of %s: %w", peerName, rootName, err)
	}
	if err := h.LinkSetUp(peer); err != nil {
		return nil, nil, fmt.Errorf("peer %s of %s up: %w", peerName, rootName, err)
	}
	return root, peer, nil
}

// deleteLink deletes the root link name if it exists.
func deleteLink(name string) error {
	lk, err := netlink.LinkByName(name)
	if errors.As(err, &netlink.LinkNotFoundError{}) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("link %s: %w", name, err)
	}
	if err := linkDel(lk); err != nil {
		return fmt.Errorf("delete link %s: %w", name, err)
	}
	return nil
}

// linkDel deletes lk; a link already gone is no error. Deleting a netns or
// one end of a veth removes the other end too, sometimes after it was listed.
func linkDel(lk netlink.Link) error {
	err := netlink.LinkDel(lk)
	if errors.Is(err, unix.ENODEV) {
		return nil
	}
	return err
}

// syncPeerAddr makes a /32 the only IPv4 address of the netns link.
func syncPeerAddr(h *netlink.Handle, link netlink.Link, a netip.Addr) error {
	have, err := dump(func() ([]netlink.Addr, error) {
		return h.AddrList(link, netlink.FAMILY_V4)
	})
	if err != nil {
		return fmt.Errorf("addresses of %s: %w", link.Attrs().Name, err)
	}
	want := hostNet(a).String()
	for _, x := range have {
		if x.IPNet.String() == want {
			continue
		}
		if err := h.AddrDel(link, &x); err != nil {
			return fmt.Errorf("delete address %s on %s: %w", x.IPNet, link.Attrs().Name, err)
		}
	}
	if err := h.AddrReplace(link, &netlink.Addr{IPNet: hostNet(a)}); err != nil {
		return fmt.Errorf("address %s on %s: %w", a, link.Attrs().Name, err)
	}
	return nil
}

// syncDevRoutes makes the lbRouteProtocol routes of link in table exactly
// want; gateways are onlink. h nil is the root namespace.
func syncDevRoutes(h *netlink.Handle, link netlink.Link, table int, want []netlink.Route) error {
	if h == nil {
		h = &netlink.Handle{}
	}
	key := func(r netlink.Route) string {
		dst := "0.0.0.0/0"
		if r.Dst != nil {
			dst = r.Dst.String()
		}
		return fmt.Sprintf("%s|%s|%s", dst, r.Gw, r.Src)
	}
	keep := map[string]bool{}
	for i := range want {
		want[i].Table = table
		want[i].Protocol = lbRouteProtocol
		if want[i].Gw != nil {
			want[i].SetFlag(netlink.FLAG_ONLINK)
		}
		keep[key(want[i])] = true
	}
	have, err := dump(func() ([]netlink.Route, error) {
		return h.RouteListFiltered(netlink.FAMILY_V4,
			&netlink.Route{LinkIndex: link.Attrs().Index, Table: table, Protocol: lbRouteProtocol},
			netlink.RT_FILTER_OIF|netlink.RT_FILTER_TABLE|netlink.RT_FILTER_PROTOCOL)
	})
	if err != nil {
		return fmt.Errorf("routes of %s: %w", link.Attrs().Name, err)
	}
	for _, r := range have {
		if keep[key(r)] {
			continue
		}
		if err := h.RouteDel(&r); err != nil && !errors.Is(err, unix.ESRCH) {
			return fmt.Errorf("delete route %s on %s: %w", r.Dst, link.Attrs().Name, err)
		}
	}
	for i := range want {
		if err := h.RouteReplace(&want[i]); err != nil {
			return fmt.Errorf("route %s on %s: %w", want[i].Dst, link.Attrs().Name, err)
		}
	}
	return nil
}

// runHAProxy writes the configuration of lb and starts its HAProxy, or
// reloads it when the configuration changed.
func (l *LoadBalancers) runHAProxy(ctx context.Context, lb agentapi.LoadBalancer) error {
	dir := l.lbDir(lb.ID)
	cfg, files := HAProxyConfig(lb, dir)
	bin, err := exec.LookPath("haproxy")
	if err != nil {
		return fmt.Errorf("haproxy not installed: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	cfgPath := filepath.Join(dir, "haproxy.cfg")
	changed, err := writeLBFiles(dir, cfg, files)
	if err != nil {
		return err
	}
	pid := l.haproxyPID(lb.ID)
	if !changed && pid != 0 {
		return nil
	}
	if changed {
		next := cfgPath + ".new"
		if err := os.WriteFile(next, []byte(cfg), 0o600); err != nil {
			return err
		}
		if out, err := exec.CommandContext(ctx, bin, "-c", "-f", next).CombinedOutput(); err != nil {
			return fmt.Errorf("haproxy -c: %v: %s", err, bytes.TrimSpace(out))
		}
		if err := os.Rename(next, cfgPath); err != nil {
			return err
		}
	}
	if pid != 0 {
		return reloadHAProxy(dir)
	}
	return l.startHAProxy(bin, lb.ID, dir)
}

// writeLBFiles writes the certificate files that differ from the disk and
// removes the ones no longer used. It reports whether a certificate file
// changed or cfg differs from haproxy.cfg; it does not write haproxy.cfg.
func writeLBFiles(dir, cfg string, files map[string][]byte) (bool, error) {
	changed := false
	old, err := os.ReadFile(filepath.Join(dir, "haproxy.cfg"))
	if err != nil || string(old) != cfg {
		changed = true
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if have, err := os.ReadFile(p); err == nil && bytes.Equal(have, content) {
			continue
		}
		if err := os.WriteFile(p, content, 0o600); err != nil {
			return false, err
		}
		changed = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if _, used := files[e.Name()]; strings.HasSuffix(e.Name(), ".pem") && !used {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return false, err
			}
		}
	}
	return changed, nil
}

// startHAProxy starts the HAProxy master of LB id in its netns. The agent
// waits for the process, so it never stays a zombie.
func (l *LoadBalancers) startHAProxy(bin string, id int64, dir string) error {
	for _, s := range []string{"haproxy.sock", "master.sock"} {
		if err := os.Remove(filepath.Join(dir, s)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	// The master outlives the request that started it.
	cmd := exec.Command("ip", "netns", "exec", lbNetns(id), bin, "-W",
		"-f", filepath.Join(dir, "haproxy.cfg"),
		"-S", filepath.Join(dir, "master.sock")+",mode,600",
		"-p", filepath.Join(dir, "haproxy.pid"))
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start haproxy: %w", err)
	}
	slog.Info("load balancer haproxy started", "lb", id, "pid", cmd.Process.Pid)
	p := &lbProc{pid: cmd.Process.Pid, done: make(chan struct{})}
	if l.procs == nil {
		l.procs = map[int64]*lbProc{}
	}
	l.procs[id] = p
	go func() {
		err := cmd.Wait()
		close(p.done)
		slog.Info("load balancer haproxy exited", "lb", id, "err", err)
	}()
	return nil
}

// reloadHAProxy saves the server state for the new workers and reloads
// the configuration through the master CLI.
func reloadHAProxy(dir string) error {
	if state, err := haproxyCommand(filepath.Join(dir, "haproxy.sock"), "show servers state"); err == nil {
		if err := os.WriteFile(filepath.Join(dir, "server-state"), state, 0o600); err != nil {
			return err
		}
	}
	out, err := haproxyCommand(filepath.Join(dir, "master.sock"), "reload")
	if err != nil {
		return fmt.Errorf("haproxy reload: %w", err)
	}
	if bytes.Contains(out, []byte("Success=0")) {
		return fmt.Errorf("haproxy reload failed: %s", bytes.TrimSpace(out))
	}
	slog.Info("load balancer haproxy reloaded", "dir", dir)
	return nil
}

// haproxyPID returns the PID of the running HAProxy master of LB id, 0 if
// none runs: the one this agent started, else the one in the PID file (a
// master left by an earlier agent).
func (l *LoadBalancers) haproxyPID(id int64) int {
	if p := l.procs[id]; p != nil {
		select {
		case <-p.done:
			delete(l.procs, id)
		default:
			return p.pid
		}
	}
	raw, err := os.ReadFile(filepath.Join(l.lbDir(id), "haproxy.pid"))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0
	}
	comm, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
	if err != nil || strings.TrimSpace(string(comm)) != "haproxy" {
		return 0
	}
	return pid
}

// stopHAProxy stops the HAProxy master of LB id and its workers, if one
// runs: SIGTERM, then SIGKILL after five seconds.
func (l *LoadBalancers) stopHAProxy(id int64) {
	pid := l.haproxyPID(id)
	if pid == 0 {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(5 * time.Second)
	for l.haproxyPID(id) != 0 {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// haproxyCommand sends one command to the HAProxy socket sock and returns
// the answer.
func haproxyCommand(sock, command string) ([]byte, error) {
	c, err := net.DialTimeout("unix", sock, lbSocketTimeout)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	if err := c.SetDeadline(time.Now().Add(lbSocketTimeout)); err != nil {
		return nil, err
	}
	if _, err := io.WriteString(c, command+"\n"); err != nil {
		return nil, err
	}
	return io.ReadAll(c)
}
