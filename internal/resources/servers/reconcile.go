package servers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"

	"github.com/webcodingcat/hlab/internal/cidata"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/store"
)

// Guest network as at Hetzner (ARCHITECTURE.md "Network").
var (
	guestGateway     = netip.MustParseAddr("172.31.1.1")
	guestNameservers = []netip.Addr{netip.MustParseAddr("185.12.64.1"), netip.MustParseAddr("185.12.64.2")}
)

// VMName is the VM name of server id under the profile tag prefix.
func VMName(prefix string, id int64) string {
	return fmt.Sprintf("%s-server-%d", prefix, id)
}

func (m *Module) vmName(id int64) string { return VMName(m.d.Settings.VMPrefix, id) }

func (m *Module) diskPath(id int64) string {
	return filepath.Join(m.d.Settings.DataFolder, "disks", fmt.Sprintf("server-%d.vhdx", id))
}

func (m *Module) seedPath(id int64) string {
	return filepath.Join(m.d.Settings.DataFolder, "disks", fmt.Sprintf("server-%d-cidata.iso", id))
}

func (m *Module) vmFolder(id int64) string {
	return filepath.Join(m.d.Settings.DataFolder, "vms", fmt.Sprintf("server-%d", id))
}

// Reconcile implements resource.Reconciler: it creates what is missing of
// the server's disk, seed and VM with its adapter, and pushes the agent
// document. It never changes the power state.
func (m *Module) Reconcile(ctx context.Context, id int64) error {
	return m.reconcile(ctx, m.d.Store, id)
}

