//go:build windows

package cloud

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/webcodingcat/hlab/appliance"
	"github.com/webcodingcat/hlab/internal/cidata"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/transit"
)

// The storage VM (CONTEXT.md: Debian 13 generic, 2 vCPU, 2048 MB of
// dynamic memory, 10 GB system disk, a 64 TiB dynamic data disk at SCSI
// LUN 1).
const (
	storageCPUs       = 2
	storageMemoryMB   = 2048
	storageSystemSize = 10 << 30
	storageDataSize   = 64 << 40
	storageDataLUN    = 1
	storageMAC        = "02:48:4c:00:00:03"
	storageSecretKey  = "storage.secret"
	storageDebs       = "storage-debs.json"
)

// storageGateway is the guests' gateway on the appliance; the storage VM
// sits on the public segment like a server.
var storageGateway = netip.MustParseAddr("172.31.1.1")

func (c *Cloud) storageName() string { return c.tag + "-storage" }

// storageDir holds the storage VM: its configuration, system disk, data
// disk and cidata ISO.
func (c *Cloud) storageDir() string {
	return filepath.Join(c.set.Storage.DataFolder, "storage")
}

func (c *Cloud) storageSystemDisk() string { return filepath.Join(c.storageDir(), "system.vhdx") }
func (c *Cloud) storageDataDisk() string   { return filepath.Join(c.storageDir(), "data.vhdx") }
func (c *Cloud) storageISO() string        { return filepath.Join(c.storageDir(), "cidata.iso") }

// storageSecret returns the storage agent's bearer secret, creating it once.
func (c *Cloud) storageSecret() (string, error) { return c.kvSecret(storageSecretKey) }

// ensureStorageVM creates the storage VM when it is missing and starts it
// when it is not running. A VM that is off gets a fresh cidata ISO, so a
// new hlab-agent reaches it.
func (c *Cloud) ensureStorageVM(ctx context.Context) error {
	name := c.storageName()
	spec := platform.VMSpec{
		Name:          name,
		CPUs:          storageCPUs,
		MemoryMB:      storageMemoryMB,
		DynamicMemory: true,
		Folder:        c.storageDir(),
		StopAction:    platform.StopSave,
	}
	if c.set.Cloud.StopBehaviour == "shutdown" {
		spec.StopAction = platform.StopShutdown
	}

	state, err := c.plat.State(ctx, name)
	var perr *platform.Error
	switch {
	case err == nil && state == platform.StateOff:
		if err := c.plat.SetVM(ctx, name, spec); err != nil {
			return err
		}
		if err := c.writeStorageISO(ctx); err != nil {
			return err
		}
		if err := c.plat.SetDVD(ctx, name, 0, c.storageISO()); err != nil {
			return err
		}
	case err == nil:
		// Hyper-V refuses a changed stop action on a running VM.
		if state == platform.StateRunning || state == platform.StateStarting {
			return nil
		}
		if err := c.plat.SetStopAction(ctx, name, spec.StopAction); err != nil {
			return err
		}
	case errors.As(err, &perr) && perr.Kind == platform.NotFound:
		// The ISO comes first: a failed package download leaves no VM.
		if err := c.writeStorageISO(ctx); err != nil {
			return err
		}
		if err := c.createStorageVM(ctx, spec); err != nil {
			return err
		}
	default:
		return err
	}
	if err := c.plat.Power(ctx, name, platform.PowerStart); err != nil {
		return err
	}
	slog.Info("storage VM started", "vm", name)
	return nil
}

