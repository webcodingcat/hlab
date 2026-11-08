package lab

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"time"

	"github.com/webcodingcat/hlab/internal/picture"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/system"
)

// ApplianceDir is the folder of the appliance's disks in the data folder.
const ApplianceDir = "appliance"

// machineStopTimeout bounds stopping the machine's side of the lab: the
// appliance's shutdown (30 s) and removing the host link.
const machineStopTimeout = 2 * time.Minute

// MachineOptions are what the lab needs on the machine.
type MachineOptions struct {
	// ApplianceRelease is the appliance's release disk (bin/dev appliance).
	ApplianceRelease string
	// Timings are the appliance watchdog's; zero takes the defaults.
	Timings system.ApplianceTimings
	// ServerShutdown is how long the servers' guests may take to shut down
	// when the lab stops, before they are turned off; zero takes 60 s.
	ServerShutdown time.Duration
}

// defaultServerShutdown is MachineOptions.ServerShutdown's default.
const defaultServerShutdown = time.Minute

// StartMachine starts the lab's side on the machine: it checks the
// address ranges and connects the host, removes the VMs that an earlier run
// left, starts or takes over the appliance, sends it the pictures and sets
// the host's DNS rules. The service calls it after Open and before it
// listens on the host link; Close stops it again. If a step fails, the host
// link is removed again.
func (l *Lab) StartMachine(ctx context.Context, opts MachineOptions) error {
	if opts.Timings == (system.ApplianceTimings{}) {
		opts.Timings = system.DefaultApplianceTimings()
	}
	if opts.ServerShutdown == 0 {
		opts.ServerShutdown = defaultServerShutdown
	}
	host, err := l.system.HostResources(ctx)
	if err != nil {
		return err
	}
	facts := l.basePictureLab()
	link := system.HostLink{
		Ranges:    []netip.Prefix{facts.Range4, facts.Range6},
		Addresses: facts.HostAddresses(),
		Gateways:  facts.ApplianceAddresses(),
	}
	if err := l.system.ConnectHost(ctx, link); err != nil {
		return err
	}
	appliance, err := l.startAppliance(ctx, opts)
	if err != nil {
		if disconnectErr := l.system.DisconnectHost(context.WithoutCancel(ctx)); disconnectErr != nil {
			l.logger.Warn("removing the host link after a failed start", "error", disconnectErr)
		}
		return err
	}
	l.mu.Lock()
	l.appliance = appliance
	l.host = host
	l.serverShutdown = opts.ServerShutdown
	l.mu.Unlock()
	if err := l.resumeServers(ctx); err != nil {
		return errors.Join(err, l.stopMachine())
	}
	watchCtx, cancel := context.WithCancel(context.Background())
	l.mu.Lock()
	l.watching = cancel
	l.mu.Unlock()
	l.watchers.Go(func() { l.watchServers(watchCtx) })
	return nil
}

// resumeServers brings the servers back after the lab started: a
// VM that runs is taken over (hlabd had crashed); one that is off gets its
// NIC on the public switch again, and starts if its server wants to run.
// A server without a VM gets one (restoreServer).
// The servers' statuses follow their VMs, also for actions that HLab's end
// cut short; a server that cannot be brought back stays off. Then the
// image cache loses what no server needs. It gets the servers' records
// once and reads them only, so keeping them across the calls to the
// system is safe: a server deleted meanwhile is skipped where its status
// is set.
func (l *Lab) resumeServers(ctx context.Context) error {
	var servers []*state.Server
	_ = l.View(func(tx *state.Tx) error {
		servers = state.All(tx, state.Servers)
		return nil
	})
	for _, server := range servers {
		err := l.resumeServer(ctx, server)
		if ctx.Err() != nil {
			return ctx.Err() // the start was called off
		}
		if err == nil {
			continue
		}
		// one server's trouble (a broken disk, an image that does not
		// load) must not keep the lab and the other servers from running;
		// it wants to run still, so the next start tries again
		l.logger.Error("a server could not be brought back; it stays off", "server", server.ID, "error", err)
		if err := l.SetServerStatus(server.ID, state.ServerOff); err != nil && !errors.Is(err, state.ErrNotFound) {
			l.logger.Warn("setting the server off", "server", server.ID, "error", err)
		}
	}
	l.cleanImageCache(ctx)
	return nil
}

