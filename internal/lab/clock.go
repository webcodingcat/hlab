package lab

import (
	"context"
	"time"
)

// Hetzner's timings, measured on Hetzner Cloud. Actions wait them through
// Sleep, which scales them by the time scale. For example, after a power off
// the server shows off after ~9 s and the action ends ~1 s later; after a reset
// or reboot the server runs again after ~15 s.
const (
	ServerCreate            = 4 * time.Second         // a new server: initializing → its create action ends (measured 2026-11-08)
	ServerCreateStart       = 5 * time.Second         // a new server: starting → running
	PrimaryIPChange         = 3 * time.Second         // a Primary IP's assign or unassign: running → the action ends (measured 2026-11-08: over 3 s)
	FloatingIPChange        = 3 * time.Second         // a Floating IP's assign or unassign: running → the action ends (measured 2026-11-08: 3 to 4 s)
	FirewallChange          = 3 * time.Second         // a firewall's apply or remove on a server: running → the action ends (measured 2026-11-08: 2.9 to 3.5 s)
	FirewallJoin            = 2 * time.Second         // a label selector takes a server: its apply_firewall appears, and the server shows the firewall pending; before, not at all (measured 2026-11-09: 1.97 s)
	FirewallJoined          = 14 * time.Second        // and that apply_firewall runs so long (measured 2026-11-09: 14 s; 2026-11-08: applied 3 to 9 s after the relabel)
	FirewallLeave           = 1700 * time.Millisecond // a relabel takes a server out of a selector: its remove_firewall appears, and the server shows the firewall pending; before, still applied (measured 2026-11-09: 1.66 s)
	FirewallLeft            = 8 * time.Second         // and that remove_firewall runs so long, then the firewall is gone from the server (measured 2026-11-09: 8 s)
	FirewallSelector        = 1 * time.Second         // a firewall's apply or remove on a label selector: running → the action ends (measured 2026-11-08: 0.6 to 1.2 s)
	ServerDelete            = 2 * time.Second         // the server's VM is gone → its delete action ends
	PowerOff                = 8700 * time.Millisecond // power off: stopping → off
	PowerOffEnd             = 1200 * time.Millisecond // power off: off → the action ends
	Shutdown                = 1 * time.Second         // shutdown: the power button is pressed → the action ends
	PowerOn                 = 7 * time.Second         // power on: starting → running
	PowerOnEnd              = 1 * time.Second         // power on: running → the action ends
	Restart                 = 13 * time.Second        // reboot or reset: starting → running
	RestartEnd              = 2 * time.Second         // reboot or reset: running → the action ends
	Rebuild                 = 10 * time.Second        // rebuild: the new disk is there → the action ends
	ChangeType              = 30 * time.Second        // change_type: the new type is set → the action ends
	NICAttach               = 3 * time.Second         // attach_to_network: the NIC is in → the action ends (measured 2026-11-08: 1.8 to 7.6 s, 2.4 s on a running server)
	NICDetachOff            = 4 * time.Second         // detach_from_network on a server that is off: the NIC is out → the action ends (measured 2026-11-08: 4.1 to 5.3 s; on a running server it ends at once)
	AliasIPs                = 6 * time.Second         // change_alias_ips: routed → the action ends (measured 2026-11-08: 5.8 to 8.1 s)
	MemberAttachingAtCreate = 1200 * time.Millisecond // a server that joins a network in its create: shown as "attaching" among its members, then "ok" while its attach still runs (measured 2026-11-09: 1.2 s)
	MemberDetaching         = 5 * time.Second         // a server that left a network: shown as "detaching" among its members (measured 2026-11-08: 5.3 s after a detach, also after a delete of the server)
	MemberUpdating          = 2 * time.Second         // another server joins or leaves a network: the members show "updating" after this (measured 2026-11-08: 1.8 to 2.9 s; 2026-11-09: 3.5 s)
	MemberUpdated           = 10 * time.Second        // and as long as this (measured 2026-11-09: from 3.5 s to 13.9 s after an attach; 2026-11-08: still so 6 s after a delete)
	NetworkRoutes           = 5 * time.Second         // add_route, delete_route with servers in the network: routed → the action ends (measured 2026-11-08: 4.6 to 6.4 s; without servers at once)
	VolumeChange            = 2 * time.Second         // a volume's create, attach, detach, resize: done → the action ends
	FloatingIPMove          = 3 * time.Second         // a Floating IP's route moves to its new server
	BoxCreate               = 5 * time.Second         // a Storage Box: its account is set up → active
)

// Clock tells the time and waits. The real one is the system clock; the tests
// use one they move forward by hand.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// realClock is the system clock.
type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Sleep waits as long as Hetzner takes for something (one of the timings
// above), scaled by the time scale. It returns early with the context's error
// when the action is canceled.
func (l *Lab) Sleep(ctx context.Context, hetzner time.Duration) error {
	wait := l.scaled(hetzner)
	if wait <= 0 {
		return ctx.Err()
	}
	select {
	case <-l.clock.After(wait):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
