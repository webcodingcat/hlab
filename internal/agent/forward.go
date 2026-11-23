package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// ConsoleForward is the Applier of the plain TCP forward to the hlabd server
// console (Config.Console): it listens on Listen and copies every
// connection both ways to Backend, so TLS passes through untouched. It
// restarts when the document changes either address and runs nothing while
// Listen is invalid.
type ConsoleForward struct {
	mu      sync.Mutex
	current agentapi.ConsoleForward
	ln      net.Listener
	conns   map[net.Conn]struct{}
	wg      sync.WaitGroup
}

// Name implements Applier.
func (f *ConsoleForward) Name() string { return "console-forward" }

// Apply implements Applier.
func (f *ConsoleForward) Apply(_ context.Context, cfg agentapi.Config) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ln != nil && cfg.Console == f.current {
		return nil
	}
	f.stop()
	if !cfg.Console.Listen.IsValid() {
		return nil
	}
	if !cfg.Console.Backend.IsValid() {
		return errors.New("console forward: listen address without backend")
	}
	ln, err := net.Listen("tcp", cfg.Console.Listen.String())
	if err != nil {
		return fmt.Errorf("console forward: %w", err)
	}
	f.ln, f.current = ln, cfg.Console
	if f.conns == nil {
		f.conns = map[net.Conn]struct{}{}
	}
	f.wg.Add(1)
	go f.accept(ln, cfg.Console.Backend.String())
	slog.Info("console forward started", "listen", cfg.Console.Listen, "backend", cfg.Console.Backend)
	return nil
}

// Close stops the forward and ends its open connections.
func (f *ConsoleForward) Close() {
	f.mu.Lock()
	f.stop()
	f.mu.Unlock()
	f.wg.Wait()
}

// stop closes the listener and the open connections; f.mu is held.
func (f *ConsoleForward) stop() {
	if f.ln == nil {
		return
	}
	_ = f.ln.Close()
	for c := range f.conns {
		_ = c.Close()
	}
	clear(f.conns)
	f.ln, f.current = nil, agentapi.ConsoleForward{}
}

func (f *ConsoleForward) accept(ln net.Listener, backend string) {
	defer f.wg.Done()
	for {
		c, err := ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				slog.Error("console forward stopped", "err", err)
			}
			return
		}
		f.wg.Add(1)
		go f.serve(ln, c, backend)
	}
}

// serve copies c to backend and back until either side ends.
func (f *ConsoleForward) serve(ln net.Listener, c net.Conn, backend string) {
	defer f.wg.Done()
	defer func() { _ = c.Close() }()
	b, err := net.DialTimeout("tcp", backend, 10*time.Second)
	if err != nil {
		slog.Warn("console forward: dial backend", "backend", backend, "err", err)
		return
	}
	defer func() { _ = b.Close() }()
	if !f.track(ln, c, b) {
		return
	}
	defer f.untrack(c, b)
	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		done <- struct{}{}
	}
	go pipe(b, c)
	go pipe(c, b)
	<-done
}

// track registers both sides of a connection unless its listener was
// stopped meanwhile.
func (f *ConsoleForward) track(ln net.Listener, conns ...net.Conn) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ln != ln {
		return false
	}
	for _, c := range conns {
		f.conns[c] = struct{}{}
	}
	return true
}

func (f *ConsoleForward) untrack(conns ...net.Conn) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range conns {
		delete(f.conns, c)
	}
}
