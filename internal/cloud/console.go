//go:build windows

package cloud

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/webcodingcat/hlab/internal/console"
	"github.com/webcodingcat/hlab/internal/platform"
)

// consoleHost is the name in the console URLs; the agent's DNS answers it
// with the appliance's forward to the console listener.
const consoleHost = "console.hetzner.internal"

// newConsole issues the console's certificate from the hlab CA, kept in
// memory only, and builds the console service on the platform's console.
func (c *Cloud) newConsole() error {
	certPEM, keyPEM, err := c.ca.Issue([]string{consoleHost})
	if err != nil {
		return fmt.Errorf("console certificate: %w", err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("console certificate: %w", err)
	}
	pc, ok := c.plat.(platform.Console)
	if !ok {
		return errors.New("platform has no console")
	}
	c.console = console.New(console.Config{
		Host: consoleHost,
		Cert: cert,
		Open: pc.OpenConsole,
	})
	return nil
}

// serveConsole serves the console on its transit address.
func (c *Cloud) serveConsole() error {
	ln, err := net.Listen("tcp4", c.addrs.ConsoleListen.String())
	if err != nil {
		return fmt.Errorf("listen on %s: %w", c.addrs.ConsoleListen, err)
	}
	go func(s *console.Service) {
		if err := s.Serve(ln); err != nil {
			slog.Error("server ended", "server", "console", "err", err)
		}
	}(c.console)
	slog.Info("console listening", "addr", c.addrs.ConsoleListen.String())
	return nil
}

// closeConsole ends the console listener and every open console.
func (c *Cloud) closeConsole() {
	if c.console == nil {
		return
	}
	if err := c.console.Close(); err != nil {
		slog.Warn("close the console", "err", err)
	}
	c.console = nil
}
