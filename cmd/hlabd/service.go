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
	"sync/atomic"
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

// service is the running hlabd: the lab and its three HTTP servers.
type service struct {
	opts      serviceOptions
	lab       *lab.Lab
	servers   []*http.Server
	tokenFile string
	status    labapi.Status
	stopping  atomic.Bool

	shutdownOnce sync.Once
	shutdown     chan struct{} // closed by POST /_lab/shutdown
	failed       chan error    // a server that stopped on its own
}

// start opens the lab, writes the control token and starts serving.
func start(opts serviceOptions) (*service, error) {
	cfg, err := config.Load(opts.DataDir)
	if err != nil {
		return nil, err
	}
	offer, err := catalog.Load(filepath.Join(opts.DataDir, catalog.UserFile))
	if err != nil {
		return nil, err
	}
	_, err = os.Stat(filepath.Join(opts.DataDir, lab.StateFile))
	newLab := errors.Is(err, os.ErrNotExist)
	l, err := lab.Open(lab.Options{
		DataDir:   opts.DataDir,
		TimeScale: cfg.TimeScale,
		Logger:    opts.Logger,
		Catalog:   offer,
	}, system.New(opts.Parts))
	if err != nil {
		return nil, err
	}
	svc := &service{
		opts:      opts,
		lab:       l,
		tokenFile: filepath.Join(opts.DataDir, labapi.TokenFile),
		shutdown:  make(chan struct{}),
		failed:    make(chan error, 3),
	}
	if err := svc.serve(cfg, newLab); err != nil {
		_ = svc.close() // the error that stopped the start says more
		return nil, err
	}
	return svc, nil
}

// serve creates the default project of a new lab, listens on the ports,
// writes the control token and starts the servers.
func (s *service) serve(cfg config.Config, newLab bool) error {
	if newLab {
		if _, err := s.lab.CreateProject(defaultProject, state.Limits{}); err != nil {
			return err
		}
	}
	var listeners []net.Listener
	for _, port := range []int{cfg.Ports.Cloud, cfg.Ports.Hetzner, cfg.Ports.Control} {
		listener, err := net.Listen("tcp", net.JoinHostPort(s.opts.Host, strconv.Itoa(port)))
		if err != nil {
			for _, open := range listeners {
				_ = open.Close()
			}
			return err
		}
		listeners = append(listeners, listener)
	}
	token := newControlToken()
	if err := s.opts.Parts.Lifecycle.WriteToken(s.tokenFile, []byte(token)); err != nil {
		for _, open := range listeners {
			_ = open.Close()
		}
		return fmt.Errorf("writing the control token: %w", err)
	}
	controlURL := "http://" + listeners[2].Addr().String()
	s.status = labapi.Status{
		Version:  s.opts.Version,
		Platform: s.opts.Platform,
		DataDir:  s.opts.DataDir,
		Started:  s.lab.Now(),
		Endpoints: labapi.Endpoints{
			Cloud:   "http://" + listeners[0].Addr().String() + web.Prefix,
			Hetzner: "http://" + listeners[1].Addr().String() + web.Prefix,
			Control: controlURL + labapi.Prefix,
			WebUI:   controlURL,
		},
	}
	handlers := s.handlers(token, listeners[2].Addr().String())
	for i, listener := range listeners {
		server := &http.Server{Handler: handlers[i], ReadHeaderTimeout: 10 * time.Second}
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
// with the web UI.
func (s *service) handlers(token, controlAddress string) []http.Handler {
	cloudAPI := web.NewAPI(web.Options{Backend: s.lab, RateLimit: web.HetznerRateLimit, Logger: s.opts.Logger})
	cloud.Register(cloudAPI, s.lab)
	hetznerAPI := web.NewAPI(web.Options{Backend: s.lab, RateLimit: web.HetznerRateLimit, Logger: s.opts.Logger})
	ui := webui.New([]string{controlAddress}, s.opts.Logger)
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
	mux.Handle(labapi.Prefix+"/", labControl)
	mux.Handle("/", ui.Handler())
	return []http.Handler{cloudAPI, hetznerAPI, ui.Guard(mux)}
}

// currentStatus returns the status for GET /_lab/status.
func (s *service) currentStatus() labapi.Status {
	status := s.status
	status.State = labapi.StateRunning
	if s.stopping.Load() {
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

// close stops the servers, letting running requests finish, removes the
// control token, so that the CLI knows the lab is gone, and closes the lab.
func (s *service) close() error {
	s.stopping.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	for _, server := range s.servers {
		_ = server.Shutdown(ctx) // after the timeout, open connections just close
	}
	if err := os.Remove(s.tokenFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.opts.Logger.Warn("removing the control token", "error", err)
	}
	return s.lab.Close()
}

// newControlToken returns a new random control token.
func newControlToken() string {
	random := make([]byte, 32)
	_, _ = rand.Read(random) // never fails, as its documentation says
	return hex.EncodeToString(random)
}
