// Package storageagent is hlab-agent in role storage, the program on the
// storage VM. hlabd pushes the full desired state of the storage boxes
// (agentapi.Storage) with PUT /v1/storage and runs jobs with POST
// /v1/jobs.
package storageagent

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

// maxDocBytes and maxJobBytes limit the size of a request body.
const (
	maxDocBytes = 64 << 20
	maxJobBytes = 64 << 10
)

// Options says where the storage lives.
type Options struct {
	DataDisk string // block device of the data disk (SCSI LUN 1)
	Root     string // mount point of the data disk, "/srv/sbox"
}

// Agent serves the storage agent's HTTP interface.
type Agent struct {
	auth []byte // "Bearer <secret>"
	o    Options

	mu         sync.Mutex // serializes applies and jobs
	generation int
	doc        agentapi.Storage // the document applied last, also when its apply failed
	mounted    bool   // the data disk is mounted at Root
	sshd       string //nolint:unused // the sshd configuration applied last; used on Linux
}

// jobError is a failed job with the HTTP status and code it answers.
type jobError struct {
	status int
	code   string
}

func (e *jobError) Error() string { return e.code }

// New returns an Agent that accepts requests carrying secret.
func New(secret []byte, o Options) (*Agent, error) {
	if len(secret) == 0 {
		return nil, errors.New("storage agent: empty secret")
	}
	if o.DataDisk == "" || o.Root == "" {
		return nil, errors.New("storage agent: data disk and root are required")
	}
	return &Agent{auth: append([]byte("Bearer "), secret...), o: o}, nil
}

// Handler returns the HTTP handler of the agent's interface.
func (a *Agent) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /v1/storage", a.putStorage)
	mux.HandleFunc("POST /v1/jobs", a.postJob)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), a.auth) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (a *Agent) putStorage(w http.ResponseWriter, r *http.Request) {
	var doc agentapi.Storage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDocBytes)).Decode(&doc); err != nil {
		http.Error(w, "invalid document: "+err.Error(), http.StatusBadRequest)
		return
	}
	if doc.Schema != agentapi.StorageSchema {
		http.Error(w, fmt.Sprintf("unsupported schema %d, want %d", doc.Schema, agentapi.StorageSchema), http.StatusBadRequest)
		return
	}
	if err := validate(doc); err != nil {
		http.Error(w, "invalid document: "+err.Error(), http.StatusBadRequest)
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if doc.Generation < a.generation {
		http.Error(w, fmt.Sprintf("generation %d is older than the applied generation %d", doc.Generation, a.generation), http.StatusConflict)
		return
	}
	err := a.apply(r.Context(), doc)
	a.doc = doc
	if err != nil {
		slog.Error("storage apply failed", "generation", doc.Generation, "err", err)
		http.Error(w, "apply: "+err.Error(), http.StatusInternalServerError)
		return
	}
	a.generation = doc.Generation
	slog.Info("storage applied", "generation", doc.Generation, "boxes", len(doc.Boxes))
	writeJSON(w, http.StatusOK, struct{ Generation int }{doc.Generation})
}

// postJob runs one job synchronously. A malformed request or an unknown
// kind answers 400; a refused job its status with an
// agentapi.StorageJobError; any other failure 500.
func (a *Agent) postJob(w http.ResponseWriter, r *http.Request) {
	var job agentapi.StorageJob
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJobBytes)).Decode(&job); err != nil {
		http.Error(w, "invalid job: "+err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := boxUID(job.Box); err != nil {
		http.Error(w, "invalid job: "+err.Error(), http.StatusBadRequest)
		return
	}
	if job.Kind == agentapi.StorageJobSnapshot || job.Kind == agentapi.StorageJobDeleteSnapshot ||
		job.Kind == agentapi.StorageJobRollback {
		for _, name := range append([]string{job.Snapshot}, job.Newer...) {
			if !snapshotNameRE.MatchString(name) {
				http.Error(w, fmt.Sprintf("invalid job: invalid snapshot name %q", name), http.StatusBadRequest)
				return
			}
		}
	}
	var run func(context.Context, agentapi.StorageJob) (agentapi.StorageJobResult, error)
	switch job.Kind {
	case agentapi.StorageJobFolders:
		run = a.folders
	case agentapi.StorageJobUsage:
		run = a.usage
	case agentapi.StorageJobSnapshot:
		run = a.snapshot
	case agentapi.StorageJobDeleteSnapshot:
		run = a.deleteSnapshotJob
	case agentapi.StorageJobRollback:
		run = a.rollback
	default:
		http.Error(w, fmt.Sprintf("unknown job kind %q", job.Kind), http.StatusBadRequest)
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.mounted {
		http.Error(w, "storage not applied yet", http.StatusServiceUnavailable)
		return
	}
	res, err := run(r.Context(), job)
	var je *jobError
	switch {
	case errors.As(err, &je):
		writeJSON(w, je.status, agentapi.StorageJobError{Error: je.code})
	case err != nil:
		slog.Error("storage job failed", "kind", job.Kind, "box", job.Box, "err", err)
		http.Error(w, fmt.Sprintf("job %s: %v", job.Kind, err), http.StatusInternalServerError)
	default:
		writeJSON(w, http.StatusOK, res)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("write response", "err", err)
	}
}
