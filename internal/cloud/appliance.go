//go:build windows

package cloud

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/store"
)

// The appliance VM (CONTEXT.md: Debian 13 generic, 2 vCPU, 1024 MB, 10 GB).
const (
	applianceImage    = "debian-13"
	applianceCPUs     = 2
	applianceMemoryMB = 1024
	applianceDiskSize = 10 << 30
	applianceMAC      = "02:48:4c:00:00:01"
	secretKey         = "agent.secret"
	// shutdownTimeout bounds the guest shutdown before the hard stop.
	shutdownTimeout = 60 * time.Second
	statePoll       = time.Second
)

func (c *Cloud) applianceName() string { return c.tag + "-appliance" }

func (c *Cloud) applianceDir() string {
	return filepath.Join(c.set.Storage.DataFolder, "appliance")
}

// agentSecret returns the agent's bearer secret, creating it once.
func (c *Cloud) agentSecret() (string, error) {
	var secret string
	err := c.st.Tx(func(tx store.Tx) error {
		v, err := store.GetKV(tx, secretKey)
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
		return store.SetKV(tx, secretKey, secret)
	})
	if err != nil {
		return "", fmt.Errorf("agent secret: %w", err)
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
	case errors.As(err, &perr) && perr.Kind == platform.NotFound:
		if err := c.createAppliance(ctx, spec); err != nil {
			return err
		}
	default:
		return err
	}

	if err := c.writeApplianceISO(secret); err != nil {
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
	return c.plat.AttachNIC(ctx, spec.Name, platform.NICSpec{
		MAC:     mac,
		Segment: platform.Segment{PublicPVLAN: true, Promiscuous: true},
	})
}

// writeApplianceISO rebuilds the appliance's cidata ISO: transit address,
// default route via the host, the agent binary next to hlabd and the secret.
func (c *Cloud) writeApplianceISO(secret string) error {
	agentBin, err := os.ReadFile(filepath.Join(c.d.Paths.Programs, appliance.AgentFile))
	if err != nil {
		return fmt.Errorf("read the hlab-agent binary: %w", err)
	}
	if err := os.MkdirAll(c.applianceDir(), 0o750); err != nil {
		return err
	}
	if err := os.Remove(c.isoPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return cidata.BuildFile(cidata.Seed{
		InstanceID: c.applianceName(),
		Hostname:   c.applianceName(),
		UserData:   string(appliance.UserData()),
		Interfaces: []cidata.Interface{{
			MAC:       applianceMAC,
			Addresses: []netip.Prefix{netip.PrefixFrom(applianceAddr, hostAddr.Bits())},
			Gateway:   hostAddr.Addr(),
		}},
		Files: map[string][]byte{
			appliance.AgentFile:  agentBin,
			appliance.SecretFile: []byte(secret + "\n"),
		},
	}, c.isoPath())
}

// stopAppliance shuts the appliance down through the Shutdown integration
// service and turns it off when it is not off within shutdownTimeout.
func (c *Cloud) stopAppliance(ctx context.Context) error {
	name := c.applianceName()
	if err := c.plat.Power(ctx, name, platform.PowerShutdown); err != nil {
		slog.Warn("appliance shutdown failed; turning it off", "err", err)
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
			slog.Warn("appliance did not shut down in time; turning it off")
			return c.plat.Power(ctx, name, platform.PowerStop)
		case <-t.C:
		}
	}
}
