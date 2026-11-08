package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/api/cloud"
	"github.com/webcodingcat/hlab/internal/api/control"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/config"
	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/labapi"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/system"
	"github.com/webcodingcat/hlab/internal/web"
	"github.com/webcodingcat/hlab/internal/webui"
)

// defaultProject is the project a new lab starts with, so that `hlab env`
// works without a project being created first.
const defaultProject = "default"

// stopTimeout is how long the service waits for running requests when it
// stops.
const stopTimeout = 10 * time.Second

// service is the running hlabd: the lab and its HTTP servers.
type service struct {
	opts      serviceOptions
	lab       *lab.Lab
	servers   []*http.Server
	tokenFile string
	status    labapi.Status

	// stoppingMu makes setting stopping and counting a request one step,
	// so that close waits for every request it let through
	stoppingMu sync.RWMutex
	stopping   bool
	requests   sync.WaitGroup // the requests that reach the lab

	shutdownOnce sync.Once
	shutdown     chan struct{} // closed by POST /_lab/shutdown
	failed       chan error    // a server that stopped on its own
}

// start opens the lab, starts the machine's side, writes the control token
// and starts serving.
func start(opts serviceOptions) (*service, error) {
	cfg, err := config.Load(opts.DataDir)
	if err != nil {
		return nil, err
	}
	offer, err := catalog.Load(filepath.Join(opts.DataDir, catalog.UserFile))
	if err != nil {
		return nil, err
	}
	l, err := lab.Open(lab.Options{
		DataDir:   opts.DataDir,
		TimeScale: cfg.TimeScale,
		Logger:    opts.Logger,
		Catalog:   offer,
	}, system.New(opts.Parts))
	if err != nil {
		return nil, err
	}
	// the host link comes first: the APIs listen on it
	ctx, cancel := stopContext(opts.Parts.Lifecycle)
	defer cancel()
	if err := l.StartMachine(ctx, opts.Machine); err != nil {
		_ = l.Close() // the error that stopped the start says more
		return nil, err
	}
	svc := &service{
		opts:      opts,
		lab:       l,
		tokenFile: filepath.Join(opts.DataDir, labapi.TokenFile),
		shutdown:  make(chan struct{}),
		failed:    make(chan error, 2+len(opts.Hosts)), // one per server
	}
	if err := svc.serve(cfg); err != nil {
		_ = svc.close() // the error that stopped the start says more
		return nil, err
	}
	return svc, nil
}

// serve listens on the ports, creates the default project of a new lab,
// writes the control token and starts the servers. The ports come first, so
// that a start that fails on a taken port, the common case, leaves a new lab
// new.
func (s *service) serve(cfg config.Config) error {
	// in the order of the handlers: Cloud API, Hetzner API, control; then
	// the control on the other hosts
	var listeners []net.Listener
	closeListeners := func() {
		for _, open := range listeners {
			_ = open.Close()
		}
	}
	addresses := []string{}
	for _, port := range []int{cfg.Ports.Cloud, cfg.Ports.Hetzner, cfg.Ports.Control} {
		addresses = append(addresses, net.JoinHostPort(s.opts.Hosts[0], strconv.Itoa(port)))
	}
	for _, host := range s.opts.Hosts[1:] {
		addresses = append(addresses, net.JoinHostPort(host, strconv.Itoa(cfg.Ports.Control)))
	}
	for _, address := range addresses {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			closeListeners()
			return err
		}
		listeners = append(listeners, listener)
	}
	if s.lab.Fresh() {
		if _, err := s.lab.CreateProject(defaultProject, state.Limits{}); err != nil {
			closeListeners()
			return err
		}
	}
	token := newControlToken()
	if err := s.opts.Parts.Lifecycle.WriteSecret(s.tokenFile, []byte(token)); err != nil {
		closeListeners()
		return fmt.Errorf("writing the control token: %w", err)
	}
	cloudAddress := listeners[0].Addr().String()
	hetznerAddress := listeners[1].Addr().String()
	controlAddress := listeners[2].Addr().String()
	controlURL := "http://" + controlAddress
	s.status = labapi.Status{
		Version:  s.opts.Version,
		Platform: s.opts.Platform,
		DataDir:  s.opts.DataDir,
		Started:  s.lab.Now(),
		Endpoints: labapi.Endpoints{
			Cloud:   "http://" + cloudAddress + web.Prefix,
			Hetzner: "http://" + hetznerAddress + web.Prefix,
			Control: controlURL + labapi.Prefix,
			WebUI:   controlURL,
		},
	}
	controlAddresses := []string{}
	for _, listener := range listeners[2:] {
		controlAddresses = append(controlAddresses, listener.Addr().String())
	}
	handlers := s.handlers(token, controlAddresses)
	for i, listener := range listeners {
		handler := handlers[min(i, len(handlers)-1)] // the control's on the other hosts
		server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
		s.servers = append(s.servers, server)
		go func() {
			if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
				s.failed <- err
			}
		}()
	}
	s.opts.Logger.Info("hlabd is running", "data", s.opts.DataDir, "platform", s.opts.Platform,
		"cloud_api", s.status.Endpoints.Cloud, "web_ui", s.status.Endpoints.WebUI)
	s.opts.Logger.Info("for the CLI: export " + "HLAB_CONTROL=" + controlURL + " HLAB_TOKEN_FILE=" + s.tokenFile)
	return nil
}