// createStorageVM creates the VM with its system disk copied from the
// Debian base, its data disk, its cidata ISO and its public adapter as an
// isolated port of the PVLAN. A half-created VM is removed again; an
// existing data disk is kept, it may hold box data.
func (c *Cloud) createStorageVM(ctx context.Context, spec platform.VMSpec) (err error) {
	slog.Info("creating the storage VM", "vm", spec.Name)
	base, err := c.images.Ensure(ctx, applianceImage)
	if err != nil {
		return err
	}
	// A system disk without its VM is a leftover of an earlier failed
	// creation.
	if err := os.Remove(c.storageSystemDisk()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := c.plat.CreateDisk(ctx, c.storageSystemDisk(), storageSystemSize, base, [16]byte{}); err != nil {
		return err
	}
	if _, err := os.Stat(c.storageDataDisk()); errors.Is(err, os.ErrNotExist) {
		if err := c.plat.CreateDisk(ctx, c.storageDataDisk(), storageDataSize, "", [16]byte{}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := c.plat.CreateVM(ctx, spec); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = c.plat.DeleteVM(context.WithoutCancel(ctx), spec.Name)
		}
	}()
	if err := c.plat.AttachDisk(ctx, spec.Name, c.storageSystemDisk(), 0); err != nil {
		return err
	}
	if err := c.plat.AttachDisk(ctx, spec.Name, c.storageDataDisk(), storageDataLUN); err != nil {
		return err
	}
	mac, err := net.ParseMAC(storageMAC)
	if err != nil {
		return err
	}
	if err := c.plat.AttachNIC(ctx, spec.Name, platform.NICSpec{
		MAC:     mac,
		Segment: platform.Segment{PublicPVLAN: true},
	}); err != nil {
		return err
	}
	if err := c.plat.SetDVD(ctx, spec.Name, 0, c.storageISO()); err != nil {
		return err
	}
	return c.plat.SetBootOrder(ctx, spec.Name, []platform.BootDevice{{Kind: "disk", Slot: 0}})
}

// writeStorageISO rebuilds the storage VM's cidata ISO: the storage
// address on the public segment, the agent binary next to hlabd, the
// storage secret, the storage address for the agent and the packages of
// storage-debs.json. Without the packages the VM cannot serve boxes, so a
// failed download is an error.
func (c *Cloud) writeStorageISO(ctx context.Context) error {
	agentBin, err := os.ReadFile(filepath.Join(c.d.Paths.Programs, appliance.AgentFile))
	if err != nil {
		return fmt.Errorf("read the hlab-agent binary: %w", err)
	}
	secret, err := c.storageSecret()
	if err != nil {
		return err
	}
	files, err := c.debFiles(ctx, storageDebs)
	if err != nil {
		return fmt.Errorf("storage VM packages: %w", err)
	}
	addr := transit.For(c.set.Network.PublicRange).Storage
	files[appliance.AgentFile] = agentBin
	files[appliance.SecretFile] = []byte(secret + "\n")
	files[appliance.TransitFile] = []byte(addr.String() + "\n")
	if err := os.MkdirAll(c.storageDir(), 0o750); err != nil {
		return err
	}
	if err := os.Remove(c.storageISO()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	iface := cidata.Interface{
		MAC:       storageMAC,
		Addresses: []netip.Prefix{netip.PrefixFrom(addr, 32)},
		Gateway:   storageGateway,
	}
	// As for the appliance, the instance-id follows the user-data and the
	// network settings; the user-data keeps the SSH host keys.
	userData := appliance.StorageUserData()
	sum := sha256.Sum256(fmt.Appendf(userData, "\n%s %v %s", iface.MAC, iface.Addresses, iface.Gateway))
	return cidata.BuildFile(cidata.Seed{
		InstanceID: c.storageName() + "-" + hex.EncodeToString(sum[:6]),
		UserData:   string(userData),
		Interfaces: []cidata.Interface{iface},
		Files:      files,
	}, c.storageISO())
}

// deleteStorageVM removes the storage VM, turning it off first, and the
// folder storage\ with its disks.
func (c *Cloud) deleteStorageVM(ctx context.Context) error {
	name := c.storageName()
	var perr *platform.Error
	if err := c.plat.DeleteVM(ctx, name); err != nil && (!errors.As(err, &perr) || perr.Kind != platform.NotFound) {
		return err
	}
	if err := os.RemoveAll(c.storageDir()); err != nil {
		return fmt.Errorf("remove the storage folder: %w", err)
	}
	slog.Info("storage VM deleted", "vm", name)
	return nil
}
