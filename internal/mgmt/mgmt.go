// Package mgmt is the management API of hlabd, served over the management
// pipe under /mgmt/v1: status, projects, tokens, management actions and the
// project proxy to the Hetzner API handlers.
package mgmt

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math/big"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/logfile"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/settings"
	"github.com/webcodingcat/hlab/internal/store"
)

// Prefix is the path prefix of the management API.
const Prefix = "/mgmt/v1"

// Status is the answer of GET /status.
type Status struct {
	State    string    `json:"state"`
	Version  string    `json:"version"`
	Problems []Problem `json:"problems"`
	// ShadowingZones are the primary zones that shadow a public domain.
	ShadowingZones []ShadowingZone `json:"shadowing_zones"`
}

// ShadowingZone is a zone whose name also resolves publicly, with the name
// of its project.
type ShadowingZone struct {
	Zone    string `json:"zone"`
	Project string `json:"project"`
}

// Problem is a host problem reported in Status.
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Deps are what the management API serves from.
type Deps struct {
	Store *store.Store
	// API serves the Hetzner API under /v1; the project proxy forwards to it.
	API    http.Handler
	Status func() Status
	// Actions runs project deletions and serves GET /actions/{id}.
	Actions *actions.Manager
	// Deleters delete the resources of a deleted project, by store kind.
	Deleters map[string]resource.Deleter
	// PushAgent pushes the agent document after a project deletion; nil
	// pushes nothing.
	PushAgent func(ctx context.Context) error
	// UpdateCatalog updates the server type catalog from the real Hetzner
	// API with hetznerToken, which it must neither store nor log; nil
	// leaves POST /catalog/update out.
	UpdateCatalog func(ctx context.Context, hetznerToken string) error
	// ServerTypes counts the served server types for the answer of
	// POST /catalog/update; nil answers 0.
	ServerTypes func() int
	// CACert returns the hlab CA certificate as PEM; nil leaves
	// GET /ca.pem out.
	CACert func() []byte
	// Trust adds the hlab CA to the host's trust stores (on) or removes it;
	// nil leaves PUT /trust out.
	Trust func(ctx context.Context, on bool) error
	// SettingsPath is settings.toml, where PUT /trust stores trust_ca.
	SettingsPath string
	// LogDir is the folder of hlabd.log, served by GET /logs; "" leaves the
	// route out.
	LogDir string
	// Reload re-reads the settings and the own server types; nil leaves
	// POST /settings/reload out.
	Reload func(ctx context.Context) error
	// Done is closed when the cloud stops; it ends the followers of
	// GET /logs, so they do not hold up the shutdown. nil never ends them.
	Done <-chan struct{}
}

// Project is a project as the management API returns it.
type Project struct {
	Name      string    `json:"name"`
	Created   time.Time `json:"created"`
	Resources int       `json:"resources"`
}

// Token is a token as the management API returns it. Value is only set in
// the response that creates the token.
type Token struct {
	Name     string    `json:"name"`
	ReadOnly bool      `json:"read_only"`
	Created  time.Time `json:"created"`
	Value    string    `json:"token,omitempty"`
}

type server struct{ d Deps }

// New returns the handler of the management API.
func New(d Deps) http.Handler {
	s := server{d}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+Prefix+"/status", s.status)
	mux.HandleFunc("GET "+Prefix+"/projects", s.listProjects)
	mux.HandleFunc("POST "+Prefix+"/projects", s.createProject)
	mux.HandleFunc("DELETE "+Prefix+"/projects/{name}", s.deleteProject)
	mux.HandleFunc("GET "+Prefix+"/actions/{id}", s.getAction)
	mux.HandleFunc("GET "+Prefix+"/projects/{name}/tokens", s.listTokens)
	mux.HandleFunc("POST "+Prefix+"/projects/{name}/tokens", s.createToken)
	mux.HandleFunc("DELETE "+Prefix+"/projects/{name}/tokens/{token}", s.deleteToken)
	mux.HandleFunc(Prefix+"/projects/{name}/api/v1/{rest...}", s.proxy)
	if d.UpdateCatalog != nil {
		mux.HandleFunc("POST "+Prefix+"/catalog/update", s.updateCatalog)
	}
	if d.CACert != nil {
		mux.HandleFunc("GET "+Prefix+"/ca.pem", s.caCert)
	}
	if d.Trust != nil {
		mux.HandleFunc("PUT "+Prefix+"/trust", s.trust)
	}
	if d.LogDir != "" {
		mux.HandleFunc("GET "+Prefix+"/logs", s.logs)
	}
	if d.Reload != nil {
		mux.HandleFunc("POST "+Prefix+"/settings/reload", s.reload)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "unknown path")
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write management response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// fail answers a store error: not found, conflict or a server error.
func fail(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", what+" not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", what+" already exists")
	default:
		slog.Error("management request", "err", err)
		writeError(w, http.StatusInternalServerError, "server_error", "internal error")
	}
}

