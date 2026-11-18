package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// APIProxy is the Applier of the reverse proxy to the hlabd API
// (Config.API): it listens on Listen and forwards every request to Backend,
// keeping the Host and Authorization headers of the client. It restarts when
// the document changes either address.
type APIProxy struct {
	mu      sync.Mutex
	current agentapi.APIProxy
	srv     *http.Server
}

// Name implements Applier.
func (p *APIProxy) Name() string { return "api-proxy" }

// Apply implements Applier.
func (p *APIProxy) Apply(ctx context.Context, cfg agentapi.Config) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.srv != nil && cfg.API == p.current {
		return nil
	}
	if err := p.stop(ctx); err != nil {
		return err
	}
	if !cfg.API.Listen.IsValid() {
		return nil
	}
	if !cfg.API.Backend.IsValid() {
		return errors.New("api proxy: listen address without backend")
	}

	target := &url.URL{Scheme: "http", Host: cfg.API.Backend.String()}
	rp := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.Host = r.In.Host
		},
	}
	ln, err := net.Listen("tcp", cfg.API.Listen.String())
	if err != nil {
		return fmt.Errorf("api proxy: %w", err)
	}
	srv := &http.Server{Handler: rp, ReadHeaderTimeout: 30 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("api proxy stopped", "err", err)
		}
	}()
	p.srv, p.current = srv, cfg.API
	slog.Info("api proxy started", "listen", cfg.API.Listen, "backend", cfg.API.Backend)
	return nil
}

// Close stops the proxy.
func (p *APIProxy) Close(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stop(ctx)
}

func (p *APIProxy) stop(ctx context.Context) error {
	if p.srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := p.srv.Shutdown(ctx)
	if err != nil {
		// Requests still running after the timeout are cut off; the
		// listener is closed either way.
		err = p.srv.Close()
	}
	p.srv, p.current = nil, agentapi.APIProxy{}
	if err != nil {
		return fmt.Errorf("api proxy: stop: %w", err)
	}
	return nil
}
