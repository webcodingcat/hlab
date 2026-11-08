package lab

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"time"

	"github.com/webcodingcat/hlab/internal/picture"
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
}

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
	l.mu.Unlock()
	return nil
}

// startAppliance removes stray VMs, starts the appliance and sets the host's
// DNS rules; the zones' rules come with phase 2b, until then it only
// removes the rules of an earlier run.
func (l *Lab) startAppliance(ctx context.Context, lab picture.Lab, opts MachineOptions) (*system.Appliance, error) {
	removed, err := l.system.RemoveStrayVMs(ctx, []string{system.ApplianceVM})
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
// generation. The servers come with phase 1b-4, the MAC set's switch with
// the appliance's update (1b-6).
func (l *Lab) pictureLab() picture.Lab {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.generation++
	lab := picture.DefaultLab()
	lab.Generation = l.generation
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
	appliance := l.appliance
	l.appliance = nil
	l.mu.Unlock()
	if appliance == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), machineStopTimeout)
	defer cancel()
	return errors.Join(appliance.Stop(ctx), l.system.DisconnectHost(ctx))
}
