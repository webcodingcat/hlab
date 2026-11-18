//go:build windows

package windows

import (
	"context"
	"fmt"
	"time"

	xwin "golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"

	"github.com/webcodingcat/hlab/internal/profile"
	"github.com/webcodingcat/hlab/internal/sysint"
)

// ServiceControl controls one service with the minimal rights an
// interactive user has (E10): never svc/mgr.Connect, which asks for more.
type ServiceControl struct {
	name string
}

var _ sysint.ServiceControl = (*ServiceControl)(nil)

// NewServiceControl controls the service name, for example
// profile.Current().ServiceName or profile.Other().ServiceName.
func NewServiceControl(name string) *ServiceControl {
	return &ServiceControl{name: name}
}

// open opens the service with access and calls fn with its handle.
func (c *ServiceControl) open(access uint32, fn func(h xwin.Handle) error) error {
	m, err := xwin.OpenSCManager(nil, nil, xwin.SC_MANAGER_CONNECT)
	if err != nil {
		return fmt.Errorf("connect to the service manager: %w", err)
	}
	defer func() { _ = xwin.CloseServiceHandle(m) }()
	name, err := xwin.UTF16PtrFromString(c.name)
	if err != nil {
		return err
	}
	h, err := xwin.OpenService(m, name, access)
	if err != nil {
		return fmt.Errorf("open service %s: %w", c.name, err)
	}
	defer func() { _ = xwin.CloseServiceHandle(h) }()
	return fn(h)
}

// Start asks the SCM to start the service with params as start parameters.
func (c *ServiceControl) Start(_ context.Context, params ...string) error {
	argv := make([]*uint16, 0, len(params))
	for _, p := range params {
		a, err := xwin.UTF16PtrFromString(p)
		if err != nil {
			return err
		}
		argv = append(argv, a)
	}
	return c.open(xwin.SERVICE_START, func(h xwin.Handle) error {
		var first **uint16
		if len(argv) > 0 {
			first = &argv[0]
		}
		if err := xwin.StartService(h, uint32(len(argv)), first); err != nil {
			return fmt.Errorf("start service %s: %w", c.name, err)
		}
		return nil
	})
}

// Stop sends the stop control to the service.
func (c *ServiceControl) Stop(context.Context) error {
	return c.open(xwin.SERVICE_STOP, func(h xwin.Handle) error {
		var st xwin.SERVICE_STATUS
		if err := xwin.ControlService(h, xwin.SERVICE_CONTROL_STOP, &st); err != nil {
			return fmt.Errorf("stop service %s: %w", c.name, err)
		}
		return nil
	})
}

// State returns the state of the service.
func (c *ServiceControl) State(context.Context) (string, error) {
	var state string
	err := c.open(xwin.SERVICE_QUERY_STATUS, func(h xwin.Handle) error {
		var st xwin.SERVICE_STATUS
		if err := xwin.QueryServiceStatus(h, &st); err != nil {
			return fmt.Errorf("query service %s: %w", c.name, err)
		}
		switch st.CurrentState {
		case xwin.SERVICE_STOPPED:
			state = sysint.StateStopped
		case xwin.SERVICE_START_PENDING:
			state = sysint.StateStarting
		case xwin.SERVICE_STOP_PENDING:
			state = sysint.StateStopping
		default: // running; hlabd never pauses
			state = sysint.StateRunning
		}
		return nil
	})
	return state, err
}

// ServiceHost runs hlabd under the SCM; it accepts stop and preshutdown.
type ServiceHost struct {
	name string
}

var _ sysint.ServiceHost = (*ServiceHost)(nil)

// NewServiceHost runs the service of the current profile.
func NewServiceHost() *ServiceHost {
	return &ServiceHost{name: profile.Current().ServiceName}
}

// waitHint is the time the SCM waits for the next checkpoint while the
// service starts or stops; checkpoints are sent more often than that.
const (
	waitHint      = 30 * time.Second
	checkpointGap = 10 * time.Second
)

// Run runs the service; see sysint.ServiceHost.
func (h *ServiceHost) Run(start func(ctx context.Context, params []string) error, stop func(ctx context.Context, reason sysint.StopReason) error) error {
	hd := &handler{start: start, stop: stop}
	if err := svc.Run(h.name, hd); err != nil {
		return err
	}
	return hd.err
}

type handler struct {
	start func(ctx context.Context, params []string) error
	stop  func(ctx context.Context, reason sysint.StopReason) error
	err   error
}

// Execute implements svc.Handler.
func (h *handler) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var params []string
	if len(args) > 1 {
		params = args[1:]
	}
	if err := pending(svc.StartPending, r, s, func() error { return h.start(ctx, params) }); err != nil {
		h.err = fmt.Errorf("start: %w", err)
		return true, 1
	}

	s <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptPreShutdown}
	reason := sysint.StopRequested
loop:
	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			s <- c.CurrentStatus
		case svc.Stop:
			break loop
		case svc.PreShutdown:
			reason = sysint.StopHostShutdown
			break loop
		}
	}

	if err := pending(svc.StopPending, r, s, func() error { return h.stop(ctx, reason) }); err != nil {
		h.err = fmt.Errorf("stop: %w", err)
		return true, 1
	}
	return false, 0
}

// pending reports state with growing checkpoints while fn runs and answers
// interrogations; it returns the error of fn.
func pending(state svc.State, r <-chan svc.ChangeRequest, s chan<- svc.Status, fn func() error) error {
	checkpoint := uint32(1)
	s <- svc.Status{State: state, CheckPoint: checkpoint, WaitHint: uint32(waitHint / time.Millisecond)}
	done := make(chan error, 1)
	go func() { done <- fn() }()
	tick := time.NewTicker(checkpointGap)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			return err
		case c := <-r:
			if c.Cmd == svc.Interrogate {
				s <- c.CurrentStatus
			}
		case <-tick.C:
			checkpoint++
			s <- svc.Status{State: state, CheckPoint: checkpoint, WaitHint: uint32(waitHint / time.Millisecond)}
		}
	}
}
