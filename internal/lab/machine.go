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

// defaultServerShutdown is MachineOptions.ServerShutdown's default (13.2).
const defaultServerShutdown = time.Minute

// StartMachine starts the lab's side on the machine (13.1): it checks the
// address ranges and connects the host, removes the VMs that an earlier run
// left, starts or takes over the appliance, sends it the pictures and sets
// the host's DNS rules. The service calls it after Open and before it
// listens on the host link; Close stops it again. If a step fails, the host
// link goes again.
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
	lab := l.pictureLab()
	link := system.HostLink{
		Ranges:    []netip.Prefix{lab.Range4, lab.Range6},
		Addresses: lab.HostAddresses(),
		Gateways:  lab.ApplianceAddresses(),
	}
	if err := l.system.ConnectHost(ctx, link); err != nil {
		return err
	}
	appliance, err := l.startAppliance(ctx, lab, opts)
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

// resumeServers brings the servers back after the lab started (13.1): a
// VM that runs is taken over (hlabd had crashed); one that is off gets its
// NIC on the public switch again, and starts if its server wants to run.
// The servers' statuses follow their VMs, also for actions that HLab's end
// cut short.
func (l *Lab) resumeServers(ctx context.Context) error {
	var servers []*state.Server
	_ = l.View(func(tx *state.Tx) error {
		servers = state.All(tx, state.Servers)
		return nil
	})
	for _, server := range servers {
		vm := ServerVM(server.ID)
		runs, err := l.system.ServerRuns(ctx, vm)
		if err != nil {
			return err
		}
		status := state.ServerRunning
		if !runs {
			if err := l.system.ReconnectServer(ctx, vm, server.MAC); err != nil {
				// a server whose create HLab's end cut short has no VM
				l.logger.Warn("a server's VM could not be reconnected", "server", server.ID, "error", err)
				status = state.ServerOff
			} else if status = state.ServerOff; server.WantRunning {
				if err := l.system.PowerServer(ctx, vm, system.PowerOn); err != nil {
					return err
				}
				status = state.ServerRunning
			}
		}
		if err := l.SetServerStatus(server.ID, status); err != nil && !errors.Is(err, state.ErrNotFound) {
			return err
		}
	}
	return nil
}

// startAppliance removes stray VMs, starts the appliance and sets the host's
// DNS rules; the zones' rules come with phase 2b, until then it only
// removes the rules of an earlier run.
func (l *Lab) startAppliance(ctx context.Context, lab picture.Lab, opts MachineOptions) (*system.Appliance, error) {
	keep := []string{system.ApplianceVM}
	_ = l.View(func(tx *state.Tx) error {
		for _, id := range state.IDs(tx, state.Servers, func(*state.Server) bool { return true }) {
			keep = append(keep, ServerVM(id))
		}
		return nil
	})
	removed, err := l.system.RemoveStrayVMs(ctx, keep)
	if err != nil {
		return nil, err
	}
	if len(removed) > 0 {
		l.logger.Info("removed VMs that an earlier run left", "vms", removed)
	}
	spec := system.ApplianceSpec{
		Release:    opts.ApplianceRelease,
		SystemDisk: filepath.Join(l.dataDir, ApplianceDir, "system-"+string(lab.MACSet)+".vhdx"),
		MACs:       [4]string{lab.MACSet.MAC(1), lab.MACSet.MAC(2), lab.MACSet.MAC(3), lab.MACSet.MAC(4)},
	}
	appliance := l.system.NewAppliance(spec, opts.Timings, l.logger.With("part", "appliance"))
	if err := appliance.Start(ctx, l.pictures(lab)); err != nil {
		return nil, fmt.Errorf("starting the appliance: %w", err)
	}
	if err := l.system.SetHostDNS(ctx, nil); err != nil {
		return nil, errors.Join(err, appliance.Stop(context.WithoutCancel(ctx)))
	}
	return appliance, nil
}

// pictureLab returns what the pictures are built from, with a new
// generation: the servers that have a VM or are getting one. The MAC set's
// switch comes with the appliance's update (1b-6).
func (l *Lab) pictureLab() picture.Lab {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.generation++
	lab := picture.DefaultLab()
	lab.Generation = l.generation
	tx := state.Read(l.state)
	for _, server := range state.All(tx, state.Servers) {
		lab.Servers = append(lab.Servers, serverFacts(tx, server))
	}
	return lab
}

// pictures builds every picture of the data plane from lab.
func (l *Lab) pictures(lab picture.Lab) system.Pictures {
	return system.Pictures{
		Links:    picture.Links(lab),
		Ruleset:  picture.Ruleset(lab),
		DHCP:     picture.DHCP(lab),
		Metadata: picture.Metadata(lab, l.catalog),
	}
}

// stopMachine shuts the appliance down and removes the host's DNS rules and
// its link (13.2). It goes on after a failure, so that as little as
// possible stays behind.
func (l *Lab) stopMachine() error {
	l.mu.Lock()
	appliance, watching := l.appliance, l.watching
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
	ctx, cancel := context.WithTimeout(context.Background(), machineStopTimeout+l.serverShutdown)
	defer cancel()
	// want_running stays as it is: the next start starts them again
	serversErr := l.system.ShutdownServers(ctx, vms, l.serverShutdown)
	return errors.Join(serversErr, appliance.Stop(ctx), l.system.DisconnectHost(ctx))
}
