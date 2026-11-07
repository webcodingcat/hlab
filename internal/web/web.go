// Package web holds what Hetzner's APIs share over HTTP: routes, the token
// check, the rate limit, injected faults, the error format, pagination,
// sorting and label selectors. The api packages register their handlers on
// an API and use the helpers; web knows no resource.
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/state"
)

// Prefix is where both of Hetzner's APIs answer, each on its own endpoint.
const Prefix = "/v1"

// HetznerRateLimit is Hetzner's rate limit: requests per hour and project.
const HetznerRateLimit = 3600

// Backend is what the API needs of the lab on every request.
type Backend interface {
	// TokenByHash returns the token with that hash (state.HashToken).
	TokenByHash(hash string) (state.Token, bool)
	// TakeAPIFault counts a call of the project against its injected fault
	// and returns the fault if this call is to fail.
	TakeAPIFault(projectID int64) (state.APIFault, bool)
}

// Options are what an API needs.
type Options struct {
	Backend Backend
	// RateLimit is the number of requests per hour and project
	// (HetznerRateLimit); 0 turns the limit off.
	RateLimit int
	// Logger gets the failures of handlers; nil is slog.Default().
	Logger *slog.Logger
	// Now tells the time for the rate limit; nil is time.Now.
	Now func() time.Time
}

// Caller is who sends a request: the project and the permission of its token.
type Caller struct {
	ProjectID  int64
	Permission state.Permission
}

// HandlerFunc answers a request of a caller whose token is checked. It
// returns the status and the body, which the API writes as JSON (none for a
// nil body), or an error: an *Error is answered as it is, any other error as
// a server_error.
type HandlerFunc func(r *http.Request, caller Caller) (status int, body any, err error)

// API is one of Hetzner's APIs: its routes under Prefix and the checks every
// request passes first.
type API struct {
	mux     *http.ServeMux
	backend Backend
	limiter *rateLimiter
	logger  *slog.Logger
}

// NewAPI returns an API without routes.
func NewAPI(opts Options) *API {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &API{
		mux:     http.NewServeMux(),
		backend: opts.Backend,
		limiter: newRateLimiter(opts.RateLimit, opts.Now),
		logger:  opts.Logger,
	}
}

// Handle registers a handler for a pattern of net/http below Prefix, such as
// "GET /actions/{id}".
func (api *API) Handle(pattern string, handler HandlerFunc) {
	method, path, found := strings.Cut(pattern, " ")
	if !found {
		panic("web: the pattern " + pattern + " names no method")
	}
	api.mux.HandleFunc(method+" "+Prefix+path, func(w http.ResponseWriter, r *http.Request) {
		status, body, err := handler(r, r.Context().Value(callerKey{}).(Caller))
		if err != nil {
			api.writeError(w, err)
			return
		}
		writeJSON(w, status, body)
	})
}

// callerKey is the context key of the request's Caller.
type callerKey struct{}

// ServeHTTP answers a request in the order Hetzner's API checks it: the
// token, the rate limit, the token's permission, then the route.
func (api *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// every answer has one, errors too: hcloud-go adds it to its error texts
	w.Header().Set("X-Correlation-Id", correlationID())

	token, found := api.authenticate(r)
	if !found {
		api.writeError(w, Unauthorized())
		return
	}
	if !api.limiter.take(w.Header(), token.ProjectID) {
		api.writeError(w, RateLimitExceeded())
		return
	}
	if token.Permission != state.PermissionReadWrite && r.Method != http.MethodGet && r.Method != http.MethodHead {
		api.writeError(w, Forbidden())
		return
	}
	if err := api.injectFault(r.Context(), token.ProjectID); err != nil {
		api.writeError(w, err)
		return
	}

	handler, pattern := api.mux.Handler(r)
	if pattern == "" {
		api.writeError(w, routeError(handler, r))
		return
	}
	caller := Caller{ProjectID: token.ProjectID, Permission: token.Permission}
	// through the mux, not the handler it returned: only the mux sets the
	// path values ({id})
	api.mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, caller)))
}

// authenticate returns the token of the request's bearer header.
func (api *API) authenticate(r *http.Request) (state.Token, bool) {
	secret, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !found || secret == "" {
		return state.Token{}, false
	}
	return api.backend.TokenByHash(state.HashToken(secret))
}

// injectFault waits and fails as the project's injected API fault says, if
// this call is one of those to fail.
func (api *API) injectFault(ctx context.Context, projectID int64) error {
	fault, found := api.backend.TakeAPIFault(projectID)
	if !found {
		return nil
	}
	select {
	case <-time.After(time.Duration(fault.DelayMS) * time.Millisecond):
	case <-ctx.Done():
	}
	return faultError(fault.Status)
}

// routeError returns the error for a request that no route takes: 405 if a
// route takes its path with another method, else 404. The mux's own handler
// tells the two apart; it writes plain text, so it writes to a recorder.
func routeError(muxHandler http.Handler, r *http.Request) *Error {
	recorder := &statusRecorder{header: http.Header{}}
	muxHandler.ServeHTTP(recorder, r)
	if recorder.status == http.StatusMethodNotAllowed {
		return MethodNotAllowed()
	}
	return NotFound("resource")
}

// statusRecorder keeps the status of an answer and drops its body.
type statusRecorder struct {
	header http.Header
	status int
}

func (s *statusRecorder) Header() http.Header            { return s.header }
func (s *statusRecorder) Write(data []byte) (int, error) { return len(data), nil }
func (s *statusRecorder) WriteHeader(status int)         { s.status = status }

// correlationID returns a new random ID for an answer.
func correlationID() string {
	random := make([]byte, 8)
	_, _ = rand.Read(random) // never fails, as its documentation says
	return hex.EncodeToString(random)
}

// writeError writes an error as Hetzner does. An error that is not an *Error
// is a failure of the lab: it is logged and answered as a server_error.
func (api *API) writeError(w http.ResponseWriter, err error) {
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		api.logger.Error("a request failed", "error", err)
		apiErr = ServerError("the lab failed: " + err.Error())
	}
	writeJSON(w, apiErr.Status, apiErr.body())
}

// writeJSON writes a status and a body as JSON; a nil body writes none.
func writeJSON(w http.ResponseWriter, status int, body any) {
	if body == nil {
		w.WriteHeader(status)
		return
	}
	data, err := json.Marshal(body)
	if err != nil {
		// the API's own types always encode: this is a bug of the lab
		status = http.StatusInternalServerError
		data, _ = json.Marshal(ServerError("encoding the answer failed: " + err.Error()).body())
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)+1))
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}
