//go:build linux

package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/webcodingcat/hlab/internal/agentproto"
)

// maxPicture limits a picture's size: the largest, the ruleset of a full
// lab, has a few hundred kilobytes.
const maxPicture = 64 << 20

// Options configure an agent.
type Options struct {
	Version string
	// Dir holds the files the agent writes, such as dnsmasq's
	// configuration.
	Dir string
	// RestartDHCP is the command that restarts dnsmasq after its
	// configuration changed.
	RestartDHCP []string
	Log         *slog.Logger
}

// Agent applies pictures; its methods may be called concurrently.
type Agent struct {
	opts Options
	// applying lets one picture change the appliance at a time.
	applying sync.Mutex
	// mu guards generations.
	mu          sync.Mutex
	generations map[string]uint64
	metadata    atomic.Pointer[agentproto.Metadata]
	announcer   *announcer
}

// New returns an agent that has applied no picture yet.
func New(opts Options) *Agent {
	agent := &Agent{opts: opts, generations: map[string]uint64{}}
	agent.metadata.Store(&agentproto.Metadata{})
	agent.announcer = newAnnouncer(opts.Log)
	return agent
}

// Handler returns the agent's API for hlabd (agentproto).
func (a *Agent) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+agentproto.PathStatus, a.status)
	mux.HandleFunc("PUT "+agentproto.PathPictures+"{name}", a.putPicture)
	return mux
}

// status reports the version and the pictures applied so far.
func (a *Agent) status(w http.ResponseWriter, _ *http.Request) {
	a.mu.Lock()
	generations := maps.Clone(a.generations)
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, agentproto.Status{Version: a.opts.Version, Generations: generations})
}

// applyError is an error with the status the API answers it with.
type applyError struct {
	status int
	err    error
}

func (e *applyError) Error() string { return e.err.Error() }
func (e *applyError) Unwrap() error { return e.err }

// refused marks an error after which the appliance is unchanged (422).
func refused(err error) error {
	return &applyError{status: http.StatusUnprocessableEntity, err: err}
}

// putPicture applies the picture in the body.
func (a *Agent) putPicture(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPicture))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Message: err.Error()})
		return
	}
	var header struct {
		Generation uint64 `json:"generation"`
	}
	if err := json.Unmarshal(body, &header); err != nil {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Message: err.Error()})
		return
	}
	a.applying.Lock()
	defer a.applying.Unlock()
	started := time.Now()
	switch name {
	case agentproto.PictureLinks:
		err = applyPicture(body, a.applyLinks)
	case agentproto.PictureRuleset:
		err = applyPicture(body, applyRuleset)
	case agentproto.PictureDHCP:
		err = applyPicture(body, a.applyDHCP)
	case agentproto.PictureMetadata:
		err = applyPicture(body, a.applyMetadata)
	default:
		writeJSON(w, http.StatusNotFound, agentproto.Error{Message: "no picture " + name})
		return
	}
	if err != nil {
		status := http.StatusInternalServerError
		if applyErr, ok := errors.AsType[*applyError](err); ok {
			status = applyErr.status
		}
		a.opts.Log.Error("picture not applied", "picture", name,
			"generation", header.Generation, "status", status, "error", err)
		writeJSON(w, status, agentproto.Error{Message: err.Error()})
		return
	}
	a.mu.Lock()
	a.generations[name] = header.Generation
	a.mu.Unlock()
	a.opts.Log.Info("picture applied", "picture", name,
		"generation", header.Generation, "took", time.Since(started))
	w.WriteHeader(http.StatusNoContent)
}

// checked is a picture of agentproto, which can check itself.
type checked interface {
	Check() error
}

// applyPicture decodes a picture, checks it and applies it. Unknown fields
// are refused: they would come from a newer hlabd, whose picture this agent
// would apply only in part.
func applyPicture[P checked](body []byte, apply func(P) error) error {
	var wanted P
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wanted); err != nil {
		return &applyError{status: http.StatusBadRequest, err: err}
	}
	if err := wanted.Check(); err != nil {
		return refused(err)
	}
	return apply(wanted)
}

// applyMetadata makes the metadata service answer from the picture.
func (a *Agent) applyMetadata(metadata agentproto.Metadata) error {
	a.metadata.Store(&metadata)
	return nil
}

// writeJSON answers with a JSON body.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// an error means the connection broke; hlabd asks again
	_ = json.NewEncoder(w).Encode(body)
}
