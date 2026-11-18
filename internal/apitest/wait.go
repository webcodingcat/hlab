package apitest

import (
	"fmt"
	"net/http"
	"time"
)

// ActionTimeout is how long WaitAction waits for an action to finish.
const ActionTimeout = 10 * time.Second

// Action is the part of a Hetzner action object tests check.
type Action struct {
	ID       int64        `json:"id"`
	Command  string       `json:"command"`
	Status   string       `json:"status"`
	Progress int          `json:"progress"`
	Finished *string      `json:"finished"`
	Error    *ActionError `json:"error"`
}

// ActionError is the error object of a failed action.
type ActionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// GetAction reads GET /actions/{id} with the token of project "p".
func (e *Env) GetAction(id int64) Action {
	e.t.Helper()
	return e.GetActionAuth("Bearer "+e.Token, id)
}

// GetActionAuth is GetAction with the given Authorization header value.
func (e *Env) GetActionAuth(authorization string, id int64) Action {
	e.t.Helper()
	resp := e.DoAuth(authorization, http.MethodGet, fmt.Sprintf("/actions/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("GET /actions/%d: status %d, want 200", id, resp.StatusCode)
	}
	var body struct {
		Action Action `json:"action"`
	}
	Decode(e.t, resp, &body)
	return body.Action
}

// WaitAction polls GET /actions/{id} with the token of project "p" until
// the action is no longer running and returns it; it fails the test after
// ActionTimeout.
func (e *Env) WaitAction(id int64) Action {
	e.t.Helper()
	return e.WaitActionAuth("Bearer "+e.Token, id)
}

// WaitActionAuth is WaitAction with the given Authorization header value.
func (e *Env) WaitActionAuth(authorization string, id int64) Action {
	e.t.Helper()
	deadline := time.Now().Add(ActionTimeout)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		a := e.GetActionAuth(authorization, id)
		if a.Status != "running" {
			return a
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("action %d still running after %s", id, ActionTimeout)
		}
		<-tick.C
	}
}
