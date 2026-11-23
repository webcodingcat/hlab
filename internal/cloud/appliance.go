//go:build windows

package cloud

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"github.com/webcodingcat/hlab/appliance"
	"github.com/webcodingcat/hlab/internal/cidata"
	"github.com/webcodingcat/hlab/internal/images"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/store"
	"github.com/webcodingcat/hlab/internal/transit"
)

// The appliance VM (CONTEXT.md: Debian 13 generic, 2 vCPU, 1024 MB, 10 GB).
const (
	applianceImage    = "debian-13"
	applianceCPUs     = 2
	applianceMemoryMB = 1024
	applianceDiskSize = 10 << 30
	applianceMAC      = "02:48:4c:00:00:01"
	// trunkMAC is the appliance's second adapter, carrying the private
	// networks' VLANs tagged; the agent finds it by this MAC.
	trunkMAC  = "02:48:4c:00:00:02"
	secretKey = "agent.secret"
	// shutdownTimeout bounds the guest shutdown before the hard stop.
	shutdownTimeout = 60 * time.Second
	statePoll       = time.Second
)

func (c *Cloud) applianceName() string { return c.tag + "-appliance" }

func (c *Cloud) applianceDir() string {
	return filepath.Join(c.set.Storage.DataFolder, "appliance")
}

// agentSecret returns the agent's bearer secret, creating it once.
func (c *Cloud) agentSecret() (string, error) { return c.kvSecret(secretKey) }

// kvSecret returns the bearer secret kept under the kv key, creating it
// once.
func (c *Cloud) kvSecret(key string) (string, error) {
	var secret string
	err := c.st.Tx(func(tx store.Tx) error {
		v, err := store.GetKV(tx, key)
		if err == nil {
			secret = v
			return nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		secret = hex.EncodeToString(b)
		return store.SetKV(tx, key, secret)
	})
	if err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
	}
	return secret, nil
}

// startAppliance creates the appliance VM if it is missing, writes its
// cidata ISO with the current agent binary and the secret, and starts it.
func (c *Cloud) startAppliance(ctx context.Context, secret string) error {
	name := c.applianceName()
	spec := platform.VMSpec{
		Name:       name,
		CPUs:       applianceCPUs,
		MemoryMB:   applianceMemoryMB,
		Folder:     c.applianceDir(),
		StopAction: platform.StopSave,
	}
	if c.set.Cloud.StopBehaviour == "shutdown" {
		spec.StopAction = platform.StopShutdown
	}

	state, err := c.plat.State(ctx, name)
	var perr *platform.Error
	switch {
	case err == nil:
		if state != platform.StateOff {
			// Left running or saved by a crash or a host shutdown; the
			// appliance keeps no state, so it boots afresh with the new ISO.
			slog.Info("turning off the appliance before its start", "state", state.String())
			if err := c.plat.Power(ctx, name, platform.PowerStop); err != nil {
				return err
			}
		}
		if err := c.plat.SetVM(ctx, name, spec); err != nil {
			return err
		}
		if err := c.ensureTrunk(ctx, name); err != nil {
			return err
		}
	case errors.As(err, &perr) && perr.Kind == platform.NotFound:
		if err := c.createAppliance(ctx, spec); err != nil {
			return err
		}
	default:
		return err
	}

	if err := c.writeApplianceISO(ctx, secret); err != nil {
		return err
	}
	if err := c.plat.SetDVD(ctx, name, 0, c.isoPath()); err != nil {
		return err
	}
	if err := c.plat.SetBootOrder(ctx, name, []platform.BootDevice{{Kind: "disk", Slot: 0}}); err != nil {
		return err
	}
	c.applianceOn = true
	if err := c.plat.Power(ctx, name, platform.PowerStart); err != nil {
		return err
	}
	slog.Info("appliance started", "vm", name)
	return nil
}

func (c *Cloud) diskPath() string { return filepath.Join(c.applianceDir(), "appliance.vhdx") }
func (c *Cloud) isoPath() string  { return filepath.Join(c.applianceDir(), "cidata.iso") }

