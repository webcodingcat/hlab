// Package control is the lab control under /_lab: the lab's status, stopping
// it, projects and tokens, and the Cloud API on behalf of a project for the
// web UI. The CLI (package client) and the web UI call it; the requests and
// answers are those of package labapi.
//
// A request is admitted with the control token (Authorization: Bearer) or,
// from the web UI, when Options.Admit vouches for it.
package control

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/labapi"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// Options are what the lab control needs.
type Options struct {
	Lab *lab.Lab
	// Cloud is the Cloud API, which the web UI calls through
	// /_lab/projects/{name}/cloud/v1/...
	Cloud *web.API
	// Token is the control token, which the CLI sends.
	Token string
	// Admit vouches for a request without the control token (the web UI's
	// requests); nil admits none.
	Admit func(r *http.Request) bool
	// Status returns the lab's status; the control adds the number of
	// projects.
	Status func() labapi.Status
	// Shutdown asks the service to stop; the control answers before it does.
	Shutdown func()
	// Logger gets the failures of handlers; nil is slog.Default().
	Logger *slog.Logger
}

// Control is the lab control's HTTP handler.
type Control struct {
	opts Options
	mux  *http.ServeMux
}

// New returns the lab control with its routes.
func New(opts Options) *Control {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	c := &Control{opts: opts, mux: http.NewServeMux()}
	c.handle("GET /status", c.status)
	c.handle("POST /shutdown", c.shutdown)
	c.handle("GET /projects", c.listProjects)
	c.handle("POST /projects", c.createProject)
	c.handle("DELETE /projects/{project}", c.deleteProject)
	c.handle("GET /projects/{project}/tokens", c.listTokens)
	c.handle("POST /projects/{project}/tokens", c.createToken)
	c.handle("DELETE /projects/{project}/tokens/{token}", c.deleteToken)
	c.handle("POST /projects/{project}/env-token", c.envToken)
	c.mux.HandleFunc(labapi.Prefix+"/projects/{project}/cloud/", c.cloud)
	return c
}

// handlerFunc answers a request with a status and a body, which the control
// writes as JSON, or an error.
type handlerFunc func(r *http.Request) (status int, body any, err error)

// handle registers a handler for a pattern below labapi.Prefix.
func (c *Control) handle(pattern string, handler handlerFunc) {
	method, path, _ := strings.Cut(pattern, " ")
	c.mux.HandleFunc(method+" "+labapi.Prefix+path, func(w http.ResponseWriter, r *http.Request) {
		status, body, err := handler(r)
		if err != nil {
			web.WriteError(w, c.opts.Logger, answer(err))
			return
		}
		web.WriteJSON(w, status, body)
	})
}

// ServeHTTP admits a request and passes it to its route.
func (c *Control) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !c.admitted(r) {
		web.WriteError(w, c.opts.Logger, &web.Error{
			Status:  http.StatusUnauthorized,
			Code:    "unauthorized",
			Message: "the control token is missing or wrong (" + labapi.TokenFile + " in the data folder)",
		})
		return
	}
	if _, pattern := c.mux.Handler(r); pattern == "" {
		web.WriteError(w, c.opts.Logger, &web.Error{Status: http.StatusNotFound, Code: "not_found", Message: "no such route of the lab control"})
		return
	}
	c.mux.ServeHTTP(w, r)
}

// admitted tells whether a request carries the control token or comes from
// the web UI.
func (c *Control) admitted(r *http.Request) bool {
	secret, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if found {
		return subtle.ConstantTimeCompare([]byte(secret), []byte(c.opts.Token)) == 1
	}
	return c.opts.Admit != nil && c.opts.Admit(r)
}

// answer turns the lab's errors into the control's error answers; other
// errors are failures of the lab.
func answer(err error) error {
	switch {
	case errors.Is(err, state.ErrNotFound):
		return &web.Error{Status: http.StatusNotFound, Code: "not_found", Message: err.Error()}
	case errors.Is(err, lab.ErrInvalid):
		return &web.Error{Status: http.StatusUnprocessableEntity, Code: "invalid_input", Message: err.Error()}
	case errors.Is(err, lab.ErrExists):
		return &web.Error{Status: http.StatusConflict, Code: "uniqueness_error", Message: err.Error()}
	}
	return err
}

func (c *Control) status(*http.Request) (int, any, error) {
	status := c.opts.Status()
	status.Projects = len(c.opts.Lab.Projects())
	return http.StatusOK, status, nil
}

func (c *Control) shutdown(*http.Request) (int, any, error) {
	c.opts.Shutdown()
	return http.StatusAccepted, nil, nil
}
