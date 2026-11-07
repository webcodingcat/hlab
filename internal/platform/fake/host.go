package fake

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"slices"

	"github.com/webcodingcat/hlab/internal/platform"
)

// SetUsedRanges names address ranges that the machine uses already, so that
// HostNet.CheckRanges refuses them.
func (f *Fake) SetUsedRanges(ranges ...netip.Prefix) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usedRanges = ranges
}

// Link returns what HostNet.SetUp set up, and false after TearDown.
func (f *Fake) Link() (platform.LinkSpec, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.link == nil {
		return platform.LinkSpec{}, false
	}
	return *f.link, true
}

// DNSRules returns the host's DNS rules.
func (f *Fake) DNSRules() []platform.DNSRule {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.dnsRules)
}

// RequestStop asks HLab to stop, as the tray or a SIGTERM would.
func (f *Fake) RequestStop() {
	f.stopOnce.Do(func() { close(f.stop) })
}

// ServeAgent makes the agent of a VM answer: every dial to it hands serve the
// other end of the connection.
func (f *Fake) ServeAgent(vm string, serve func(conn net.Conn)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agents[vm] = serve
}

// HostNet is the fake machine's host network.
type HostNet struct {
	fake *Fake
}

// CheckRanges refuses a range that overlaps one of SetUsedRanges.
func (n *HostNet) CheckRanges(ctx context.Context, ranges []netip.Prefix) error {
	if err := n.fake.enter(ctx, "CheckRanges", ranges); err != nil {
		return err
	}
	n.fake.mu.Lock()
	defer n.fake.mu.Unlock()
	for _, wanted := range ranges {
		for _, used := range n.fake.usedRanges {
			if wanted.Overlaps(used) {
				return fmt.Errorf("range %s overlaps %s: %w", wanted, used, platform.ErrInUse)
			}
		}
	}
	return nil
}

// SetUp keeps the link; a second SetUp replaces it, as taking over a link
// that is there already.
func (n *HostNet) SetUp(ctx context.Context, link platform.LinkSpec) error {
	if err := n.fake.enter(ctx, "SetUp", link.Switch); err != nil {
		return err
	}
	n.fake.mu.Lock()
	defer n.fake.mu.Unlock()
	n.fake.link = &link
	return nil
}

// TearDown removes the link; without one there is nothing to do.
func (n *HostNet) TearDown(ctx context.Context) error {
	if err := n.fake.enter(ctx, "TearDown"); err != nil {
		return err
	}
	n.fake.mu.Lock()
	defer n.fake.mu.Unlock()
	n.fake.link = nil
	return nil
}

// HostDNS is the fake machine's DNS rules.
type HostDNS struct {
	fake *Fake
}

// Apply replaces the rules.
func (d *HostDNS) Apply(ctx context.Context, rules []platform.DNSRule) error {
	if err := d.fake.enter(ctx, "Apply", len(rules)); err != nil {
		return err
	}
	d.fake.mu.Lock()
	defer d.fake.mu.Unlock()
	d.fake.dnsRules = slices.Clone(rules)
	return nil
}

// Clear removes every rule.
func (d *HostDNS) Clear(ctx context.Context) error {
	if err := d.fake.enter(ctx, "Clear"); err != nil {
		return err
	}
	d.fake.mu.Lock()
	defer d.fake.mu.Unlock()
	d.fake.dnsRules = nil
	return nil
}

// Lifecycle is the fake machine's process life cycle.
type Lifecycle struct {
	fake *Fake
}

// Lock lets one holder per data folder in; the fake machine is the whole
// world, so two labs on one Fake see each other.
func (l *Lifecycle) Lock(dataDir string) (func(), error) {
	if err := l.fake.enter(context.Background(), "Lock", dataDir); err != nil {
		return nil, err
	}
	l.fake.mu.Lock()
	defer l.fake.mu.Unlock()
	if l.fake.locked[dataDir] {
		return nil, fmt.Errorf("data folder %s: %w", dataDir, platform.ErrLocked)
	}
	l.fake.locked[dataDir] = true
	unlock := func() {
		l.fake.mu.Lock()
		defer l.fake.mu.Unlock()
		delete(l.fake.locked, dataDir)
	}
	return unlock, nil
}

// WriteSecret writes the secret as a real file, readable by its owner only.
func (l *Lifecycle) WriteSecret(path string, token []byte) error {
	if err := l.fake.enter(context.Background(), "WriteSecret", path); err != nil {
		return err
	}
	return os.WriteFile(path, token, 0o600)
}

// StopRequests is closed by Fake.RequestStop.
func (l *Lifecycle) StopRequests() <-chan struct{} {
	return l.fake.stop
}

// AgentDialer reaches the agents that Fake.ServeAgent serves.
func (l *Lifecycle) AgentDialer() platform.AgentDialer {
	return &agentDialer{fake: l.fake}
}

// agentDialer connects to the fake machine's agents through in-memory pipes.
type agentDialer struct {
	fake *Fake
}

// Dial hands one end of a pipe to the VM's agent and returns the other.
func (a *agentDialer) Dial(ctx context.Context, vm string, port uint32) (net.Conn, error) {
	if err := a.fake.enter(ctx, "Dial", vm, port); err != nil {
		return nil, err
	}
	a.fake.mu.Lock()
	serve, found := a.fake.agents[vm]
	a.fake.mu.Unlock()
	if !found {
		return nil, fmt.Errorf("agent of VM %q: %w", vm, platform.ErrNotFound)
	}
	ours, theirs := net.Pipe()
	go serve(theirs)
	return ours, nil
}
