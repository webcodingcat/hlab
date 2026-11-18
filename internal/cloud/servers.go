//go:build windows

package cloud

import (
	"context"
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
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// Guest network as at Hetzner (ARCHITECTURE.md "Network").
var (
	guestGateway     = netip.MustParseAddr("172.31.1.1")
	guestNameservers = []netip.Addr{netip.MustParseAddr("185.12.64.1"), netip.MustParseAddr("185.12.64.2")}
)

// serverVendorData lets root log in with the given keys, as on Hetzner's
// images; cloud images lock root by default. user_data overrides it.
const serverVendorData = "#cloud-config\ndisable_root: false\n"

// machines creates and deletes the VMs of servers (servers.Machines).
type machines struct{ c *Cloud }

func (c *Cloud) serverVM(id int64) string { return fmt.Sprintf("%s-server-%d", c.tag, id) }

func (c *Cloud) serverDisk(id int64) string {
	return filepath.Join(c.set.Storage.DataFolder, "disks", fmt.Sprintf("server-%d.vhdx", id))
}

func (c *Cloud) serverISO(id int64) string {
	return filepath.Join(c.set.Storage.DataFolder, "disks", fmt.Sprintf("server-%d-cidata.iso", id))
}

func (c *Cloud) serverFolder(id int64) string {
	return filepath.Join(c.set.Storage.DataFolder, "vms", fmt.Sprintf("server-%d", id))
}

// Create builds the server's disk from its image, writes its seed, creates
// the VM with disk, public adapter and seed, publishes its address to the
// agent and starts it if asked.
func (m machines) Create(ctx context.Context, id int64) error {
	c := m.c
	var rec servers.Record
	if err := c.st.Tx(func(tx store.Tx) error {
		var err error
		rec, err = servers.Load(tx, id)
		return err
	}); err != nil {
		return err
	}
	typ, ok := c.cat.ServerType(rec.Hlab.ServerType)
	if !ok {
		return fmt.Errorf("server type %q not in the catalog", rec.Hlab.ServerType)
	}
	mac, err := net.ParseMAC(rec.Hlab.MAC)
	if err != nil {
		return err
	}
	ip := rec.IPv4()
	if !ip.IsValid() {
		return errors.New("server has no public IPv4")
	}

	base, err := c.images.Ensure(ctx, rec.Hlab.Image)
	if err != nil {
		return err
	}
	disk := c.serverDisk(id)
	if err := c.plat.CreateDisk(ctx, disk, int64(typ.Disk)<<30, base, [16]byte{}); err != nil {
		return err
	}
	if err := cidata.BuildFile(cidata.Seed{
		InstanceID: strconv.FormatInt(id, 10),
		Hostname:   rec.Name,
		SSHKeys:    rec.Hlab.SSHKeys,
		UserData:   rec.Hlab.UserData,
		VendorData: serverVendorData,
		Interfaces: []cidata.Interface{{
			MAC:         rec.Hlab.MAC,
			Addresses:   []netip.Prefix{netip.PrefixFrom(ip, 32)},
			Gateway:     guestGateway,
			Nameservers: guestNameservers,
		}},
	}, c.serverISO(id)); err != nil {
		return err
	}

	name := c.serverVM(id)
	spec := platform.VMSpec{
		Name:       name,
		CPUs:       typ.Cores,
		MemoryMB:   int64(typ.Memory * 1024),
		Folder:     c.serverFolder(id),
		StopAction: platform.StopSave,
	}
	if c.set.Cloud.StopBehaviour == "shutdown" {
		spec.StopAction = platform.StopShutdown
	}
	if err := c.plat.CreateVM(ctx, spec); err != nil {
		return err
	}
	if err := c.plat.AttachDisk(ctx, name, disk, 0); err != nil {
		return err
	}
	if err := c.plat.AttachNIC(ctx, name, platform.NICSpec{
		MAC:     mac,
		Segment: platform.SegmentFor(rec.Hlab.Location, 0),
	}); err != nil {
		return err
	}
	if err := c.plat.SetDVD(ctx, name, 0, c.serverISO(id)); err != nil {
		return err
	}
	if err := c.plat.SetBootOrder(ctx, name, []platform.BootDevice{{Kind: "disk", Slot: 0}}); err != nil {
		return err
	}
	if err := c.PushAgent(ctx); err != nil {
		return err
	}
	if !rec.Hlab.StartAfterCreate {
		slog.Info("server created", "id", id, "vm", name)
		return nil
	}
	if err := c.st.Tx(func(tx store.Tx) error { return servers.SetStatus(tx, id, servers.StatusStarting) }); err != nil {
		return err
	}
	if err := c.plat.Power(ctx, name, platform.PowerStart); err != nil {
		return err
	}
	slog.Info("server created and started", "id", id, "vm", name, "ipv4", ip.String())
	return nil
}

// Delete turns the VM off, removes it with its disk, seed and folder, and
// pushes the agent document without the server. Missing parts are skipped,
// so a half-created server is deleted too.
func (m machines) Delete(ctx context.Context, id int64) error {
	c := m.c
	name := c.serverVM(id)
	var perr *platform.Error
	if err := c.plat.DeleteVM(ctx, name); err != nil && (!errors.As(err, &perr) || perr.Kind != platform.NotFound) {
		return err
	}
	for _, p := range []string{c.serverDisk(id), c.serverISO(id)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.RemoveAll(c.serverFolder(id)); err != nil {
		return err
	}
	if err := c.PushAgent(ctx); err != nil {
		return err
	}
	slog.Info("server deleted", "id", id, "vm", name)
	return nil
}
