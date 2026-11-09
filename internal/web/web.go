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
	"regexp"
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
	// (HetznerRateLimit); the actions endpoints have twice as many of their
	// own. 0 turns the limit off.
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

// Request is a request whose token is checked, as a handler gets it.
type Request struct {
	*http.Request
	Caller Caller
	// Header is the answer's header, for what a handler adds (the Link
	// header of Paginate).
	Header http.Header
}

// HandlerFunc answers a request. It returns the status and the body, which
// the API writes as JSON (none for a nil body), or an error: an *Error is
// answered as it is, any other error as a server_error.
type HandlerFunc func(req *Request) (status int, body any, err error)

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

// wildcard finds the wildcards of a pattern: "{id}", "{action_id}".
var wildcard = regexp.MustCompile(`\{([a-z_]+)\}`)

// Handle registers a handler for a pattern of net/http below Prefix, such as
// "GET /actions/{id}". A wildcard named id or ending in _id takes only IDs:
// for any other value, Hetzner's API knows no such route.
func (api *API) Handle(pattern string, handler HandlerFunc) {
	method, path, found := strings.Cut(pattern, " ")
	if !found {
		panic("web: the pattern " + pattern + " names no method")
	}
	var ids []string
	for _, match := range wildcard.FindAllStringSubmatch(path, -1) {
		if name := match[1]; name == "id" || strings.HasSuffix(name, "_id") {
			ids = append(ids, name)
		}
	}
	api.mux.HandleFunc(method+" "+Prefix+path, func(w http.ResponseWriter, r *http.Request) {
		for _, name := range ids {
			if id, err := strconv.ParseInt(r.PathValue(name), 10, 64); err != nil || id < 1 {
				WriteError(w, api.logger, RouteNotFound())
				return
			}
		}
		caller, admitted := api.admit(w, r)
		if !admitted {
			return
		}
		status, body, err := handler(&Request{Request: r, Caller: caller, Header: w.Header()})
		if err != nil {
			WriteError(w, api.logger, err)
			return
		}
		WriteJSON(w, status, body)
	})
}

// ServeHTTP answers a request in the order Hetzner's API checks it: an
// unknown route first, then the token, then (in admit) the rate limit, the
// permission and an injected fault; 405 comes only after the token.
func (api *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	muxHandler, pattern := api.mux.Handler(r)
	if pattern != "" {
		api.mux.ServeHTTP(w, r) // the route's handler (Handle) does the rest
		return
	}
	// The mux's own handler tells an unknown path from a known path with
	// another method; it writes plain text, so it writes to a recorder.
	recorder := &statusRecorder{header: http.Header{}}
	muxHandler.ServeHTTP(recorder, r)
	if recorder.status != http.StatusMethodNotAllowed {
		WriteError(w, api.logger, RouteNotFound())
		return
	}
	if _, admitted := api.admit(w, r); !admitted {
		return
	}
	w.Header().Set("Allow", recorder.header.Get("Allow"))
	WriteError(w, api.logger, MethodNotAllowed())
}

// admit checks a request of a known route: the token, the rate limit, the
// token's permission and an injected fault. It answers a request that fails
// and returns the caller of one that passes.
func (api *API) admit(w http.ResponseWriter, r *http.Request) (Caller, bool) {
	caller, vouched := r.Context().Value(callerKey{}).(Caller)
	if !vouched {
		secret, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !found || secret == "" {
			WriteError(w, api.logger, unauthorized("token is required"))
			return Caller{}, false
		}
		token, found := api.backend.TokenByHash(state.HashToken(secret))
		if !found {
			WriteError(w, api.logger, TokenInvalid())
			return Caller{}, false
		}
		caller = Caller{ProjectID: token.ProjectID, Permission: token.Permission}
	}
	// Hetzner's answers carry one from here on; those above, and to unknown
	// routes, do not. hcloud-go adds it to its error texts.
	w.Header().Set("X-Correlation-Id", correlationID())
	// a vouched caller (the web UI) neither counts against the project's rate
	// limit nor takes its injected faults: those are for the tools under test,
	// and a page that polls would use them up
	if !vouched && !api.limiter.take(w.Header(), caller.ProjectID, r.URL.Path) {
		WriteError(w, api.logger, RateLimitExceeded())
		return Caller{}, false
	}
	if caller.Permission != state.PermissionReadWrite && r.Method != http.MethodGet && r.Method != http.MethodHead {
		WriteError(w, api.logger, Forbidden())
		return Caller{}, false
	}
	if !vouched {
		if err := injectFault(r.Context(), api.backend, caller.ProjectID); err != nil {
			WriteError(w, api.logger, err)
			return Caller{}, false
		}
	}
	return caller, true
}

// callerKey is the context key of a caller that ServeAs vouches for.
type callerKey struct{}

// ServeAs answers requests as the given caller, without a token: the lab
// control vouches for the caller (the web UI acts for a project and keeps no
// token in the browser). The permission still applies; the rate limit and
// injected faults do not (admit).
func (api *API) ServeAs(caller Caller) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, caller)))
	})
}

// injectFault waits and fails as the project's injected API fault says, if
// this call is one of those to fail.
func injectFault(ctx context.Context, backend Backend, projectID int64) error {
	fault, found := backend.TakeAPIFault(projectID)
	if !found {
		return nil
	}
	select {
	case <-time.After(time.Duration(fault.DelayMS) * time.Millisecond):
	case <-ctx.Done():
	}
	return faultError(fault.Status)
}

// statusRecorder keeps the status and the header of an answer and drops its
// body.
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

// WriteError writes an error as Hetzner does. An error that is not an *Error
// is a failure of the lab: it is logged and answered as a server_error with
// its text, which in a test lab helps more than it reveals. The lab control
// and the web UI answer their errors in the same format.
func WriteError(w http.ResponseWriter, logger *slog.Logger, err error) {
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		logger.Error("a request failed", "error", err)
		apiErr = ServerError("the lab failed: " + err.Error())
	}
	if apiErr.bare {
		w.WriteHeader(apiErr.Status)
		return
	}
	if apiErr.plain {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(apiErr.Status)
		_, _ = w.Write([]byte(apiErr.Message + "\n"))
		return
	}
	WriteJSON(w, apiErr.Status, apiErr.body())
}

// WriteJSON writes a status and a body as JSON; a nil body writes none.
func WriteJSON(w http.ResponseWriter, status int, body any) {
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
