// Package fake is a platform in memory, for the unit tests and for
// `hlabd --platform fake`. It behaves as the contract tests
// (internal/platform/contract) say a platform behaves, so that code tested
// against it works on Hyper-V too. Tests can make calls fail or wait, and read
// the calls it got.
package fake

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/platform"
)

// Fake is one machine: its VMs, disks, host link, DNS rules and life cycle.
// Its four parts share it, as the parts of a real platform share the machine.
type Fake struct {
	mu       sync.Mutex
	calls    []string
	failures map[string][]error
	delays   map[string]time.Duration

	vms   map[string]*vm
	disks map[string]*disk
	seeds map[string]map[string][]byte

	usedRanges []netip.Prefix
	link       *platform.LinkSpec
	dnsRules   []platform.DNSRule

	locked   map[string]bool
	stop     chan struct{}
	stopOnce sync.Once
	agents   map[string]func(conn net.Conn)
}

// New returns an empty machine.
func New() *Fake {
	return &Fake{
		failures: map[string][]error{},
		delays:   map[string]time.Duration{},
		vms:      map[string]*vm{},
		disks:    map[string]*disk{},
		seeds:    map[string]map[string][]byte{},
		locked:   map[string]bool{},
		stop:     make(chan struct{}),
		agents:   map[string]func(conn net.Conn){},
	}
}

// Platform returns the four parts of the machine.
func (f *Fake) Platform() platform.Platform {
	return platform.Platform{
		Hypervisor: &Hypervisor{fake: f},
		HostNet:    &HostNet{fake: f},
		HostDNS:    &HostDNS{fake: f},
		Lifecycle:  &Lifecycle{fake: f},
	}
}

// FailNext makes the next call of a method (such as "StartVM") fail with err.
// Called n times, the next n calls fail, in order.
func (f *Fake) FailNext(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[method] = append(f.failures[method], err)
}

// Delay makes every call of a method wait d first, or until its context ends.
func (f *Fake) Delay(method string, d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delays[method] = d
}

// Calls returns the calls so far, each as "Method arg ...".
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// Seed returns the files of the seed that CreateSeed wrote to path.
func (f *Fake) Seed(path string) map[string][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seeds[path]
}

// enter records a call, waits its delay and returns its injected failure.
// Every method of the four parts starts with it.
func (f *Fake) enter(ctx context.Context, method string, args ...any) error {
	f.mu.Lock()
	words := []string{method}
	for _, arg := range args {
		words = append(words, fmt.Sprint(arg))
	}
	f.calls = append(f.calls, strings.Join(words, " "))
	var failure error
	if queued := f.failures[method]; len(queued) > 0 {
		failure, f.failures[method] = queued[0], queued[1:]
	}
	delay := f.delays[method]
	f.mu.Unlock()

	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if failure != nil {
		return failure
	}
	return ctx.Err()
}
