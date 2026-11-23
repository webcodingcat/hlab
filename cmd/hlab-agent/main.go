// Command hlab-agent runs inside the hlab appliance VM, where it serves the
// agent interface for hlabd and applies the pushed network document, and
// with --role storage inside the storage VM, where it applies the storage
// document.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/webcodingcat/hlab/internal/agent"
	"github.com/webcodingcat/hlab/internal/storageagent"
	"github.com/webcodingcat/hlab/internal/version"
)

// listenPort is the agent interface's port on the transit address;
// metadataAddr is the metadata service; publicInterface is the appliance
// adapter on the public segment.
const (
	listenPort      = 9100
	metadataAddr    = "169.254.169.254:80"
	publicInterface = "eth0"
	lbDir           = "/run/hlab"
	// The storage VM's data disk sits at SCSI LUN 1 and is mounted at
	// storageRoot.
	dataDiskLUN = 1
	storageRoot = "/srv/sbox"
)

// trunkMAC is the MAC of the appliance's second adapter, the trunk that
// carries the private networks' VLANs.
var trunkMAC = net.HardwareAddr{0x02, 0x48, 0x4c, 0x00, 0x00, 0x02}

func main() {
	if err := run(); err != nil {
		slog.Error("hlab-agent", "err", err)
		os.Exit(1)
	}
}

func run() error {
	secretFile := flag.String("secret", "/etc/hlab-agent/secret", "file holding the bearer secret")
	transitFile := flag.String("transit", "/etc/hlab-agent/transit", "file holding the agent's listen address")
	role := flag.String("role", "appliance", "appliance or storage")
	flag.Parse()

	raw, err := os.ReadFile(*secretFile)
	if err != nil {
		return fmt.Errorf("read secret: %w", err)
	}
	rawTransit, err := os.ReadFile(*transitFile)
	if err != nil {
		return fmt.Errorf("read transit address: %w", err)
	}
	transit, err := netip.ParseAddr(string(bytes.TrimSpace(rawTransit)))
	if err != nil || !transit.Is4() {
		return fmt.Errorf("transit address %q is no IPv4 address", bytes.TrimSpace(rawTransit))
	}
	listenAddr := netip.AddrPortFrom(transit, listenPort).String()
	secret := bytes.TrimSpace(raw)
	switch *role {
	case "appliance":
		return runAppliance(secret, listenAddr)
	case "storage":
		return runStorage(secret, listenAddr)
	default:
		return fmt.Errorf("unknown role %q", *role)
	}
}

// runStorage serves the storage agent until a signal ends it.
func runStorage(secret []byte, listenAddr string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	disk, err := agent.FindDisk(ctx, dataDiskLUN)
	if err != nil {
		return fmt.Errorf("data disk: %w", err)
	}
	a, err := storageagent.New(secret, storageagent.Options{DataDisk: disk, Root: storageRoot})
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: listenAddr, Handler: a.Handler(), ReadHeaderTimeout: 30 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("hlab-agent started", "version", version.Version, "role", "storage", "listen", listenAddr, "disk", disk)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Warn("shutdown", "err", err)
	}
	return nil
}

// runAppliance serves the appliance agent and the metadata service until
// a signal ends them.
func runAppliance(secret []byte, listenAddr string) error {
	proxy := &agent.APIProxy{}
	dns := &agent.DNS{}
	defer dns.Close()
	metadata := agent.NewMetadata()
	// The VRF applier runs one metadata listener per private network.
	vrf := agent.NewVRF(trunkMAC)
	vrf.Metadata = metadata
	// The network applier adds the addresses the DNS server binds, so it
	// comes first; the VRF applier needs its IPv4 forwarding, and the load
	// balancers enslave their network veths to its VRF devices.
	a, err := agent.New(secret,
		agent.NewNetwork(publicInterface), vrf, agent.NewLoadBalancers(lbDir), agent.NewIsolation(publicInterface), agent.NewFirewall(publicInterface), dns, proxy, metadata)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The network applier adds the metadata address with the first
	// document, so the metadata listener binds before the address exists.
	mdln, err := listenFreebind(metadataAddr)
	if err != nil {
		return fmt.Errorf("metadata service: %w", err)
	}
	mdsrv := &http.Server{Handler: metadata, ReadHeaderTimeout: 30 * time.Second}
	srv := &http.Server{Addr: listenAddr, Handler: a.Handler(), ReadHeaderTimeout: 30 * time.Second}
	errc := make(chan error, 2)
	go func() { errc <- srv.ListenAndServe() }()
	go func() { errc <- mdsrv.Serve(mdln) }()
	slog.Info("hlab-agent started", "version", version.Version, "listen", listenAddr, "metadata", metadataAddr)

	select {
	case err := <-errc:
		_ = srv.Close()
		_ = mdsrv.Close()
		_ = proxy.Close(context.Background())
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, s := range []*http.Server{srv, mdsrv} {
		if err := s.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Warn("shutdown", "err", err)
		}
	}
	return proxy.Close(shutdown)
}
