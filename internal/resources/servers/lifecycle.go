package servers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/store"
)

var _ resource.Lifecycle = (*Module)(nil)

// defaultHaltTimeout bounds the wait for the guests to shut down at cloud
// stop when Deps.HaltTimeout is 0.
const defaultHaltTimeout = 60 * time.Second

// haltPoll is how often Halt reads the states of VMs whose guests were
// asked to shut down.
const haltPoll = 100 * time.Millisecond

// Halt implements resource.Lifecycle. Running VMs are saved one after
// another ("save"; a failed save falls back to a guest shutdown and a hard
// off) or all asked to shut down at once and turned off hard after the
// deadline ("shutdown"). Their servers get status running, servers whose
// VM is off get off.
func (m *Module) Halt(ctx context.Context, behaviour string, hostShutdown bool) error {
	if hostShutdown {
		return nil
	}
	recs, err := m.records()
	if err != nil {
		return err
	}
	var running []string
	var errs []error
	for _, r := range recs {
		name := m.vmName(r.ID)
		st, err := m.d.Platform.State(ctx, name)
		if err != nil {
			if !isNotFound(err) {
				slog.Error("read VM state at halt", "id", r.ID, "err", err)
			}
			continue
		}
		var status string
		switch st {
		case platform.StateRunning:
			status = StatusRunning
			running = append(running, name)
		case platform.StateOff:
			status = StatusOff
		default:
			continue
		}
		if r.Status != status {
			if err := m.setStatus(m.d.Store, r.ID, status); err != nil {
				errs = append(errs, fmt.Errorf("set status of server %d: %w", r.ID, err))
			}
		}
	}

	if behaviour == "shutdown" {
		errs = append(errs, m.shutdownAll(ctx, running))
	} else {
		for _, name := range running {
			if err := m.d.Platform.Power(ctx, name, platform.PowerSave); err != nil {
				slog.Warn("save VM at halt failed, shutting it down", "vm", name, "err", err)
				errs = append(errs, m.shutdownAll(ctx, []string{name}))
			}
		}
	}
	return errors.Join(errs...)
}

// shutdownAll asks the guests of names to shut down, waits until all VMs
// are off or the halt timeout has passed, then turns off the rest hard.
func (m *Module) shutdownAll(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return nil
	}
	for _, name := range names {
		if err := m.d.Platform.Power(ctx, name, platform.PowerShutdown); err != nil {
			slog.Warn("ask guest to shut down at halt", "vm", name, "err", err)
		}
	}
	timeout := m.d.HaltTimeout
	if timeout == 0 {
		timeout = defaultHaltTimeout
	}
	deadline := m.d.Clock.After(timeout)
	tick := time.NewTicker(haltPoll)
	defer tick.Stop()
	left := names
	for {
		left = m.notOff(ctx, left)
		if len(left) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			var errs []error
			for _, name := range left {
				slog.Warn("guest did not shut down at halt, turning the VM off", "vm", name)
				if err := m.d.Platform.Power(ctx, name, platform.PowerStop); err != nil {
					errs = append(errs, fmt.Errorf("turn off VM %s: %w", name, err))
				}
			}
			return errors.Join(errs...)
		case <-tick.C:
		}
	}
}

// notOff returns the VMs of names that are not off; a VM whose state
// cannot be read counts as not off.
func (m *Module) notOff(ctx context.Context, names []string) []string {
	var out []string
	for _, name := range names {
		if st, err := m.d.Platform.State(ctx, name); err != nil || st != platform.StateOff {
			out = append(out, name)
		}
	}
	return out
}

// Restore implements resource.Lifecycle. Hyper-V refuses a changed stop
// action on a running VM, so it is set on Off and Saved VMs only.
func (m *Module) Restore(ctx context.Context, behaviour string) error {
	recs, err := m.records()
	if err != nil {
		return err
	}
	action := platform.StopSave
	if behaviour == "shutdown" {
		action = platform.StopShutdown
	}
	for _, r := range recs {
		name := m.vmName(r.ID)
		st, err := m.d.Platform.State(ctx, name)
		if err != nil {
			if !isNotFound(err) {
				slog.Error("read VM state at restore", "id", r.ID, "err", err)
			}
			continue
		}
		if st != platform.StateOff && st != platform.StateSaved {
			continue
		}
		if err := m.d.Platform.SetStopAction(ctx, name, action); err != nil {
			slog.Warn("set VM stop action at restore", "vm", name, "err", err)
		}
		if r.Status != StatusRunning && r.Status != StatusStarting {
			continue
		}
		if err := m.bootPower(ctx, m.d.Store, r.ID, platform.PowerStart); err != nil {
			slog.Error("start server at restore", "id", r.ID, "err", err)
			continue
		}
		if err := m.setStatus(m.d.Store, r.ID, StatusRunning); err != nil {
			slog.Error("set server status at restore", "id", r.ID, "err", err)
		}
	}
	return nil
}

// records lists all servers of all projects.
func (m *Module) records() ([]Record, error) {
	var recs []Record
	err := m.d.Store.Tx(func(tx store.Tx) error {
		var err error
		recs, err = store.ListAll[Record](tx, Kind)
		return err
	})
	return recs, err
}
