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
	// Faults is the number of active faults of all projects from GET
	// /status; 0 unless running, a lost connection keeps the last count.
	Faults int `json:"faults"`
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

	// notify orders recording a state and calling OnChange, so OnChange
	// sees the states in the order they were recorded.
	notify sync.Mutex

	mu    sync.Mutex
	state State
	fails int    // failed /status polls in a row while running
	gen   uint64 // counts Start and Stop; a poll begun before one is dropped
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
// discard an incompatible state. Once the call is accepted the state is
// "starting" at once.
func (c *Cloud) Start(discard bool) error {
	var params []string
	if discard {
		params = append(params, discardParam)
	}
	if err := c.d.Service.Start(context.Background(), params...); err != nil {
		return err
	}
	c.pending(sysint.StateStarting)
	return nil
}

// Stop asks the service manager to stop the cloud. Once the call is
// accepted the state is "stopping" at once.
func (c *Cloud) Stop() error {
	if err := c.d.Service.Stop(context.Background()); err != nil {
		return err
	}
	c.pending(sysint.StateStopping)
	return nil
}

// pending records state after an accepted Start or Stop and drops the
// result of every poll begun before.
func (c *Cloud) pending(state string) {
	c.record(State{State: state, Problems: []cloudstatus.Problem{}}, 0, true, 0)
}

// record sets the state and the failed poll count and calls OnChange when
// the state changed. A poll passes the generation it began with and is
// dropped when a Start or Stop came in between; bump starts a new
// generation.
func (c *Cloud) record(next State, fails int, bump bool, gen uint64) {
	c.notify.Lock()
	defer c.notify.Unlock()
	c.mu.Lock()
	if bump {
		c.gen++
	} else if gen != c.gen {
		c.mu.Unlock()
		return
	}
	c.fails = fails
	changed := !reflect.DeepEqual(c.state, next)
	c.state = next
	c.mu.Unlock()
	if changed && c.d.OnChange != nil {
		c.d.OnChange(next)
	}
}

// poll reads the state once and records it.
func (c *Cloud) poll(ctx context.Context) {
	c.mu.Lock()
	gen := c.gen
	c.mu.Unlock()
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
			next.Problems, next.Version, next.Faults = st.Problems, st.Version, st.Faults
		} else {
			if ctx.Err() != nil {
				return
			}
			fails = prevFails + 1
			if prev.State == sysint.StateRunning {
				next.Problems, next.Version, next.Faults = prev.Problems, prev.Version, prev.Faults
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
	c.record(next, fails, false, gen)
}

// statusAnswer is the part of GET /status the tray shows.
type statusAnswer struct {
	Version  string                `json:"version"`
	Problems []cloudstatus.Problem `json:"problems"`
	Faults   int                   `json:"faults"`
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

// EndAllFaults ends the active faults of all projects through the
// management API; a status other than 204 is an error carrying the
// management API's message.
func (c *Cloud) EndAllFaults() error {
	status, body, err := c.d.Client.Do(context.Background(), http.MethodDelete, "/faults", nil)
	if err != nil {
		return err
	}
	if status != http.StatusNoContent {
		return mgmtclient.AnswerError(status, body)
	}
	return nil
}

// TrayModel is what the tray icon and its menu show.
type TrayModel struct {
	Icon        string `json:"icon"`         // running|stopped|busy|error
	Tooltip     string `json:"tooltip"`      // "HLab: <status>"
	Status      string `json:"status"`       // menu status line
	CloudItem   string `json:"cloud_item"`   // label of the one cloud item
	CloudAction string `json:"cloud_action"` // "start" | "stop" | "" (item disabled)
	Faults      int    `json:"faults"`       // > 0: the menu shows "End all faults"
}

// Labels of the tray menu's cloud item.
const (
	itemStart    = "Start cloud"
	itemStop     = "Stop cloud"
	itemStarting = "Starting cloud…"
	itemStopping = "Stopping cloud…"
	itemChecking = "Checking cloud…"
)

// Actions of the tray menu's cloud item.
const (
	actionStart = "start"
	actionStop  = "stop"
)

// Tray maps a state to icon, tooltip, menu status line and the label and
// action of the cloud item.
func Tray(s State) TrayModel {
	m := func(icon, status, item, action string) TrayModel {
		return TrayModel{Icon: icon, Tooltip: "HLab: " + status, Status: status, CloudItem: item, CloudAction: action, Faults: s.Faults}
	}
	switch {
	case s.State == sysint.StateRunning && s.ConnectionLost:
		return m("error", "Connection to the cloud service lost", itemStop, actionStop)
	case s.State == sysint.StateRunning:
		return m("running", "Cloud running", itemStop, actionStop)
	case s.State == sysint.StateStarting:
		return m("busy", "Cloud starting", itemStarting, "")
	case s.State == sysint.StateStopping:
		return m("busy", "Cloud stopping", itemStopping, "")
	case s.State == "refused":
		return m("error", "Cloud start refused", itemStart, actionStart)
	case s.State == sysint.StateStopped && s.Error != "":
		return m("error", "Cloud stopped: start failed", itemStart, actionStart)
	case s.State == sysint.StateStopped:
		return m("stopped", "Cloud stopped", itemStart, actionStart)
	default:
		return m("busy", "Checking the cloud state", itemChecking, "")
	}
}
