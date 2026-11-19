// Command hlab-agent runs inside the hlab appliance VM. It serves the agent
// interface for hlabd and applies the pushed network document.
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
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/webcodingcat/hlab/internal/agent"
	"github.com/webcodingcat/hlab/internal/version"
)

// listenAddr is the agent interface on the transit address; metadataAddr is
// the metadata service; publicInterface is the appliance adapter on the
// public segment.
const (
	listenAddr      = "198.19.0.1:9100"
	metadataAddr    = "169.254.169.254:80"
	publicInterface = "eth0"
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
	flag.Parse()

	raw, err := os.ReadFile(*secretFile)
	if err != nil {
		return fmt.Errorf("read secret: %w", err)
	}
	proxy := &agent.APIProxy{}
	dns := &agent.DNS{}
	defer dns.Close()
	metadata := agent.NewMetadata()
	// The network applier adds the addresses the DNS server binds, so it
	// comes first; the VRF applier needs its IPv4 forwarding.
	a, err := agent.New(bytes.TrimSpace(raw),
		agent.NewNetwork(publicInterface), agent.NewVRF(trunkMAC), agent.NewIsolation(publicInterface), dns, proxy, metadata)
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