// handlers returns the handlers of the three ports: the Cloud API, the
// Hetzner API (its routes come with the Storage Boxes) and the lab control
// with the web UI. While the lab stops, they answer 503, but for the lab
// control's status and shutdown.
func (s *service) handlers(token string, controlAddresses []string) []http.Handler {
	cloudAPI := web.NewAPI(web.Options{Backend: s.lab, RateLimit: web.HetznerRateLimit, Logger: s.opts.Logger})
	cloud.Register(cloudAPI, s.lab)
	hetznerAPI := web.NewAPI(web.Options{Backend: s.lab, RateLimit: web.HetznerRateLimit, Logger: s.opts.Logger})
	ui := webui.New(controlAddresses, s.opts.Logger)
	labControl := control.New(control.Options{
		Lab:      s.lab,
		Cloud:    cloudAPI,
		Token:    token,
		Admit:    ui.Admits,
		Status:   s.currentStatus,
		Shutdown: s.requestShutdown,
		Logger:   s.opts.Logger,
	})
	mux := http.NewServeMux()
	mux.Handle(labapi.Prefix+"/", s.refuseWhileStopping(labControl, func(r *http.Request) bool {
		return r.URL.Path == labapi.Prefix+"/status" || r.URL.Path == labapi.Prefix+"/shutdown"
	}))
	mux.Handle("/", ui.Handler())
	return []http.Handler{
		s.refuseWhileStopping(cloudAPI, nil),
		s.refuseWhileStopping(hetznerAPI, nil),
		ui.Guard(mux),
	}
}

// refuseWhileStopping answers 503 unavailable once the lab stops, as
// Hetzner's API does while it is down, and counts the requests it lets
// through, so that close waits for them before it closes the lab. Requests
// that always passes go through even then, uncounted: they do not change
// the lab.
func (s *service) refuseWhileStopping(next http.Handler, always func(*http.Request) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if always != nil && always(r) {
			next.ServeHTTP(w, r)
			return
		}
		s.stoppingMu.RLock()
		if s.stopping {
			s.stoppingMu.RUnlock()
			web.WriteError(w, s.opts.Logger, &web.Error{
				Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "HLab is stopping",
			})
			return
		}
		s.requests.Add(1)
		s.stoppingMu.RUnlock()
		defer s.requests.Done()
		next.ServeHTTP(w, r)
	})
}

// currentStatus returns the status for GET /_lab/status.
func (s *service) currentStatus() labapi.Status {
	status := s.status
	status.State = labapi.StateRunning
	s.stoppingMu.RLock()
	defer s.stoppingMu.RUnlock()
	if s.stopping {
		status.State = labapi.StateStopping
	}
	return status
}

// requestShutdown makes wait return, after the answer to the request.
func (s *service) requestShutdown() {
	s.shutdownOnce.Do(func() { close(s.shutdown) })
}

// wait runs the service until it is asked to stop (the lab control, Ctrl+C,
// the machine) or a server fails, and then stops it.
func (s *service) wait() error {
	var failure error
	select {
	case <-s.shutdown:
		s.opts.Logger.Info("stopping: the lab control asked to")
	case <-s.opts.Parts.Lifecycle.StopRequests():
		s.opts.Logger.Info("stopping: the machine asked to")
	case failure = <-s.failed:
		s.opts.Logger.Error("stopping: a server failed", "error", failure)
	}
	if err := s.close(); err != nil && failure == nil {
		failure = err
	}
	return failure
}

// close stops the lab (13.2): new requests get 503 while the running ones
// finish (up to stopTimeout); the lab closes, with the appliance and the
// host link, while the lab control still reports "stopping"; then the
// servers stop and the control token goes, so that the CLI knows the lab is
// gone.
func (s *service) close() error {
	s.stoppingMu.Lock()
	s.stopping = true
	s.stoppingMu.Unlock()
	finished := make(chan struct{})
	go func() {
		s.requests.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(stopTimeout):
		s.opts.Logger.Warn("closing the lab with requests still running", "waited", stopTimeout)
	}
	labErr := s.lab.Close()
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	for _, server := range s.servers {
		_ = server.Shutdown(ctx) // after the timeout, open connections just close
	}
	if err := os.Remove(s.tokenFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.opts.Logger.Warn("removing the control token", "error", err)
	}
	return labErr
}

// newControlToken returns a new random control token.
func newControlToken() string {
	random := make([]byte, 32)
	_, _ = rand.Read(random) // never fails, as its documentation says
	return hex.EncodeToString(random)
}
