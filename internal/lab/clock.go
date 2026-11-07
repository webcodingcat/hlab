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
	ServerCreateStart = 5 * time.Second         // a new server: starting → running
	ServerDelete      = 2 * time.Second         // the server's VM is gone → its delete action ends
	PowerOff          = 8700 * time.Millisecond // power off: stopping → off
	PowerOffEnd       = 1200 * time.Millisecond // power off: off → the action ends
	Shutdown          = 1 * time.Second         // shutdown: the power button is pressed → the action ends
	PowerOn           = 7 * time.Second         // power on: starting → running
	PowerOnEnd        = 1 * time.Second         // power on: running → the action ends
	Restart           = 13 * time.Second        // reboot or reset: starting → running
	RestartEnd        = 2 * time.Second         // reboot or reset: running → the action ends
	Rebuild           = 10 * time.Second        // rebuild: the new disk is there → the action ends
	ChangeType        = 30 * time.Second        // change_type: the new type is set → the action ends
	NICAttach         = 3 * time.Second         // attach_to_network: the NIC is in → the action ends
	NICDetach         = 2 * time.Second         // detach_from_network: the NIC is out → the action ends
	AliasIPs          = 2 * time.Second         // change_alias_ips: routed → the action ends
	VolumeChange      = 2 * time.Second         // a volume's create, attach, detach, resize: done → the action ends
	FloatingIPMove    = 3 * time.Second         // a Floating IP's route moves to its new server
	BoxCreate         = 5 * time.Second         // a Storage Box: its account is set up → active
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
	wait := time.Duration(float64(hetzner) * l.timeScale)
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
