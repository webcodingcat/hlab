package system

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/agentproto"
	"github.com/webcodingcat/hlab/internal/platform"
)

// ApplianceVM returns the name of the appliance's VM with a MAC set ("A"
// or "B"): during an update the old and the new appliance run side by side.
func ApplianceVM(macSet string) string {
	return "hlab-appliance-" + strings.ToLower(macSet)
}

// The appliance's size: 371 MB were in use under load, with 20 servers, 10
// firewalls and HAProxy (measured on Hyper-V). The room check keeps
// ApplianceMemoryMB free for it.
const (
	applianceCPUs     = 2
	ApplianceMemoryMB = 1024
)

// ApplianceSpec is what the appliance is made of.
type ApplianceSpec struct {
	// VM is the name of its VM (ApplianceVM).
	VM string
	// Release is the disk of the appliance's release in the data folder
	// (StageApplianceRelease); it stays as it is.
	Release string
	// SystemDisk is the appliance's own disk, a differencing disk on Release.
	// Each start that does not take a running appliance over makes it anew:
	// the appliance keeps nothing of its own.
	SystemDisk string
	// MACs are the MAC addresses of its NICs, in their order: link, public,
	// private, uplink.
	MACs [4]string
}

// ApplianceTimings are the watchdog's times; tests take shorter ones than
// DefaultApplianceTimings.
type ApplianceTimings struct {
	// Interval is the time between two checks.
	Interval time.Duration
	// Failures is the number of failed checks in a row after which the
	// watchdog restarts the appliance.
	Failures int
	// Boot is how long a start may take until the agent answers.
	Boot time.Duration
	// Shutdown is how long the guest may take to shut down before it is
	// turned off.
	Shutdown time.Duration
}

// DefaultApplianceTimings returns the watchdog's times: a check every 2 s,
// a restart after three failures (the appliance is down for 6 s then), or
// after one that finds the appliance off; the agent answered 5.1 s after
// the start.
func DefaultApplianceTimings() ApplianceTimings {
	return ApplianceTimings{Interval: 2 * time.Second, Failures: 3, Boot: 2 * time.Minute, Shutdown: 30 * time.Second}
}

// Pictures is the whole data plane: every picture of the agent.
type Pictures struct {
	Links    agentproto.Links
	Ruleset  agentproto.Ruleset
	DHCP     agentproto.DHCP
	Metadata agentproto.Metadata
}

// namedPicture is a picture with its name, as the agent's route takes it.
type namedPicture struct {
	name       string
	generation uint64
	body       any
}

// inOrder returns the pictures in the order a fresh agent applies them:
// links first, because the others name its interfaces and addresses.
func (p Pictures) inOrder() []namedPicture {
	return []namedPicture{
		{agentproto.PictureLinks, p.Links.Generation, p.Links},
		{agentproto.PictureRuleset, p.Ruleset.Generation, p.Ruleset},
		{agentproto.PictureDHCP, p.DHCP.Generation, p.DHCP},
		{agentproto.PictureMetadata, p.Metadata.Generation, p.Metadata},
	}
}

// Appliance runs the appliance: it starts it or takes it over, sends it the
// pictures, and its watchdog restarts it when it fails.
type Appliance struct {
	system  *System
	spec    ApplianceSpec
	timings ApplianceTimings
	agent   agentClient
	logger  *slog.Logger

	mu       sync.Mutex
	pictures Pictures          // the newest; sent again after every restart
	refused  map[string]uint64 // generations the agent refused, by picture

	stopWatchdog context.CancelFunc
	watchdog     sync.WaitGroup
}

// NewAppliance returns the appliance of the spec; it does not start it yet.
func (s *System) NewAppliance(spec ApplianceSpec, timings ApplianceTimings, logger *slog.Logger) *Appliance {
	return &Appliance{
		system:  s,
		spec:    spec,
		timings: timings,
		agent:   newAgentClient(s.platform.Lifecycle.AgentDialer(), spec.VM),
		logger:  logger,
		refused: map[string]uint64{},
	}
}

