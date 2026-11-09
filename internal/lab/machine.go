package lab

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/config"
	"github.com/webcodingcat/hlab/internal/picture"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/system"
)

// ApplianceDir is the folder of the appliance's disks in the data folder.
const ApplianceDir = "appliance"

// machineStopTimeout bounds stopping the machine's side of the lab: the
// appliance's shutdown (30 s) and removing the host link.
const machineStopTimeout = 2 * time.Minute

// Machine is the lab's side on the machine: the servers' VMs and files, the
// image cache, the appliance with its pictures and its update, and the
// servers' watchdog. The steps of the actions are its methods (DEVELOPING.md,
// Actions). Lab.Machine returns it. It works before Start too, without an
// appliance: the API's unit tests run the steps on the fake platform, and
// there are no pictures to send.
//
// Its fields have no mutex of their own: they share the lab's, because the
// watchdog and the room check read them together with the state and the
// flags, and a second mutex would add a lock order to keep. The locks it
// holds across calls to the system, pictureMu and the image loads, are
// separate, and neither is ever taken while holding the lab's mutex.
type Machine struct {
	lab    *Lab
	system *system.System
	labKey string      // Options.LabKey
	room   config.Room // Options.Room

	// pictureMu makes UpdatePictures one at a time, across its calls to the
	// appliance, so that pictures arrive in the order of their generations
	pictureMu sync.Mutex

	// the lab's mutex guards the fields below

	// appliance runs between Start and the lab's Close; nil without it
	appliance  *system.Appliance
	generation uint64 // of the newest pictures
	// host is what the machine has for VMs; zero before Start, and then
	// nothing is refused for lack of room
	host system.HostResources
	// imageLoads are the image cache's loads by image name: one load per
	// image at a time, held across the system's calls
	imageLoads map[string]*sync.Mutex
	// looks are the servers the watchdog looks at in its current round, each
	// true once an action flagged it during the look; nil between rounds
	// (watchRound)
	looks map[state.ResourceRef]bool
	// watching ends the servers' watchdog (Start)
	watching       context.CancelFunc
	serverShutdown time.Duration // MachineOptions.ServerShutdown

	watchers sync.WaitGroup // the watchdog's goroutine
}

// MachineOptions are what the lab needs on the machine.
type MachineOptions struct {
	// ApplianceRelease is the appliance's release disk (bin/dev appliance).
	ApplianceRelease string
	// Timings are the appliance watchdog's; zero takes the defaults.
	Timings system.ApplianceTimings
	// ServerShutdown is how long the servers' guests may take to shut down
	// when the lab stops, before they are turned off; zero takes 60 s.
	ServerShutdown time.Duration
	// ServerWatchInterval is how often the servers' watchdog looks at their
	// VMs (watchRound); zero takes 2 s. Tests set it short.
	ServerWatchInterval time.Duration
}

// defaultServerShutdown and defaultServerWatchInterval are the defaults of
// MachineOptions.ServerShutdown and ServerWatchInterval.
const (
	defaultServerShutdown      = time.Minute
	defaultServerWatchInterval = 2 * time.Second
)