// reconcile is Reconcile on the given store. An existing VM is taken as
// it is; a missing disk or seed file is created.
func (m *Module) reconcile(ctx context.Context, stor *store.Store, id int64) error {
	rec, all, err := loadWithAll(stor, id)
	if err != nil {
		return err
	}
	typ, err := m.recordType(rec)
	if err != nil {
		return err
	}
	mac, err := net.ParseMAC(rec.Hlab.MAC)
	if err != nil {
		return err
	}
	// A server without public IPv4 keeps its public adapter without address.
	ip := rec.IPv4()

	disk := m.diskPath(id)
	if _, err := os.Stat(disk); errors.Is(err, os.ErrNotExist) {
		base, err := m.baseDisk(ctx, stor, rec)
		if err != nil {
			return err
		}
		if err := m.d.Platform.CreateDisk(ctx, disk, int64(rec.PrimaryDiskSize)<<30, base, [16]byte{}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	seed := m.seedPath(id)
	if _, err := os.Stat(seed); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(seed), 0o750); err != nil {
			return err
		}
		if err := cidata.BuildFile(m.seed(rec), seed); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	name := m.vmName(id)
	if _, err := m.d.Platform.State(ctx, name); err != nil {
		if !isNotFound(err) {
			return err
		}
		nics := privateNICs(rec, all)
		if err := m.createVM(ctx, name, id, typ.Cores, int64(typ.Memory*1024), mac, rec.Hlab.Location, nics); err != nil {
			return err
		}
		if err := stor.Tx(func(tx store.Tx) error {
			return Update(tx, id, func(r *Record) error {
				r.Hlab.Adapters = nil
				for _, nic := range nics {
					r.Hlab.Adapters = append(r.Hlab.Adapters, nic.MAC.String())
				}
				return nil
			})
		}); err != nil {
			return err
		}
		// The other members learn the new adapter's MAC.
		var nets []int64
		for _, pn := range rec.PrivateNet {
			nets = append(nets, pn.Network)
		}
		if err := m.syncDeny(ctx, all, nets); err != nil {
			return err
		}
		// A VM made again keeps the server's ISO.
		if rec.Hlab.ISO != "" {
			iso, err := m.d.Images.EnsureISO(ctx, rec.Hlab.ISO)
			if err != nil {
				return err
			}
			if err := m.setISO(ctx, stor, id, iso, rec.Hlab.ISO, rec.ISO); err != nil {
				return err
			}
		}
	}
	if err := m.d.PushAgent(ctx); err != nil {
		return err
	}
	slog.Info("server reconciled", "id", id, "vm", name, "ipv4", ip.String())
	return nil
}

// seed is the seed of server rec: the public interface with the public
// IPv4 (without one, the adapter gets no address) and one interface per
// private network.
func (m *Module) seed(rec Record) cidata.Seed {
	instanceID := rec.Hlab.InstanceID
	if instanceID == "" {
		instanceID = strconv.FormatInt(rec.ID, 10)
	}
	public := cidata.Interface{MAC: rec.Hlab.MAC}
	if ip := rec.IPv4(); ip.IsValid() {
		public.Addresses = []netip.Prefix{netip.PrefixFrom(ip, 32)}
		public.Gateway = guestGateway
		public.Nameservers = guestNameservers
	}
	return cidata.Seed{
		InstanceID: instanceID,
		Hostname:   rec.Name,
		SSHKeys:    rec.Hlab.SSHKeys,
		UserData:   seedUserData(rec.Hlab.UserData, rec.Hlab.AutomountVolumes),
		VendorData: vendorData(rec.Hlab.RootHash),
		Interfaces: append([]cidata.Interface{public}, privateInterfaces(rec)...),
		NoOnLink:   !netplanFlavor(rec.Image),
	}
}

// notNetplan are the os_flavor values of images whose network renderer is
// not netplan; their seeds leave on-link out of every route.
var notNetplan = map[string]bool{"rocky": true, "alma": true, "centos": true, "fedora": true, "opensuse": true}

// netplanFlavor reports whether the image object image may get on-link
// routes: false only for a readable image whose os_flavor is in notNetplan.
func netplanFlavor(image json.RawMessage) bool {
	var img struct {
		OSFlavor string `json:"os_flavor"`
	}
	if len(image) == 0 || json.Unmarshal(image, &img) != nil {
		return true
	}
	return !notNetplan[img.OSFlavor]
}

// createVM creates the VM with the server's disk, public adapter, private
// adapters and seed.
func (m *Module) createVM(ctx context.Context, name string, id int64, cores int, memoryMB int64, mac net.HardwareAddr, location string, private []platform.NICSpec) error {
	p := m.d.Platform
	if err := p.CreateVM(ctx, m.vmSpec(name, id, cores, memoryMB)); err != nil {
		return err
	}
	if err := p.AttachDisk(ctx, name, m.diskPath(id), 0); err != nil {
		return err
	}
	if err := p.AttachNIC(ctx, name, platform.NICSpec{MAC: mac, Segment: platform.SegmentFor(location, 0)}); err != nil {
		return err
	}
	for _, nic := range private {
		if err := p.AttachNIC(ctx, name, nic); err != nil {
			return err
		}
	}
	if err := p.SetDVD(ctx, name, 0, m.seedPath(id)); err != nil {
		return err
	}
	return p.SetBootOrder(ctx, name, bootOrder(false, false))
}

// vmSpec is the VM specification of server id with cores and memoryMB.
func (m *Module) vmSpec(name string, id int64, cores int, memoryMB int64) platform.VMSpec {
	spec := platform.VMSpec{
		Name:       name,
		CPUs:       cores,
		MemoryMB:   memoryMB,
		Folder:     m.vmFolder(id),
		StopAction: platform.StopSave,
	}
	if m.d.Settings.StopBehaviour == "shutdown" {
		spec.StopAction = platform.StopShutdown
	}
	return spec
}

// deleteMachine turns the VM off, removes it with its disk, seed, rescue
// disk, rescue seed and folder, and pushes the agent document without the
// server. Missing parts are skipped, so a half-created server is deleted
// too.
func (m *Module) deleteMachine(ctx context.Context, id int64) error {
	name := m.vmName(id)
	if err := m.d.Platform.DeleteVM(ctx, name); err != nil && !isNotFound(err) {
		return err
	}
	for _, p := range []string{m.diskPath(id), m.seedPath(id), m.rescueDiskPath(id), m.rescueSeedPath(id)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.RemoveAll(m.vmFolder(id)); err != nil {
		return err
	}
	if err := m.d.PushAgent(ctx); err != nil {
		return err
	}
	slog.Info("server deleted", "id", id, "vm", name)
	return nil
}

func isNotFound(err error) bool {
	var perr *platform.Error
	return errors.As(err, &perr) && perr.Kind == platform.NotFound
}