// Start takes over the appliance if it runs and its agent answers, as after
// a crash of hlabd; otherwise it starts it anew. Then it sends every
// picture and starts the watchdog. It reports whether it took the
// appliance over: that one may run an older release.
func (a *Appliance) Start(ctx context.Context, pictures Pictures) (takenOver bool, err error) {
	a.mu.Lock()
	a.pictures = pictures
	a.mu.Unlock()
	started := time.Now()
	takenOver = a.answers(ctx)
	if takenOver {
		a.logger.Info("taking over the running appliance", "vm", a.spec.VM)
	} else {
		if err := a.create(ctx); err != nil {
			return false, err
		}
		if err := a.waitForAgent(ctx); err != nil {
			return false, err
		}
		a.logger.Info("the appliance's agent answers", "vm", a.spec.VM, "took", time.Since(started).Round(time.Millisecond))
	}
	if err := a.sendAll(ctx); err != nil {
		return takenOver, err
	}
	a.logger.Info("the appliance is ready", "vm", a.spec.VM, "took", time.Since(started).Round(time.Millisecond))
	watchdogCtx, cancel := context.WithCancel(context.Background())
	a.stopWatchdog = cancel
	a.watchdog.Go(func() { a.watch(watchdogCtx) })
	return takenOver, nil
}

// Update makes pictures the newest and sends them all, in order. If
// sending fails, the watchdog sends them again on its next check: the agent
// reports the generations it has.
func (a *Appliance) Update(ctx context.Context, pictures Pictures) error {
	a.mu.Lock()
	a.pictures = pictures
	a.mu.Unlock()
	return a.sendAll(ctx)
}

// Announce sends the links picture again, after which the agent announces
// the appliance's addresses. After an update the old appliance answered
// too while both ran, so some neighbors may still hold its MAC addresses.
func (a *Appliance) Announce(ctx context.Context) error {
	a.mu.Lock()
	links := a.pictures.inOrder()[0]
	a.mu.Unlock()
	return a.send(ctx, links)
}

// Remove ends the watchdog and deletes the appliance's VM, turned off at
// once, and its system disk: the old appliance after an update, or a new
// one whose start failed.
func (a *Appliance) Remove(ctx context.Context) error {
	a.endWatchdog()
	hypervisor := a.system.platform.Hypervisor
	if err := hypervisor.DeleteVM(ctx, a.spec.VM); err != nil && !errors.Is(err, platform.ErrNotFound) {
		return fmt.Errorf("removing the appliance %s: %w", a.spec.VM, err)
	}
	if err := hypervisor.DeleteDisk(ctx, a.spec.SystemDisk); err != nil && !errors.Is(err, platform.ErrNotFound) {
		return fmt.Errorf("removing the disk of the appliance %s: %w", a.spec.VM, err)
	}
	return nil
}

// endWatchdog stops the watchdog, if it runs, and waits until it is done.
func (a *Appliance) endWatchdog() {
	if a.stopWatchdog != nil {
		a.stopWatchdog()
		a.watchdog.Wait()
		a.stopWatchdog = nil
	}
	a.agent.close()
}