// Start starts the lab's side on the machine: it checks the address ranges
// and connects the host, removes the VMs that an earlier run left, starts or
// takes over the appliance, sends it the pictures and sets the host's DNS
// rules. The service calls it after Open and before it listens on the host
// link; the lab's Close stops it again. If a step fails, the host link is
// removed again.
func (m *Machine) Start(ctx context.Context, opts MachineOptions) error {
	if opts.Timings == (system.ApplianceTimings{}) {
		opts.Timings = system.DefaultApplianceTimings()
	}
	if opts.ServerShutdown == 0 {
		opts.ServerShutdown = defaultServerShutdown
	}
	if opts.ServerWatchInterval == 0 {
		opts.ServerWatchInterval = defaultServerWatchInterval
	}
	host, err := m.system.HostResources(ctx)
	if err != nil {
		return err
	}
	facts := m.basePictureLab()
	link := system.HostLink{
		Ranges:    []netip.Prefix{facts.Range4, facts.Range6},
		Addresses: facts.HostAddresses(),
		Gateways:  facts.ApplianceAddresses(),
	}
	if err := m.system.ConnectHost(ctx, link); err != nil {
		return err
	}
	appliance, err := m.startAppliance(ctx, opts)
	if err != nil {
		if disconnectErr := m.system.DisconnectHost(context.WithoutCancel(ctx)); disconnectErr != nil {
			m.lab.logger.Warn("removing the host link after a failed start", "error", disconnectErr)
		}
		return err
	}
	m.lab.mu.Lock()
	m.appliance = appliance
	m.host = host
	m.serverShutdown = opts.ServerShutdown
	m.lab.mu.Unlock()
	if err := m.resumeServers(ctx); err != nil {
		return errors.Join(err, m.stop())
	}
	watchCtx, cancel := context.WithCancel(context.Background())
	m.lab.mu.Lock()
	m.watching = cancel
	m.lab.mu.Unlock()
	m.watchers.Go(func() { m.watchServers(watchCtx, opts.ServerWatchInterval) })
	return nil
}

// resumeServers brings the servers back after the lab started: a
// VM that runs is taken over (hlabd had crashed), or turned off if its
// server does not want to run (HLab ended during a poweroff, whose plan
// had set want_running); one that is off gets its NIC on the public
// switch again, and starts if its server wants to run.
// A server without a VM gets one (restoreServer).
// The servers' statuses follow their VMs, also for actions that HLab's end
// cut short; a server that cannot be brought back stays off. Then the
// data folder loses the files that no server needs. It gets the servers' records
// once and reads them only, so keeping them across the calls to the
// system is safe: a server deleted meanwhile is skipped where its status
// is set.
func (m *Machine) resumeServers(ctx context.Context) error {
	var servers []*state.Server
	_ = m.lab.View(func(tx *state.Tx) error {
		servers = state.All(tx, state.Servers)
		return nil
	})
	for _, server := range servers {
		err := m.resumeServer(ctx, server)
		if ctx.Err() != nil {
			return ctx.Err() // the start was called off
		}
		if err == nil {
			continue
		}
		// one server's trouble (a broken disk, an image that does not
		// load) must not keep the lab and the other servers from running;
		// it wants to run still, so the next start tries again
		m.lab.logger.Error("a server could not be brought back; it stays off", "server", server.ID, "error", err)
		if err := m.lab.SetServerStatus(server.ID, state.ServerOff); err != nil && !errors.Is(err, state.ErrNotFound) {
			m.lab.logger.Warn("setting the server off", "server", server.ID, "error", err)
		}
	}
	m.cleanDataFiles(ctx)
	return nil
}

// resumeServer brings one server back; it gets the server's record as it
// was when the start began.
func (m *Machine) resumeServer(ctx context.Context, server *state.Server) error {
	vm := ServerVM(server.ID)
	exists, err := m.system.ServerExists(ctx, vm)
	if err != nil {
		return err
	}
	if !exists {
		if err := m.restoreServer(ctx, server); err != nil {
			return err
		}
	}
	runs, err := m.system.ServerRuns(ctx, vm)
	if err != nil {
		return err
	}
	var private []system.PrivateNIC
	_ = m.lab.View(func(tx *state.Tx) error {
		private = systemNICs(tx, server)
		return nil
	})
	if runs && !server.WantRunning {
		if err := m.system.PowerServer(ctx, vm, system.PowerOff); err != nil {
			return err
		}
		runs = false
	}
	status := state.ServerRunning
	if !runs {
		if exists {
			if err := m.system.ReconnectServer(ctx, vm, server.MAC, private); err != nil {
				return err
			}
		}
		status = state.ServerOff
		if server.WantRunning {
			if err := m.system.PowerServer(ctx, vm, system.PowerOn); err != nil {
				return err
			}
			status = state.ServerRunning
		}
	}
	if err := m.lab.SetServerStatus(server.ID, status); err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	return nil
}

