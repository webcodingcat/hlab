package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/mdlayher/vsock"

	"github.com/webcodingcat/hlab/internal/agent"
	"github.com/webcodingcat/hlab/internal/agentproto"
)

// The appliance's fixed places, set up by its image.
const (
	stateDir        = "/run/hlab" // the unit's RuntimeDirectory
	metadataAddress = "169.254.169.254:80"
)

// run serves hlabd on vsock and the servers on the metadata address, until
// systemd stops the agent.
func run() error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	hlabAgent := agent.New(agent.Options{
		Version: version,
		Dir:     stateDir,
		RestartDHCP: func(device string) []string {
			return []string{"systemctl", "restart", "hlab-dnsmasq@" + device + ".service"}
		},
		StopDHCP: func(device string) []string {
			return []string{"systemctl", "stop", "hlab-dnsmasq@" + device + ".service"}
		},
		Log: log,
	})
	control, err := vsock.Listen(agentproto.Port, nil)
	if err != nil {
		return fmt.Errorf("listening on vsock port %d: %w", agentproto.Port, err)
	}
	metadata, err := agent.ListenMetadata(metadataAddress)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", metadataAddress, err)
	}
	failed := make(chan error, 3)
	go func() { failed <- hlabAgent.WatchCarrier(ctx) }()
	go func() { failed <- http.Serve(control, hlabAgent.Handler()) }()
	go func() { failed <- hlabAgent.MetadataServer().Serve(metadata) }()
	log.Info("hlab-agent started", "version", version, "vsock_port", agentproto.Port)
	select {
	case <-ctx.Done():
		return nil
	case err := <-failed:
		return errors.Join(errors.New("stopped"), err)
	}
}