// resumeServer brings one server back; it gets the server's record as it
// was when the start began.
func (l *Lab) resumeServer(ctx context.Context, server *state.Server) error {
	vm := ServerVM(server.ID)
	exists, err := l.system.ServerExists(ctx, vm)
	if err != nil {
		return err
	}
	if !exists {
		if err := l.restoreServer(ctx, server); err != nil {
			return err
		}
	}
	runs, err := l.system.ServerRuns(ctx, vm)
	if err != nil {
		return err
	}
	status := state.ServerRunning
	if !runs {
		if exists {
			if err := l.system.ReconnectServer(ctx, vm, server.MAC); err != nil {
				return err
			}
		}
		status = state.ServerOff
		if server.WantRunning {
			if err := l.system.PowerServer(ctx, vm, system.PowerOn); err != nil {
				return err
			}
			status = state.ServerRunning
		}
	}
	if err := l.SetServerStatus(server.ID, status); err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	return nil
}

// restoreServer makes a VM for a server that has none. If its disk is
// there (an uninstall kept the data but removed the VMs), the new VM gets
// it as it is. Without a disk, as when HLab ended during the create, the
// server is built anew; so is one whose base image is gone.
func (l *Lab) restoreServer(ctx context.Context, server *state.Server) error {
	if server.BaseImage != "" {
		kept, err := l.makeServerVM(ctx, server.ID)
		switch {
		case kept && err != nil:
			return err // never build anew over the server's own disk
		case kept:
			l.logger.Info("a server had no VM; it has a new one with its disk", "server", server.ID)
			return nil
		case err == nil:
			l.logger.Warn("a server had no VM and no disk; it has a new disk on its image", "server", server.ID)
			return nil
		}
		l.logger.Warn("a server without a VM and a disk cannot get one on its image", "server", server.ID, "error", err)
	}
	l.logger.Warn("a server has no VM, as HLab ended during its create; building it anew", "server", server.ID)
	disk, seedISO := l.serverFiles(server.ID)
	if err := l.system.DeleteServer(ctx, ServerVM(server.ID), disk, seedISO); err != nil {
		return err // what the cut-short create left
	}
	return l.BuildServer(ctx, server.ID)
}

// cleanImageCache removes the cached images that neither a cache entry nor
// a server's disk needs, and what loads cut short left. A failure only
// costs disk space, so it is logged.
func (l *Lab) cleanImageCache(ctx context.Context) {
	var keep []string
	_ = l.View(func(tx *state.Tx) error {
		for _, cached := range tx.Lab().Images {
			keep = append(keep, cached.File)
		}
		for _, server := range state.All(tx, state.Servers) {
			keep = append(keep, server.BaseImage)
		}
		return nil
	})
	removed, err := l.system.CleanDir(ctx, filepath.Join(l.dataDir, ImageCacheDir), keep)
	if len(removed) > 0 {
		l.logger.Info("removed from the image cache what no server needs", "files", removed)
	}
	if err != nil {
		l.logger.Warn("cleaning the image cache", "error", err)
	}
}

