// Package api is the HTTP layer of the Hetzner-compatible API shared by all
// resource modules: routing under /v1, request log, token authentication,
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
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/store"
)

// Prefix is the path prefix of the API.
const Prefix = "/v1"

// Deps are the parts of hlabd the handlers reach through the request.
type Deps struct {
	Store   *store.Store
	Actions *actions.Manager
}

// Router serves the API of all mounted modules under /v1.
type Router struct {
	deps    Deps
	mux     *http.ServeMux
	handler http.Handler
}

// New returns a router without modules.
func New(deps Deps) *Router {
	r := &Router{deps: deps, mux: http.NewServeMux()}
	r.mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		NotFound(w)
	}))
	inner := r.authenticate(checkJSON(guardReadOnly(r.mux)))
	r.handler = logRequests(r.underPrefix(http.StripPrefix(Prefix, inner)))
	return r
}

// Mount registers the endpoints of m and hands the action manager to a
// resource.ActionUser.
func (r *Router) Mount(m resource.Module) {
	if u, ok := m.(resource.ActionUser); ok {
		u.UseActions(r.deps.Actions)
	}
	m.Routes(registrar{r.mux})
}

// ServeHTTP serves one API request.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.handler.ServeHTTP(w, req)
}

type registrar struct{ mux *http.ServeMux }

func (g registrar) Handle(pattern string, h http.Handler) { g.mux.Handle(pattern, h) }

// underPrefix answers not_found outside /v1 and puts the dependencies into
// the request context.
func (r *Router) underPrefix(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != Prefix && !strings.HasPrefix(req.URL.Path, Prefix+"/") {
			NotFound(w)
			return
		}
		next.ServeHTTP(w, req.WithContext(withDeps(req.Context(), r.deps)))
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
		next.ServeHTTP(w, req.WithContext(withAuth(req.Context(), project, tok.ReadOnly)))
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
