// Package agent is the HTTP interface and the appliers of hlab-agent, the
// program that runs inside the hlab appliance VM. hlabd pushes the full
// desired state (agentapi.Config) with PUT /v1/config; the agent hands it
// to every Applier in order and remembers the applied generation.
package agent

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// maxConfigBytes limits the size of a pushed document.
const maxConfigBytes = 64 << 20

// maxJobBytes limits the size of a job request.
const maxJobBytes = 64 << 10

// Applier brings one part of the appliance to the state of a document. Apply
// must be idempotent: the same document may be applied again at any time.
type Applier interface {
	Name() string
	Apply(ctx context.Context, cfg agentapi.Config) error
}

// Agent serves the agent's HTTP interface.
type Agent struct {
	auth     []byte // "Bearer <secret>"
	appliers []Applier

	mu         sync.Mutex // serializes applies
	generation int
}

// New returns an Agent that accepts requests carrying secret and applies
// documents with appliers in the given order.
func New(secret []byte, appliers ...Applier) (*Agent, error) {
	if len(secret) == 0 {
		return nil, errors.New("agent: empty secret")
	}
	return &Agent{auth: append([]byte("Bearer "), secret...), appliers: appliers}, nil
}

// Handler returns the HTTP handler of the agent's interface.
func (a *Agent) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /v1/config", a.putConfig)
	mux.HandleFunc("GET /v1/status", a.getStatus)
	mux.HandleFunc("POST /v1/jobs", a.postJob)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), a.auth) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (a *Agent) putConfig(w http.ResponseWriter, r *http.Request) {
	var cfg agentapi.Config
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConfigBytes)).Decode(&cfg); err != nil {
		http.Error(w, "invalid document: "+err.Error(), http.StatusBadRequest)
		return
	}
	if cfg.Schema != agentapi.Schema {
		http.Error(w, fmt.Sprintf("unsupported schema %d, want %d", cfg.Schema, agentapi.Schema), http.StatusBadRequest)
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if cfg.Generation < a.generation {
		http.Error(w, fmt.Sprintf("generation %d is older than the applied generation %d", cfg.Generation, a.generation), http.StatusConflict)
		return
	}
	for _, ap := range a.appliers {
		if err := ap.Apply(r.Context(), cfg); err != nil {
			slog.Error("apply failed", "applier", ap.Name(), "generation", cfg.Generation, "err", err)
			http.Error(w, fmt.Sprintf("apply %s: %v", ap.Name(), err), http.StatusInternalServerError)
			return
		}
	}
	a.generation = cfg.Generation
	slog.Info("config applied", "generation", cfg.Generation)
	writeJSON(w, agentapi.Status{Generation: cfg.Generation, PasswordResetsDone: a.resetsDone()})
}

// postJob runs one job synchronously. A malformed request or an unknown
// kind answers 400; a job that fails answers 200 with OK false.
func (a *Agent) postJob(w http.ResponseWriter, r *http.Request) {
	var job agentapi.Job
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJobBytes)).Decode(&job); err != nil {
		http.Error(w, "invalid job: "+err.Error(), http.StatusBadRequest)
		return
	}
	run, err := jobFunc(job)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	slog.Info("job started", "kind", job.Kind, "lun", job.LUN, "fs", job.FS)
	if err := run(r.Context()); err != nil {
		slog.Error("job failed", "kind", job.Kind, "lun", job.LUN, "err", err)
		writeJSON(w, agentapi.JobResult{Message: fmt.Sprintf("job %s: %v", job.Kind, err)})
		return
	}
	slog.Info("job done", "kind", job.Kind, "lun", job.LUN)
	writeJSON(w, agentapi.JobResult{OK: true})
}

func (a *Agent) getStatus(w http.ResponseWriter, _ *http.Request) {
	a.mu.Lock()
	st := agentapi.Status{
		Generation: a.generation,
		Targets:    map[int64][]agentapi.TargetHealth{},
		LBMetrics:  map[int64]agentapi.LBCounters{},
	}
	a.mu.Unlock()
	st.PasswordResetsDone = a.resetsDone()
	writeJSON(w, st)
}

// resetsDone returns the password resets confirmed to the metadata
// service, if one is among the appliers.
func (a *Agent) resetsDone() []int64 {
	done := []int64{}
	for _, ap := range a.appliers {
		if m, ok := ap.(*Metadata); ok {
			done = append(done, m.Done()...)
		}
	}
	return done
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("write response", "err", err)
	}
}
