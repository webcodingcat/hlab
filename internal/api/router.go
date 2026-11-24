// Package api is the HTTP layer of the Hetzner-compatible API shared by all
// resource modules: routing under /v1 (Cloud API) and /hetzner/v1 (Hetzner
// API), request log, token authentication,
// JSON body checks, the read-only guard and the response helpers.
package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/store"
)

// Prefix is the path prefix of the Cloud API, the default of a module.
const Prefix = "/v1"

// HetznerPrefix is the path prefix of the Hetzner API (Storage Boxes); a
// module served there implements resource.Prefixed.
const HetznerPrefix = "/hetzner/v1"

// prefixes are the API prefixes the router serves, each with its own mux.
var prefixes = []string{Prefix, HetznerPrefix}

// Deps are the parts of hlabd the handlers reach through the request.
type Deps struct {
	Store   *store.Store
	Actions *actions.Manager
	// Faults wraps the chain after token auth (faults.Manager.Middleware);
	// nil = none.
	Faults func(http.Handler) http.Handler
}

// Router serves the API of all mounted modules, each under its prefix.
type Router struct {
	deps    Deps
	muxes   map[string]*http.ServeMux // by prefix
	inner   map[string]http.Handler   // the middleware chain of each mux
	types   []string                  // Type() of the mounted modules
	handler http.Handler
}

// New returns a router without modules.
func New(deps Deps) *Router {
	r := &Router{deps: deps, muxes: map[string]*http.ServeMux{}, inner: map[string]http.Handler{}}
	for _, p := range prefixes {
		mux := http.NewServeMux()
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			NotFound(w)
		}))
		r.muxes[p] = mux
		chain := checkJSON(guardReadOnly(mux))
		if deps.Faults != nil {
			chain = deps.Faults(chain)
		}
		r.inner[p] = http.StripPrefix(p, r.authenticate(chain))
	}
	r.handler = logRequests(r.underPrefix())
	return r
}

// Mount registers the endpoints of m under its prefix (Prefix, or the one
// of a resource.Prefixed module) and hands the action manager to a
// resource.ActionUser.
func (r *Router) Mount(m resource.Module) {
	prefix := Prefix
	if p, ok := m.(resource.Prefixed); ok {
		prefix = p.Prefix()
	}
	mux, ok := r.muxes[prefix]
	if !ok {
		panic("api: module " + m.Type() + " has unknown prefix " + prefix)
	}
	if u, ok := m.(resource.ActionUser); ok {
		u.UseActions(r.deps.Actions)
	}
	m.Routes(registrar{mux})
	if !slices.Contains(r.types, m.Type()) {
		r.types = append(r.types, m.Type())
	}
}

// Resources returns the first path segments of the mounted modules
// (their Type()), sorted.
func (r *Router) Resources() []string {
	out := slices.Clone(r.types)
	slices.Sort(out)
	return out
}

// ServeHTTP serves one API request.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.handler.ServeHTTP(w, req)
}

type registrar struct{ mux *http.ServeMux }

func (g registrar) Handle(pattern string, h http.Handler) { g.mux.Handle(pattern, h) }

// underPrefix answers not_found outside the prefixes, passes a request
// under one to that prefix's chain with the prefix stripped and puts the
// dependencies into the request context. Every response under a prefix
// gets Hetzner's default RateLimit-* headers; the fault layer overwrites
// them for token requests.
func (r *Router) underPrefix() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		for _, p := range prefixes {
			if strings.HasPrefix(req.URL.Path, p+"/") {
				h := w.Header()
				h.Set("RateLimit-Limit", "3600")
				h.Set("RateLimit-Remaining", "3600")
				h.Set("RateLimit-Reset", strconv.FormatInt(time.Now().Unix(), 10))
				r.inner[p].ServeHTTP(w, req.WithContext(withDeps(req.Context(), r.deps)))
				return
			}
		}
		NotFound(w)
	})
}

// authenticate resolves the bearer token to its project. A request whose
// context already carries a project (WithProject) passes unchanged.
func (r *Router) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if _, ok := ProjectFrom(req.Context()); ok {
			next.ServeHTTP(w, req)
			return
		}
		token, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			Error(w, "unauthorized", "unable to authenticate", nil)
			return
		}
		sum := sha256.Sum256([]byte(token))
		var tok store.Token
		var project store.Project
		err := r.deps.Store.Tx(func(tx store.Tx) error {
			var err error
			tok, project, err = store.TokenByHash(tx, sum[:])
			return err
		})
		if errors.Is(err, store.ErrNotFound) {
			Error(w, "unauthorized", "unable to authenticate", nil)
			return
		}
		if err != nil {
			ServerError(w, err)
			return
		}
		next.ServeHTTP(w, req.WithContext(withToken(withAuth(req.Context(), project, tok.ReadOnly))))
	})
}

// checkJSON rejects a request body that is not valid JSON.
func checkJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Body != nil && req.Body != http.NoBody {
			body, err := io.ReadAll(req.Body)
			_ = req.Body.Close()
			if err != nil {
				Error(w, "json_error", "unable to read the request body", nil)
				return
			}
			if len(bytes.TrimSpace(body)) > 0 && !json.Valid(body) {
				Error(w, "json_error", "the request body is not valid JSON", nil)
				return
			}
			req.Body = io.NopCloser(bytes.NewReader(body))
		}
		next.ServeHTTP(w, req)
	})
}

// guardReadOnly rejects every non-GET request of a read-only token.
func guardReadOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && ReadOnlyFrom(req.Context()) {
			Error(w, "token_readonly", "the token is read-only", nil)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// Decode decodes the JSON request body into v. On failure it writes a
// json_error response and returns false.
func Decode(w http.ResponseWriter, req *http.Request, v any) bool {
	if err := json.NewDecoder(req.Body).Decode(v); err != nil {
		Error(w, "json_error", "invalid JSON: "+err.Error(), nil)
		return false
	}
	return true
}

// logRequests logs every request with its status and duration.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, req)
		slog.Info("api request", "method", req.Method, "path", req.URL.Path,
			"status", rec.status, "duration", time.Since(start))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