// restoreServer makes a VM for a server that has none. If its disk is
// there (an uninstall kept the data but removed the VMs), the new VM gets
// it as it is. Without a disk, as when HLab ended during the create, the
// server is built anew; so is one whose base image is gone.
func (m *Machine) restoreServer(ctx context.Context, server *state.Server) error {
	if server.BaseImage != "" {
		kept, err := m.makeServerVM(ctx, server.ID)
		switch {
		case kept && err != nil:
			return err // never build anew over the server's own disk
		case kept:
			m.lab.logger.Info("a server had no VM; it has a new one with its disk", "server", server.ID)
			return nil
		case err == nil:
			m.lab.logger.Warn("a server had no VM and no disk; it has a new disk on its image", "server", server.ID)
			return nil
		}
		m.lab.logger.Warn("a server without a VM and a disk cannot get one on its image", "server", server.ID, "error", err)
	}
	m.lab.logger.Warn("a server has no VM, as HLab ended during its create; building it anew", "server", server.ID)
	disk, seedISO := m.serverFiles(server.ID)
	if err := m.system.DeleteServer(ctx, ServerVM(server.ID), disk, seedISO); err != nil {
		return err // what the cut-short create left
	}
	return m.BuildServer(ctx, server.ID)
}

// cleanDataFiles removes what no record needs from the data folder: the
// cached images that neither a cache entry nor a server's disk needs, what
// loads cut short left, and the disks and seeds of servers that are gone
// (a delete that HLab's end cut short, also a project's).
// A failure only costs disk space, so it is logged.
func (m *Machine) cleanDataFiles(ctx context.Context) {
	var images, disks, seeds []string
	_ = m.lab.View(func(tx *state.Tx) error {
		for _, cached := range tx.Lab().Images {
			images = append(images, cached.File)
		}
		for _, server := range state.All(tx, state.Servers) {
			images = append(images, server.BaseImage)
			disk, seedISO := m.serverFiles(server.ID)
			disks, seeds = append(disks, disk), append(seeds, seedISO)
		}
		return nil
	})
	for _, clean := range []struct {
		dir  string
		keep []string
	}{{ImageCacheDir, images}, {ServerDiskDir, disks}, {SeedDir, seeds}} {
		removed, err := m.system.CleanDir(ctx, filepath.Join(m.lab.dataDir, clean.dir), clean.keep)
		if len(removed) > 0 {
			m.lab.logger.Info("removed files that no server needs", "files", removed)
		}
		if err != nil {
			m.lab.logger.Warn("cleaning the data folder", "folder", clean.dir, "error", err)
		}
	}
}

