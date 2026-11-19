// Package bridge holds the services the tray program binds to its window:
// management calls, settings, server types, cloud status and control, and
// the log stream. It does not import Wails; events leave through callbacks.
package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/cloudstatus"
	"github.com/webcodingcat/hlab/internal/mgmtclient"
	"github.com/webcodingcat/hlab/internal/sysint"
)

// discardParam is the service start parameter that asks hlabd to discard
// an incompatible state.
const discardParam = "discard-state"

// lostAfter is the number of failed /status polls in a row, while the
// service manager reports running, after which the connection counts as
// lost.
const lostAfter = 3

// statusTimeout bounds one /status request.
const statusTimeout = 5 * time.Second

// State is the state of the cloud as the tray shows it.
type State struct {
	State          string                `json:"state"` // stopped|starting|running|stopping|refused
	Problems       []cloudstatus.Problem `json:"problems"`
	Refused        *cloudstatus.Problem  `json:"refused,omitempty"`
	Error          string                `json:"error,omitempty"` // failed start
	ConnectionLost bool                  `json:"connection_lost"`
	Version        string                `json:"version,omitempty"`
}

// CloudDeps are the dependencies of a Cloud.
type CloudDeps struct {
	Service    sysint.ServiceControl
	Client     *mgmtclient.Client
	StatusFile string
	Interval   time.Duration // 2 s; tests shorten it
	OnChange   func(State)   // tray icon/menu and the "cloud" event
}

// Cloud polls the state of the cloud and starts and stops it.
type Cloud struct {
	d CloudDeps

	mu    sync.Mutex
	state State
	fails int // failed /status polls in a row while running
}

// NewCloud returns a Cloud; Run starts its poller.
func NewCloud(d CloudDeps) *Cloud {
	return &Cloud{d: d}
}

// Run polls the state every interval until ctx ends: the service
// manager's state, /status while running and the status file while
// stopped. OnChange is called on every change.
func (c *Cloud) Run(ctx context.Context) {
	t := time.NewTicker(c.d.Interval)
	defer t.Stop()
	for {
		c.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Status is the last polled state.
func (c *Cloud) Status() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Start asks the service manager to start the cloud; discard asks hlabd to
// discard an incompatible state.
func (c *Cloud) Start(discard bool) error {
	var params []string
	if discard {
		params = append(params, discardParam)
	}
	return c.d.Service.Start(context.Background(), params...)
}

// Stop asks the service manager to stop the cloud.
func (c *Cloud) Stop() error {
	return c.d.Service.Stop(context.Background())
}

// poll reads the state once and records it.
func (c *Cloud) poll(ctx context.Context) {
	scm, err := c.d.Service.State(ctx)
	if err != nil {
		return
	}
	next := State{State: scm, Problems: []cloudstatus.Problem{}}
	fails := 0
	switch scm {
	case sysint.StateRunning:
		c.mu.Lock()
		prev, prevFails := c.state, c.fails
		c.mu.Unlock()
		if st, ok := c.status(ctx); ok {
			next.Problems, next.Version = st.Problems, st.Version
		} else {
			if ctx.Err() != nil {
				return
			}
			fails = prevFails + 1
			if prev.State == sysint.StateRunning {
				next.Problems, next.Version = prev.Problems, prev.Version
			}
			next.ConnectionLost = fails >= lostAfter
		}
	case sysint.StateStopped:
		if f, ok := cloudstatus.Read(c.d.StatusFile); ok {
			switch {
			case f.State == "refused" && f.Problem != nil:
				next.State, next.Refused = "refused", f.Problem
			case f.State == sysint.StateStopped && f.Error != "":
				next.Error = f.Error
			}
		}
	}
	c.mu.Lock()
	c.fails = fails
	changed := !reflect.DeepEqual(c.state, next)
	c.state = next
	c.mu.Unlock()
	if changed && c.d.OnChange != nil {
		c.d.OnChange(next)
	}
}

// statusAnswer is the part of GET /status the tray shows.
type statusAnswer struct {
	Version  string                `json:"version"`
	Problems []cloudstatus.Problem `json:"problems"`
}

// status asks the management API for its status; false when it does not
// answer.
func (c *Cloud) status(ctx context.Context) (statusAnswer, bool) {
	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	code, body, err := c.d.Client.Do(ctx, http.MethodGet, "/status", nil)
	var st statusAnswer
	if err != nil || code != http.StatusOK || json.Unmarshal(body, &st) != nil {
		return statusAnswer{}, false
	}
	if st.Problems == nil {
		st.Problems = []cloudstatus.Problem{}
	}
	return st, true
}

// TrayModel is what the tray icon and its menu show.
type TrayModel struct {
	Icon     string `json:"icon"` // running|stopped|busy|error
	Tooltip  string `json:"tooltip"`
	Status   string `json:"status"` // menu status line
	CanStart bool   `json:"can_start"`
	CanStop  bool   `json:"can_stop"`
}

// Tray maps a state to icon, tooltip, menu status line and the enabled
// Start and Stop items.
func Tray(s State) TrayModel {
	m := func(icon, status string, canStart, canStop bool) TrayModel {
		return TrayModel{Icon: icon, Tooltip: "hlab: " + status, Status: status, CanStart: canStart, CanStop: canStop}
	}
	switch {
	case s.State == sysint.StateRunning && s.ConnectionLost:
		return m("error", "Connection to the cloud service lost", false, true)
	case s.State == sysint.StateRunning:
		return m("running", "Cloud running", false, true)
	case s.State == sysint.StateStarting:
		return m("busy", "Cloud starting", false, false)
	case s.State == sysint.StateStopping:
		return m("busy", "Cloud stopping", false, false)
	case s.State == "refused":
		return m("error", "Cloud start refused", true, false)
	case s.State == sysint.StateStopped && s.Error != "":
		return m("error", "Cloud stopped: start failed", true, false)
	case s.State == sysint.StateStopped:
		return m("stopped", "Cloud stopped", true, false)
	default:
		return m("busy", "Checking the cloud state", false, false)
	}
}
