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
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/webcodingcat/hlab/internal/agent"
	"github.com/webcodingcat/hlab/internal/version"
)

// listenAddr is the agent interface on the transit address; publicInterface
// is the appliance adapter on the public segment.
const (
	listenAddr      = "198.19.0.1:9100"
	publicInterface = "eth0"
)

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
	// The network applier adds the addresses the DNS server binds, so it
	// comes first.
	a, err := agent.New(bytes.TrimSpace(raw),
		agent.NewNetwork(publicInterface), agent.NewIsolation(publicInterface), dns, proxy)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{Addr: listenAddr, Handler: a.Handler(), ReadHeaderTimeout: 30 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("hlab-agent started", "version", version.Version, "listen", listenAddr)

	select {
	case err := <-errc:
		_ = proxy.Close(context.Background())
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Warn("shutdown", "err", err)
	}
	return proxy.Close(shutdown)
}