// startAppliance stages the appliance's release, removes stray VMs, starts
// the appliance or takes it over and updates it if it runs an older release
// (updateAppliance); then it sets the host's DNS rules and cleans the appliance's
// folder. The zones' rules come with phase 2b; until then it only removes
// the rules of an earlier run.
func (l *Lab) startAppliance(ctx context.Context, opts MachineOptions) (*system.Appliance, error) {
	dir := filepath.Join(l.dataDir, ApplianceDir)
	release, err := l.system.StageApplianceRelease(opts.ApplianceRelease, dir)
	if err != nil {
		return nil, err
	}
	var running state.Appliance
	keep := []string{}
	_ = l.View(func(tx *state.Tx) error {
		running = tx.Lab().Appliance
		running.MACSet = string(macSet(tx))
		for _, id := range state.IDs(tx, state.Servers, func(*state.Server) bool { return true }) {
			keep = append(keep, ServerVM(id))
		}
		return nil
	})
	// the other set's appliance is what an update that HLab's end cut short
	// left: the state names the new one only once it has every picture
	keep = append(keep, system.ApplianceVM(running.MACSet))
	removed, err := l.system.RemoveStrayVMs(ctx, keep)
	if err != nil {
		return nil, err
	}
	if len(removed) > 0 {
		l.logger.Info("removed VMs that an earlier run left", "vms", removed)
	}
	appliance := l.newAppliance(picture.MACSet(running.MACSet), release, opts.Timings)
	takenOver, err := appliance.Start(ctx, l.pictures(l.pictureLab()))
	if err != nil {
		return nil, fmt.Errorf("starting the appliance: %w", err)
	}
	switch {
	case !takenOver:
		running.Release = release.ID
		if err := l.Change(func(tx *state.Tx) error { tx.Lab().Appliance = running; return nil }); err != nil {
			return nil, errors.Join(err, appliance.Stop(context.WithoutCancel(ctx)))
		}
	case running.Release != release.ID:
		if appliance, err = l.updateAppliance(ctx, appliance, running, release, opts.Timings); err != nil {
			return nil, err
		}
	}
	if err := l.system.SetHostDNS(ctx, nil); err != nil {
		return nil, errors.Join(err, appliance.Stop(context.WithoutCancel(ctx)))
	}
	l.cleanApplianceDir(ctx, release)
	return appliance, nil
}

// updateAppliance replaces the running appliance, taken over from a
// crashed hlabd, by one of the new release with the other MAC set:
// the new one starts beside the old one on the same switches and gets every
// picture; then the state names it, the old one goes, and the new one
// announces its addresses again. If the new one does not come up, the old
// one runs on and the next start tries again.
func (l *Lab) updateAppliance(ctx context.Context, old *system.Appliance, from state.Appliance, release system.ApplianceRelease, timings system.ApplianceTimings) (*system.Appliance, error) {
	started := time.Now()
	set := picture.MACSet(from.MACSet).Other()
	l.logger.Info("updating the appliance", "from", from.Release, "to", release.ID, "mac_set", set)
	facts := l.pictureLab()
	facts.MACSet = set
	next := l.newAppliance(set, release, timings)
	if _, err := next.Start(ctx, l.pictures(facts)); err != nil {
		err = errors.Join(err, next.Remove(context.WithoutCancel(ctx)))
		if ctx.Err() != nil {
			return nil, errors.Join(err, old.Stop(context.WithoutCancel(ctx)))
		}
		l.logger.Error("the appliance's update failed; the old one runs on", "error", err)
		return old, nil
	}
	err := l.Change(func(tx *state.Tx) error {
		tx.Lab().Appliance = state.Appliance{Release: release.ID, MACSet: string(set)}
		return nil
	})
	if err == nil {
		err = old.Remove(ctx)
	}
	if err != nil {
		// the next start removes what is left of either
		return nil, errors.Join(err, next.Stop(context.WithoutCancel(ctx)))
	}
	if err := next.Announce(ctx); err != nil {
		l.logger.Warn("announcing the new appliance's addresses", "error", err)
	}
	l.logger.Info("the appliance is updated", "took", time.Since(started).Round(time.Millisecond))
	return next, nil
}