// startAppliance stages the appliance's release, removes stray VMs, starts
// the appliance or takes it over and updates it if it runs an older release
// (updateAppliance); then it sets the host's DNS rules and cleans the appliance's
// folder. The zones' rules come with phase 2b; until then it only removes
// the rules of an earlier run.
func (m *Machine) startAppliance(ctx context.Context, opts MachineOptions) (*system.Appliance, error) {
	dir := filepath.Join(m.lab.dataDir, ApplianceDir)
	release, err := m.system.StageApplianceRelease(opts.ApplianceRelease, dir)
	if err != nil {
		return nil, err
	}
	var running state.Appliance
	keep := []string{}
	_ = m.lab.View(func(tx *state.Tx) error {
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
	removed, err := m.system.RemoveStrayVMs(ctx, keep)
	if err != nil {
		return nil, err
	}
	if len(removed) > 0 {
		m.lab.logger.Info("removed VMs that an earlier run left", "vms", removed)
	}
	appliance := m.newAppliance(picture.MACSet(running.MACSet), release, opts.Timings)
	takenOver, err := appliance.Start(ctx, m.pictures(m.pictureLab()))
	if err != nil {
		return nil, fmt.Errorf("starting the appliance: %w", err)
	}
	switch {
	case !takenOver:
		running.Release = release.ID
		if err := m.lab.Change(func(tx *state.Tx) error { tx.Lab().Appliance = running; return nil }); err != nil {
			return nil, errors.Join(err, appliance.Stop(context.WithoutCancel(ctx)))
		}
	case running.Release != release.ID:
		if appliance, err = m.updateAppliance(ctx, appliance, running, release, opts.Timings); err != nil {
			return nil, err
		}
	}
	if err := m.system.SetHostDNS(ctx, nil); err != nil {
		return nil, errors.Join(err, appliance.Stop(context.WithoutCancel(ctx)))
	}
	m.cleanApplianceDir(ctx, release)
	return appliance, nil
}

// updateAppliance replaces the running appliance, taken over from a
// crashed hlabd, by one of the new release with the other MAC set:
// the new one starts beside the old one on the same switches and gets every
// picture; then the state names it, the old one goes, and the new one
// announces its addresses again. If the new one does not come up, the old
// one runs on and the next start tries again.
func (m *Machine) updateAppliance(ctx context.Context, old *system.Appliance, from state.Appliance, release system.ApplianceRelease, timings system.ApplianceTimings) (*system.Appliance, error) {
	started := time.Now()
	set := picture.MACSet(from.MACSet).Other()
	m.lab.logger.Info("updating the appliance", "from", from.Release, "to", release.ID, "mac_set", set)
	facts := m.pictureLab()
	facts.MACSet = set
	next := m.newAppliance(set, release, timings)
	if _, err := next.Start(ctx, m.pictures(facts)); err != nil {
		err = errors.Join(err, next.Remove(context.WithoutCancel(ctx)))
		if ctx.Err() != nil {
			return nil, errors.Join(err, old.Stop(context.WithoutCancel(ctx)))
		}
		m.lab.logger.Error("the appliance's update failed; the old one runs on", "error", err)
		return old, nil
	}
	err := m.lab.Change(func(tx *state.Tx) error {
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
		m.lab.logger.Warn("announcing the new appliance's addresses", "error", err)
	}
	m.lab.logger.Info("the appliance is updated", "took", time.Since(started).Round(time.Millisecond))
	return next, nil
}

// newAppliance returns the appliance with a MAC set on a release; it does
// not start it yet.
func (m *Machine) newAppliance(set picture.MACSet, release system.ApplianceRelease, timings system.ApplianceTimings) *system.Appliance {
	spec := system.ApplianceSpec{
		VM:         system.ApplianceVM(string(set)),
		Release:    release.Disk,
		SystemDisk: filepath.Join(m.lab.dataDir, ApplianceDir, "system-"+string(set)+".vhdx"),
		MACs:       [4]string{set.MAC(1), set.MAC(2), set.MAC(3), set.MAC(4)},
	}
	return m.system.NewAppliance(spec, timings, m.lab.logger.With("part", "appliance"))
}

// cleanApplianceDir removes from the appliance's folder what the running
// appliance and the newest release do not need: earlier releases, the
// other set's system disk, copies cut short. A failure only costs disk
// space, so it is logged.
func (m *Machine) cleanApplianceDir(ctx context.Context, release system.ApplianceRelease) {
	var running state.Appliance
	_ = m.lab.View(func(tx *state.Tx) error {
		running = tx.Lab().Appliance
		running.MACSet = string(macSet(tx))
		return nil
	})
	dir := filepath.Join(m.lab.dataDir, ApplianceDir)
	keep := []string{
		release.Disk,
		filepath.Join(dir, running.Release+".vhdx"),
		filepath.Join(dir, "system-"+running.MACSet+".vhdx"),
	}
	removed, err := m.system.CleanDir(ctx, dir, keep)
	if len(removed) > 0 {
		m.lab.logger.Info("removed from the appliance's folder what it no longer needs", "files", removed)
	}
	if err != nil {
		m.lab.logger.Warn("cleaning the appliance's folder", "error", err)
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
// generation: the running appliance's MAC set, every server of the state,
// also one whose VM is still being made, the assigned Floating IPs, the
// firewalls that apply to a server and the private networks.
func (m *Machine) pictureLab() picture.Lab {
	m.lab.mu.Lock()
	defer m.lab.mu.Unlock()
	m.generation++
	facts := m.basePictureLab()
	facts.Generation = m.generation
	tx := m.lab.read()
	facts.MACSet = macSet(tx)
	for _, server := range state.All(tx, state.Servers) {
		facts.Servers = append(facts.Servers, serverFacts(tx, server))
	}
	facts.FloatingIPs = floatingIPFacts(tx)
	facts.Firewalls = firewallFacts(tx)
	facts.Networks = networkFacts(tx)
	return facts
}

// basePictureLab returns the lab's settings for pictures and seeds, without
// servers.
func (m *Machine) basePictureLab() picture.Lab {
	facts := picture.DefaultLab()
	facts.LabKey = m.labKey
	return facts
}

// pictures builds every picture of the data plane from facts.
func (m *Machine) pictures(facts picture.Lab) system.Pictures {
	return system.Pictures{
		Links:    picture.Links(facts),
		Ruleset:  picture.Ruleset(facts),
		DHCP:     picture.DHCP(facts),
		Metadata: picture.Metadata(facts, m.lab.catalog),
	}
}

// stop shuts the appliance down and removes the host's DNS rules and
// its link. It goes on after a failure, so that as little as
// possible stays behind.
func (m *Machine) stop() error {
	m.lab.mu.Lock()
	appliance, watching, shutdown := m.appliance, m.watching, m.serverShutdown
	m.appliance, m.watching = nil, nil
	var vms []string
	for _, id := range state.IDs(m.lab.read(), state.Servers, func(*state.Server) bool { return true }) {
		vms = append(vms, ServerVM(id))
	}
	m.lab.mu.Unlock()
	if watching != nil {
		watching()
		m.watchers.Wait()
	}
	if appliance == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), machineStopTimeout+shutdown)
	defer cancel()
	// want_running stays as it is: the next start starts them again
	serversErr := m.system.ShutdownServers(ctx, vms, shutdown)
	return errors.Join(serversErr, appliance.Stop(ctx), m.system.DisconnectHost(ctx))
}

// UpdatePictures sends the appliance every picture built from the state as
// it is now; before Start (the API's unit tests) there is nothing
// to send. Updates run one at a time, so that an older picture never
// follows a newer one.
func (m *Machine) UpdatePictures(ctx context.Context) error {
	m.pictureMu.Lock()
	defer m.pictureMu.Unlock()
	m.lab.mu.Lock()
	appliance := m.appliance
	m.lab.mu.Unlock()
	if appliance == nil {
		return nil
	}
	return appliance.Update(ctx, m.pictures(m.pictureLab()))
}

// updatePicturesAfterUndo sends the pictures after an action's undo,
// which took quick changes back that the steps may have sent already: a
// server whose create failed, an assign that did not finish. It runs in
// the action's goroutine before the action counts as done, so that an
// action already queued behind it finds the appliance as the state is (an
// action sent after the action's end may start meanwhile; the pictures
// stay in order through pictureMu). The action
// is finished already; a failure is logged, as the appliance's watchdog
// sends the newest pictures again. A closing lab sends nothing: its next
// start sends every picture.
func (m *Machine) updatePicturesAfterUndo() {
	if err := m.UpdatePictures(m.lab.ctx); err != nil && m.lab.ctx.Err() == nil {
		m.lab.logger.Warn("sending the pictures after an undo; the watchdog sends them again", "error", err)
	}
}

// UpdatePicturesLater sends the pictures as UpdatePictures does, but in the
// background, for a change whose answer need not wait for the appliance (a
// rename). It ends with the lab's context, and Close waits for it; a closing
// lab sends nothing. A failure is logged, and the appliance's watchdog
// sends the newest pictures again anyway.
func (m *Machine) UpdatePicturesLater() {
	m.lab.mu.Lock()
	defer m.lab.mu.Unlock()
	if m.lab.closed {
		return
	}
	m.lab.background.Go(func() {
		if err := m.UpdatePictures(m.lab.ctx); err != nil && m.lab.ctx.Err() == nil {
			m.lab.logger.Warn("sending the pictures after a change; the watchdog sends them again", "error", err)
		}
	})
}