// Stop ends the watchdog and shuts the appliance down: the guest first,
// turned off after the deadline. The VM stays, for the next start.
func (a *Appliance) Stop(ctx context.Context) error {
	a.endWatchdog()
	hypervisor := a.system.platform.Hypervisor
	if err := hypervisor.ShutdownVM(ctx, a.spec.VM); err != nil {
		if errors.Is(err, platform.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("shutting the appliance down: %w", err)
	}
	started := time.Now()
	deadline := started.Add(a.timings.Shutdown)
	for time.Now().Before(deadline) {
		state, err := hypervisor.VMState(ctx, a.spec.VM)
		if err == nil && state.Power == platform.PowerOff {
			a.logger.Info("the appliance shut down", "took", time.Since(started).Round(time.Millisecond))
			return nil
		}
		if err := sleep(ctx, a.timings.Shutdown/60); err != nil {
			break
		}
	}
	a.logger.Warn("the appliance did not shut down in time; turning it off", "deadline", a.timings.Shutdown)
	return hypervisor.StopVM(context.WithoutCancel(ctx), a.spec.VM)
}

// answers tells whether the appliance runs and its agent answers.
func (a *Appliance) answers(ctx context.Context) bool {
	state, err := a.system.platform.Hypervisor.VMState(ctx, a.spec.VM)
	if err != nil || state.Power != platform.PowerRunning {
		return false
	}
	_, err = a.agent.status(ctx)
	return err == nil
}

// create makes the appliance anew and starts it: a VM that is there goes,
// with its system disk; the new system disk is a differencing disk on the
// release.
func (a *Appliance) create(ctx context.Context) error {
	hypervisor := a.system.platform.Hypervisor
	if err := hypervisor.DeleteVM(ctx, a.spec.VM); err != nil && !errors.Is(err, platform.ErrNotFound) {
		return fmt.Errorf("removing the old appliance: %w", err)
	}
	if err := hypervisor.DeleteDisk(ctx, a.spec.SystemDisk); err != nil && !errors.Is(err, platform.ErrNotFound) {
		return fmt.Errorf("removing the appliance's old disk: %w", err)
	}
	if err := hypervisor.CreateDisk(ctx, a.spec.SystemDisk, 0, a.spec.Release); err != nil {
		return fmt.Errorf("creating the appliance's disk on %s: %w", a.spec.Release, err)
	}
	spec := platform.VMSpec{
		Name:     a.spec.VM,
		Tag:      VMTag,
		CPUs:     applianceCPUs,
		MemoryMB: ApplianceMemoryMB,
		Disks:    []platform.DiskSlot{{Slot: 0, Path: a.spec.SystemDisk}},
		// the private networks' switch comes with phase 2a; until then the
		// trunk is not connected
		NICs: []platform.NICSpec{
			{MAC: a.spec.MACs[0], Switch: LinkSwitch, Port: platform.Plain()},
			{MAC: a.spec.MACs[1], Switch: PublicSwitch, Port: platform.Promiscuous()},
			{MAC: a.spec.MACs[2], Port: platform.Trunk()},
			{MAC: a.spec.MACs[3], Switch: UplinkSwitch, Port: platform.Plain()},
		},
	}
	if err := hypervisor.CreateVM(ctx, spec); err != nil {
		return fmt.Errorf("creating the appliance: %w", err)
	}
	if err := hypervisor.StartVM(ctx, a.spec.VM); err != nil {
		return fmt.Errorf("starting the appliance: %w", err)
	}
	return nil
}

// restart turns the appliance off and on again with the same MAC set, or
// makes it anew if its VM is gone.
func (a *Appliance) restart(ctx context.Context) error {
	hypervisor := a.system.platform.Hypervisor
	err := hypervisor.StopVM(ctx, a.spec.VM)
	if err == nil {
		err = hypervisor.StartVM(ctx, a.spec.VM)
	}
	if errors.Is(err, platform.ErrNotFound) {
		err = a.create(ctx)
	}
	if err != nil {
		return err
	}
	if err := a.waitForAgent(ctx); err != nil {
		return err
	}
	return a.sendAll(ctx)
}

// agentAttempt bounds one try to reach a booting agent: hv_sock holds a
// connection to a VM whose agent does not listen yet for seconds before it
// fails, while one that listens answers within a millisecond.
const agentAttempt = time.Second

// waitForAgent waits until the agent answers, at most Timings.Boot.
func (a *Appliance) waitForAgent(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, a.timings.Boot)
	defer cancel()
	for {
		attemptCtx, cancelAttempt := context.WithTimeout(ctx, agentAttempt)
		_, err := a.agent.status(attemptCtx)
		cancelAttempt()
		if err == nil {
			return nil
		}
		if sleep(ctx, a.timings.Boot/500) != nil {
			return fmt.Errorf("the appliance's agent did not answer within %s: %w", a.timings.Boot, err)
		}
	}
}