// newAppliance returns the appliance with a MAC set on a release; it does
// not start it yet.
func (l *Lab) newAppliance(set picture.MACSet, release system.ApplianceRelease, timings system.ApplianceTimings) *system.Appliance {
	spec := system.ApplianceSpec{
		VM:         system.ApplianceVM(string(set)),
		Release:    release.Disk,
		SystemDisk: filepath.Join(l.dataDir, ApplianceDir, "system-"+string(set)+".vhdx"),
		MACs:       [4]string{set.MAC(1), set.MAC(2), set.MAC(3), set.MAC(4)},
	}
	return l.system.NewAppliance(spec, timings, l.logger.With("part", "appliance"))
}

// cleanApplianceDir removes from the appliance's folder what the running
// appliance and the newest release do not need: earlier releases, the
// other set's system disk, copies cut short. A failure only costs disk
// space, so it is logged.
func (l *Lab) cleanApplianceDir(ctx context.Context, release system.ApplianceRelease) {
	var running state.Appliance
	_ = l.View(func(tx *state.Tx) error {
		running = tx.Lab().Appliance
		running.MACSet = string(macSet(tx))
		return nil
	})
	dir := filepath.Join(l.dataDir, ApplianceDir)
	keep := []string{
		release.Disk,
		filepath.Join(dir, running.Release+".vhdx"),
		filepath.Join(dir, "system-"+running.MACSet+".vhdx"),
	}
	removed, err := l.system.CleanDir(ctx, dir, keep)
	if len(removed) > 0 {
		l.logger.Info("removed from the appliance's folder what it no longer needs", "files", removed)
	}
	if err != nil {
		l.logger.Warn("cleaning the appliance's folder", "error", err)
	}
}

// macSet returns the running appliance's MAC set, A before its first
// start.
func macSet(tx *state.Tx) picture.MACSet {
	if set := picture.MACSet(tx.Lab().Appliance.MACSet); set == picture.MACSetB {
		return set
	}
	return picture.MACSetA
}

// pictureLab returns what the pictures are built from, with a new
// generation: the running appliance's MAC set and every server of the
// state, also one whose VM is still being made.
func (l *Lab) pictureLab() picture.Lab {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.generation++
	facts := l.basePictureLab()
	facts.Generation = l.generation
	tx := state.Read(l.state)
	facts.MACSet = macSet(tx)
	for _, server := range state.All(tx, state.Servers) {
		facts.Servers = append(facts.Servers, serverFacts(tx, server))
	}
	return facts
}

// basePictureLab returns the lab's settings for pictures and seeds, without
// servers.
func (l *Lab) basePictureLab() picture.Lab {
	facts := picture.DefaultLab()
	facts.LabKey = l.labKey
	return facts
}

// pictures builds every picture of the data plane from facts.
func (l *Lab) pictures(facts picture.Lab) system.Pictures {
	return system.Pictures{
		Links:    picture.Links(facts),
		Ruleset:  picture.Ruleset(facts),
		DHCP:     picture.DHCP(facts),
		Metadata: picture.Metadata(facts, l.catalog),
	}
}

// stopMachine shuts the appliance down and removes the host's DNS rules and
// its link. It goes on after a failure, so that as little as
// possible stays behind.
func (l *Lab) stopMachine() error {
	l.mu.Lock()
	appliance, watching, shutdown := l.appliance, l.watching, l.serverShutdown
	l.appliance, l.watching = nil, nil
	var vms []string
	for _, id := range state.IDs(state.Read(l.state), state.Servers, func(*state.Server) bool { return true }) {
		vms = append(vms, ServerVM(id))
	}
	l.mu.Unlock()
	if watching != nil {
		watching()
		l.watchers.Wait()
	}
	if appliance == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), machineStopTimeout+shutdown)
	defer cancel()
	// want_running stays as it is: the next start starts them again
	serversErr := l.system.ShutdownServers(ctx, vms, shutdown)
	return errors.Join(serversErr, appliance.Stop(ctx), l.system.DisconnectHost(ctx))
}