// createAppliance creates the VM with its disk copied from the Debian base
// and its public adapter as the PVLAN's promiscuous port. A half-created VM
// is removed again.
func (c *Cloud) createAppliance(ctx context.Context, spec platform.VMSpec) (err error) {
	slog.Info("creating the appliance", "vm", spec.Name)
	base, err := c.images.Ensure(ctx, applianceImage)
	if err != nil {
		return err
	}
	// A disk without its VM is a leftover of an earlier failed creation.
	if err := os.Remove(c.diskPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := c.plat.CreateDisk(ctx, c.diskPath(), applianceDiskSize, base, [16]byte{}); err != nil {
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
	if err := c.plat.AttachDisk(ctx, spec.Name, c.diskPath(), 0); err != nil {
		return err
	}
	mac, err := net.ParseMAC(applianceMAC)
	if err != nil {
		return err
	}
	if err := c.plat.AttachNIC(ctx, spec.Name, platform.NICSpec{
		MAC:     mac,
		Segment: platform.Segment{PublicPVLAN: true, Promiscuous: true},
	}); err != nil {
		return err
	}
	return c.attachTrunk(ctx, spec.Name)
}

// attachTrunk adds the appliance's trunk adapter.
func (c *Cloud) attachTrunk(ctx context.Context, name string) error {
	mac, err := net.ParseMAC(trunkMAC)
	if err != nil {
		return err
	}
	return c.plat.AttachNIC(ctx, name, platform.NICSpec{MAC: mac, Segment: platform.Segment{Trunk: true}})
}

// ensureTrunk adds the trunk adapter to an appliance created before it
// existed. Setting its (empty) deny list finds out whether it is there.
func (c *Cloud) ensureTrunk(ctx context.Context, name string) error {
	err := c.plat.SetNICDeny(ctx, name, trunkMAC, nil)
	var perr *platform.Error
	if errors.As(err, &perr) && perr.Kind == platform.NotFound {
		slog.Info("adding the trunk adapter to the appliance", "vm", name)
		return c.attachTrunk(ctx, name)
	}
	return err
}

// debList is catalog/appliance-debs.json or catalog/storage-debs.json:
// packages the Debian image lacks, installed from the VM's cidata ISO.
type debList struct {
	Debs []struct {
		Name   string   `json:"name"`   // file <name>.deb on the ISO and in the cache
		URLs   []string `json:"urls"`   // tried in order
		SHA256 string   `json:"sha256"` // hex
	} `json:"debs"`
}

// debsDir caches the .deb files of the appliance and the storage VM.
func (c *Cloud) debsDir() string {
	return filepath.Join(c.set.Storage.DataFolder, "cache", "debs")
}

// debFiles downloads the packages of the catalog file once into the cache
// and returns them keyed by their file name on the ISO.
func (c *Cloud) debFiles(ctx context.Context, catalogFile string) (map[string][]byte, error) {
	data, err := os.ReadFile(filepath.Join(c.d.Paths.Catalog, catalogFile))
	if err != nil {
		return nil, err
	}
	var list debList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("%s: %w", catalogFile, err)
	}
	files := map[string][]byte{}
	for _, d := range list.Debs {
		file := d.Name + ".deb"
		path := filepath.Join(c.debsDir(), file)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			slog.Info("downloading a package", "name", d.Name, "list", catalogFile)
			if err := images.Fetch(ctx, nil, d.URLs, d.SHA256, path); err != nil {
				return nil, err
			}
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		files[file] = b
	}
	return files, nil
}

// writeApplianceISO rebuilds the appliance's cidata ISO: transit address
// of the public range, default route via the host, the agent binary next to
// hlabd, the secret, the transit address for the agent and the cached
// appliance packages. Without the packages the appliance still starts;
// formatting with xfs then fails with a clear message.
func (c *Cloud) writeApplianceISO(ctx context.Context, secret string) error {
	agentBin, err := os.ReadFile(filepath.Join(c.d.Paths.Programs, appliance.AgentFile))
	if err != nil {
		return fmt.Errorf("read the hlab-agent binary: %w", err)
	}
	files, err := c.debFiles(ctx, "appliance-debs.json")
	if err != nil {
		slog.Warn("appliance packages unavailable; xfs volumes cannot be formatted", "err", err)
		files = map[string][]byte{}
	}
	addrs := transit.For(c.set.Network.PublicRange)
	files[appliance.AgentFile] = agentBin
	files[appliance.SecretFile] = []byte(secret + "\n")
	files[appliance.TransitFile] = []byte(addrs.Appliance.String() + "\n")
	if err := os.MkdirAll(c.applianceDir(), 0o750); err != nil {
		return err
	}
	if err := os.Remove(c.isoPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	iface := cidata.Interface{
		MAC:       applianceMAC,
		Addresses: []netip.Prefix{netip.PrefixFrom(addrs.Appliance, addrs.Host.Bits())},
		Gateway:   addrs.Host.Addr(),
	}
	// The instance-id carries a hash of the user-data and the network
	// settings, so cloud-init treats an existing appliance as a new instance
	// once and writes the changed unit, install script and network-config;
	// the appliance keeps no state that this loses.
	userData := appliance.UserData()
	sum := sha256.Sum256(fmt.Appendf(userData, "\n%s %v %s", iface.MAC, iface.Addresses, iface.Gateway))
	return cidata.BuildFile(cidata.Seed{
		InstanceID: c.applianceName() + "-" + hex.EncodeToString(sum[:6]),
		Hostname:   c.applianceName(),
		UserData:   string(userData),
		Interfaces: []cidata.Interface{iface},
		Files:      files,
	}, c.isoPath())
}

// stopAppliance shuts the appliance down.
func (c *Cloud) stopAppliance(ctx context.Context) error {
	return c.shutdownVM(ctx, c.applianceName())
}

// shutdownVM shuts VM name down through the Shutdown integration service
// and turns it off when it is not off within shutdownTimeout.
func (c *Cloud) shutdownVM(ctx context.Context, name string) error {
	if err := c.plat.Power(ctx, name, platform.PowerShutdown); err != nil {
		slog.Warn("VM shutdown failed; turning it off", "vm", name, "err", err)
		return c.plat.Power(ctx, name, platform.PowerStop)
	}
	sctx, cancel := context.WithTimeout(ctx, shutdownTimeout)
	defer cancel()
	t := time.NewTicker(statePoll)
	defer t.Stop()
	for {
		st, err := c.plat.State(sctx, name)
		if err == nil && st == platform.StateOff {
			return nil
		}
		select {
		case <-sctx.Done():
			slog.Warn("VM did not shut down in time; turning it off", "vm", name)
			return c.plat.Power(ctx, name, platform.PowerStop)
		case <-t.C:
		}
	}
}