// sendAll sends every picture, in order.
func (a *Appliance) sendAll(ctx context.Context) error {
	a.mu.Lock()
	pictures := a.pictures.inOrder()
	a.mu.Unlock()
	for _, picture := range pictures {
		if err := a.send(ctx, picture); err != nil {
			return err
		}
	}
	return nil
}

// send sends one picture. A picture the agent refused is not sent again:
// the next generation may pass.
func (a *Appliance) send(ctx context.Context, picture namedPicture) error {
	err := a.agent.put(ctx, picture.name, picture.body)
	if errors.Is(err, errRefused) {
		a.mu.Lock()
		a.refused[picture.name] = picture.generation
		a.mu.Unlock()
		a.logger.Error("the agent refused a picture", "picture", picture.name, "generation", picture.generation, "error", err)
		return nil
	}
	if err != nil {
		return fmt.Errorf("sending the picture %s: %w", picture.name, err)
	}
	return nil
}

// errOff is a check that found the appliance's VM off or gone: it is dead
// for sure, so the watchdog restarts it at once.
var errOff = errors.New("the appliance is not running")

// watch checks the appliance every interval and restarts it after too many
// failures in a row, or at once when it is off, until ctx ends.
func (a *Appliance) watch(ctx context.Context) {
	failures := 0
	for sleep(ctx, a.timings.Interval) == nil {
		err := a.check(ctx)
		if err == nil {
			failures = 0
			continue
		}
		if ctx.Err() != nil {
			return
		}
		failures++
		a.logger.Warn("the appliance failed a check", "failures", failures, "error", err)
		// a missed heartbeat or ping may pass; an appliance that is off does
		// not come back by itself
		if failures < a.timings.Failures && !errors.Is(err, errOff) {
			continue
		}
		a.logger.Error("restarting the appliance")
		started := time.Now()
		if err := a.restart(ctx); err != nil {
			a.logger.Error("restarting the appliance failed", "error", err)
			continue // failures stays: the next failed check tries again
		}
		failures = 0
		a.logger.Info("the appliance is back", "took", time.Since(started).Round(time.Millisecond))
	}
}

// check fails unless the VM runs, its heartbeat answers and its agent
// answers. It sends the pictures the agent lacks, as after a restart of the
// agent alone.
func (a *Appliance) check(ctx context.Context) error {
	state, err := a.system.platform.Hypervisor.VMState(ctx, a.spec.VM)
	switch {
	case errors.Is(err, platform.ErrNotFound):
		return fmt.Errorf("%w: its VM is gone", errOff)
	case err != nil:
		return err
	case state.Power == platform.PowerOff:
		return errOff
	case state.Power != platform.PowerRunning:
		return fmt.Errorf("the appliance is %s", state.Power)
	case !state.Heartbeat:
		return errors.New("the appliance's heartbeat does not answer")
	}
	pingCtx, cancel := context.WithTimeout(ctx, a.timings.Interval)
	defer cancel()
	status, err := a.agent.status(pingCtx)
	if err != nil {
		return fmt.Errorf("the agent does not answer: %w", err)
	}
	a.mu.Lock()
	pictures := a.pictures.inOrder()
	a.mu.Unlock()
	for _, picture := range pictures {
		a.mu.Lock()
		generation, found := a.refused[picture.name]
		a.mu.Unlock()
		refused := found && generation == picture.generation
		if status.Generations[picture.name] == picture.generation || refused {
			continue
		}
		if err := a.send(ctx, picture); err != nil {
			a.logger.Warn("sending a picture again failed", "picture", picture.name, "error", err)
		}
	}
	return nil
}

// sleep waits d or until ctx ends, and then returns ctx's error.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
