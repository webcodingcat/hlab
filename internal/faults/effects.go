package faults

import "context"

// ServerInjector is implemented by the servers module.
type ServerInjector interface {
	Crash(ctx context.Context, project string, id int64) error
	// Freeze is an *InputError "server is not running" unless the VM is
	// Running.
	Freeze(ctx context.Context, project string, id int64) error
	// Thaw resumes only a Paused VM.
	Thaw(ctx context.Context, project string, id int64) error
	// SetSystemDiskIOPS limits the server's system disk; 0 removes.
	SetSystemDiskIOPS(ctx context.Context, project string, id int64, max int) error
}

// VolumeInjector is implemented by the volumes module.
type VolumeInjector interface {
	Remove(ctx context.Context, project string, id int64) error
	Restore(ctx context.Context, project string, id int64) error
	// SetIOPS limits the volume's disk; 0 removes.
	SetIOPS(ctx context.Context, project string, id int64, max int) error
}

// Effects returns the effects of server_crash, server_freeze,
// volume_unavailable and disk_slow over s and v, for Options.Effects.
func Effects(s ServerInjector, v VolumeInjector) map[string]Effect {
	return map[string]Effect{
		// A crash has nothing to undo: the block ends with the listing.
		KindServerCrash:       effect{apply: s.Crash, undo: nothing},
		KindServerFreeze:      effect{apply: s.Freeze, undo: s.Thaw},
		KindVolumeUnavailable: effect{apply: v.Remove, undo: v.Restore},
		KindDiskSlow:          diskSlow{s: s, v: v},
	}
}

// targetCall is an injector call on a fault's target.
type targetCall func(ctx context.Context, project string, id int64) error

func nothing(context.Context, string, int64) error { return nil }

// effect runs apply and undo on the fault's target.
type effect struct{ apply, undo targetCall }

func (e effect) Apply(ctx context.Context, f Fault) error {
	return e.apply(ctx, f.Project, f.Target.ID)
}

func (e effect) Undo(ctx context.Context, f Fault) error {
	return e.undo(ctx, f.Project, f.Target.ID)
}

// diskSlow sets the IOPS limit of a volume or a server's system disk.
type diskSlow struct {
	s ServerInjector
	v VolumeInjector
}

func (d diskSlow) Apply(ctx context.Context, f Fault) error {
	n, _ := toInt64(f.Params["iops"])
	return d.set(ctx, f, int(n))
}

func (d diskSlow) Undo(ctx context.Context, f Fault) error { return d.set(ctx, f, 0) }

// set limits the server's system disk or the volume; validation allows
// only these two target types for disk_slow.
func (d diskSlow) set(ctx context.Context, f Fault, max int) error {
	if f.Target.Type == TargetServer {
		return d.s.SetSystemDiskIOPS(ctx, f.Project, f.Target.ID, max)
	}
	return d.v.SetIOPS(ctx, f.Project, f.Target.ID, max)
}