func decode(w http.ResponseWriter, req *http.Request, v any) bool {
	if err := json.NewDecoder(req.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func (s server) status(w http.ResponseWriter, _ *http.Request) {
	st := s.d.Status()
	if st.Problems == nil {
		st.Problems = []Problem{}
	}
	if st.ShadowingZones == nil {
		st.ShadowingZones = []ShadowingZone{}
	}
	writeJSON(w, http.StatusOK, st)
}

var projectName = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)

func (s server) listProjects(w http.ResponseWriter, _ *http.Request) {
	out := []Project{}
	err := s.d.Store.Tx(func(tx store.Tx) error {
		ps, err := store.ListProjects(tx)
		if err != nil {
			return err
		}
		for _, p := range ps {
			n, err := store.CountResources(tx, p.ID)
			if err != nil {
				return err
			}
			out = append(out, Project{Name: p.Name, Created: p.Created, Resources: n})
		}
		return nil
	})
	if err != nil {
		fail(w, err, "project")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": out})
}

func (s server) createProject(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, req, &body) {
		return
	}
	if !projectName.MatchString(body.Name) {
		writeError(w, http.StatusBadRequest, "invalid_input", "the project name must have 1 to 63 characters a-z, 0-9 and -")
		return
	}
	var p store.Project
	err := s.d.Store.Tx(func(tx store.Tx) error {
		var err error
		p, err = store.CreateProject(tx, body.Name)
		return err
	})
	if err != nil {
		fail(w, err, "project "+body.Name)
		return
	}
	writeJSON(w, http.StatusCreated, Project{Name: p.Name, Created: p.Created})
}

// projectRef is the resource type of a project in the actions of project
// deletion; a running deletion locks its project.
const projectRef = "project"

// deleteProject revokes the project's tokens at once and starts an action
// without a project that deletes the project with all its resources.
func (s server) deleteProject(w http.ResponseWriter, req *http.Request) {
	name := req.PathValue("name")
	var p store.Project
	var action *actions.Action
	err := s.d.Store.Tx(func(tx store.Tx) error {
		var err error
		p, err = store.ProjectByName(tx, name)
		if err != nil {
			return err
		}
		action, err = s.d.Actions.Start(tx, 0, "delete_project", []actions.Ref{{ID: p.ID, Type: projectRef}})
		if err != nil {
			return err
		}
		tokens, err := store.ListTokens(tx, p.ID)
		if err != nil {
			return err
		}
		for _, t := range tokens {
			if err := store.DeleteToken(tx, p.ID, t.Name); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, actions.ErrLocked) {
		writeError(w, http.StatusConflict, "conflict", "a deletion of project "+name+" is running")
		return
	}
	if err != nil {
		fail(w, err, "project "+name)
		return
	}
	s.d.Actions.Run(action.ID, func(ctx context.Context, progress func(int)) error {
		return s.purge(ctx, p, progress)
	})
	writeJSON(w, http.StatusAccepted, map[string]int64{"action_id": action.ID})
}

// idleTimeout bounds how long a project deletion waits for the project's
// running actions, a server creation for example, before it deletes.
const idleTimeout = 20 * time.Minute

// purge waits until no action of p runs, so no VM is created after its
// server was deleted, then deletes every resource of p through its
// module's Deleter, then the project with the remaining rows, and pushes
// the agent document. It stops at the first failing Delete or a wait that
// times out and keeps the project.
//
// The kinds go in sorted order, except networks, which go last: deleting a
// network releases its VLAN, and a released VLAN can go to another
// project's network at once, whose members would then share layer 2 with
// this project's VMs until those are deleted.
func (s server) purge(ctx context.Context, p store.Project, progress func(int)) error {
	type item struct {
		kind string
		id   int64
	}
	wait, cancel := context.WithTimeout(ctx, idleTimeout)
	err := s.d.Actions.WaitIdle(wait, p.ID)
	cancel()
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("actions of project %s still running after %s", p.Name, idleTimeout)
	}
	if err != nil {
		return err
	}
	var items []item
	err = s.d.Store.Tx(func(tx store.Tx) error {
		for _, kind := range deletionOrder(s.d.Deleters) {
			ids, err := store.IDs(tx, kind, p.ID)
			if err != nil {
				return err
			}
			for _, id := range ids {
				items = append(items, item{kind, id})
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i, it := range items {
		if err := s.d.Deleters[it.kind].Delete(ctx, it.id); err != nil {
			return fmt.Errorf("delete %s %d of project %s: %w", it.kind, it.id, p.Name, err)
		}
		progress((i + 1) * 90 / len(items))
	}
	if err := s.d.Store.Tx(func(tx store.Tx) error { return store.DeleteProject(tx, p.ID) }); err != nil {
		return err
	}
	if s.d.PushAgent != nil {
		if err := s.d.PushAgent(ctx); err != nil {
			slog.Warn("push the agent document after deleting a project", "project", p.Name, "err", err)
		}
	}
	slog.Info("project deleted", "project", p.Name, "resources", len(items))
	return nil
}

// networksKind is the kind of the networks module; mgmt does not import it.
const networksKind = "networks"

// deletionOrder returns the kinds of deleters sorted, with networksKind
// moved to the end.
func deletionOrder(deleters map[string]resource.Deleter) []string {
	kinds := slices.Sorted(maps.Keys(deleters))
	if i := slices.Index(kinds, networksKind); i >= 0 {
		kinds = append(slices.Delete(kinds, i, i+1), networksKind)
	}
	return kinds
}

// getAction answers a management action, that is one without a project.
func (s server) getAction(w http.ResponseWriter, req *http.Request) {
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusNotFound, "not_found", "action not found")
		return
	}
	a, err := s.d.Actions.Get(0, id)
	if err != nil {
		fail(w, err, "action")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"action": a})
}

// project looks up the project of the request path and answers not_found
// when it does not exist.
func (s server) project(w http.ResponseWriter, req *http.Request) (store.Project, bool) {
	name := req.PathValue("name")
	var p store.Project
	err := s.d.Store.Tx(func(tx store.Tx) error {
		var err error
		p, err = store.ProjectByName(tx, name)
		return err
	})
	if err != nil {
		fail(w, err, "project "+name)
		return store.Project{}, false
	}
	return p, true
}

func (s server) listTokens(w http.ResponseWriter, req *http.Request) {
	p, ok := s.project(w, req)
	if !ok {
		return
	}
	out := []Token{}
	err := s.d.Store.Tx(func(tx store.Tx) error {
		ts, err := store.ListTokens(tx, p.ID)
		for _, t := range ts {
			out = append(out, Token{Name: t.Name, ReadOnly: t.ReadOnly, Created: t.Created})
		}
		return err
	})
	if err != nil {
		fail(w, err, "token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

func (s server) createToken(w http.ResponseWriter, req *http.Request) {
	p, ok := s.project(w, req)
	if !ok {
		return
	}
	var body struct {
		Name     string `json:"name"`
		ReadOnly bool   `json:"read_only"`
	}
	if !decode(w, req, &body) {
		return
	}
	if body.Name == "" || len(body.Name) > 63 {
		writeError(w, http.StatusBadRequest, "invalid_input", "the token name must have 1 to 63 characters")
		return
	}
	value, err := newTokenValue()
	if err != nil {
		fail(w, err, "token")
		return
	}
	sum := sha256.Sum256([]byte(value))
	err = s.d.Store.Tx(func(tx store.Tx) error {
		return store.CreateToken(tx, p.ID, body.Name, sum[:], body.ReadOnly)
	})
	if err != nil {
		fail(w, err, "token "+body.Name)
		return
	}
	writeJSON(w, http.StatusCreated, Token{Name: body.Name, ReadOnly: body.ReadOnly, Created: time.Now().UTC(), Value: value})
}

func (s server) deleteToken(w http.ResponseWriter, req *http.Request) {
	p, ok := s.project(w, req)
	if !ok {
		return
	}
	name := req.PathValue("token")
	err := s.d.Store.Tx(func(tx store.Tx) error { return store.DeleteToken(tx, p.ID, name) })
	if err != nil {
		fail(w, err, "token "+name)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// updateCatalog hands the Hetzner token of the body to UpdateCatalog once.
// The body and the token are never logged.
func (s server) updateCatalog(w http.ResponseWriter, req *http.Request) {
	var body struct {
		HetznerToken string `json:"hetzner_token"`
	}
	if !decode(w, req, &body) {
		return
	}
	if body.HetznerToken == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "hetzner_token is required")
		return
	}
	if err := s.d.UpdateCatalog(req.Context(), body.HetznerToken); err != nil {
		slog.Warn("catalog update failed", "err", err)
		writeError(w, http.StatusBadGateway, "server_error", err.Error())
		return
	}
	n := 0
	if s.d.ServerTypes != nil {
		n = s.d.ServerTypes()
	}
	writeJSON(w, http.StatusOK, map[string]int{"server_types": n})
}

// caCert answers the hlab CA certificate as PEM.
func (s server) caCert(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(s.d.CACert()); err != nil {
		slog.Error("write management response", "err", err)
	}
}

// LogLines is the number of lines GET /logs answers before following.
const LogLines = 1000

// logs answers the last LogLines lines of hlabd.log as NDJSON, one flush
// per line; with follow=1 it goes on with new lines until the client goes
// or Done closes.
func (s server) logs(w http.ResponseWriter, req *http.Request) {
	follow := req.URL.Query().Get("follow") == "1"
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	if s.d.Done != nil {
		go func() {
			select {
			case <-s.d.Done:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	_ = rc.Flush()
	err := logfile.Tail(ctx, s.d.LogDir, "hlabd.log", LogLines, follow, func(line []byte) error {
		if _, err := w.Write(line); err != nil {
			return err
		}
		return rc.Flush()
	})
	if err != nil && ctx.Err() == nil {
		slog.Error("serve the log", "err", err)
	}
}

// reload applies a changed settings file and own server types file.
func (s server) reload(w http.ResponseWriter, req *http.Request) {
	if err := s.d.Reload(req.Context()); err != nil {
		slog.Warn("settings reload failed", "err", err)
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

// trust switches the hlab CA in the host's trust stores and, once that
// worked, stores the switch as trust_ca in the settings file.
func (s server) trust(w http.ResponseWriter, req *http.Request) {
	var body struct {
		On *bool `json:"on"`
	}
	if !decode(w, req, &body) {
		return
	}
	if body.On == nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "on is required")
		return
	}
	set, err := settings.Load(s.d.SettingsPath)
	if err != nil {
		slog.Error("load the settings for the trust switch", "err", err)
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	if err := s.d.Trust(req.Context(), *body.On); err != nil {
		slog.Warn("trust switch failed", "on", *body.On, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	set.Certificates.TrustCA = *body.On
	if err := settings.Save(s.d.SettingsPath, set); err != nil {
		slog.Error("save the trust switch", "err", err)
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"on": *body.On})
}

// proxy forwards a request to the Hetzner API handlers as project name
// with read/write rights.
func (s server) proxy(w http.ResponseWriter, req *http.Request) {
	p, ok := s.project(w, req)
	if !ok {
		return
	}
	r := req.Clone(api.WithProject(req.Context(), p))
	r.URL.Path = api.Prefix + "/" + req.PathValue("rest")
	r.URL.RawPath = ""
	r.RequestURI = ""
	s.d.API.ServeHTTP(w, r)
}

const tokenAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// newTokenValue returns 64 random characters [A-Za-z0-9].
func newTokenValue() (string, error) {
	b := make([]byte, 64)
	n := big.NewInt(int64(len(tokenAlphabet)))
	for i := range b {
		k, err := rand.Int(rand.Reader, n)
		if err != nil {
			return "", err
		}
		b[i] = tokenAlphabet[k.Int64()]
	}
	return string(b), nil
}
