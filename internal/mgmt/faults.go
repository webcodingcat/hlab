package mgmt

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/webcodingcat/hlab/internal/faults"
)

// faultRoutes adds the fault routes of the management API to mux.
func (s server) faultRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+Prefix+"/faults", s.listAllFaults)
	mux.HandleFunc("DELETE "+Prefix+"/faults", s.endAllFaults)
	mux.HandleFunc("GET "+Prefix+"/projects/{name}/faults", s.listFaults)
	mux.HandleFunc("POST "+Prefix+"/projects/{name}/faults", s.createFault)
	mux.HandleFunc("DELETE "+Prefix+"/projects/{name}/faults", s.endProjectFaults)
	mux.HandleFunc("DELETE "+Prefix+"/projects/{name}/faults/{id}", s.endFault)
}

// failFault answers an error of the fault manager.
func failFault(w http.ResponseWriter, err error) {
	var in *faults.InputError
	switch {
	case errors.As(err, &in):
		writeError(w, http.StatusBadRequest, "invalid_input", in.Error())
	case errors.Is(err, faults.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, faults.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	default:
		slog.Error("management fault request", "err", err)
		writeError(w, http.StatusInternalServerError, "server_error", err.Error())
	}
}

func (s server) listAllFaults(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"faults": s.d.Faults.List("")})
}

func (s server) endAllFaults(w http.ResponseWriter, req *http.Request) {
	if err := s.d.Faults.EndAll(req.Context()); err != nil {
		failFault(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s server) listFaults(w http.ResponseWriter, req *http.Request) {
	p, ok := s.project(w, req)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"faults": s.d.Faults.List(p.Name)})
}

func (s server) createFault(w http.ResponseWriter, req *http.Request) {
	p, ok := s.project(w, req)
	if !ok {
		return
	}
	var body faults.Request
	dec := json.NewDecoder(req.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "invalid JSON body: "+err.Error())
		return
	}
	spec, err := body.Spec()
	if err != nil {
		failFault(w, err)
		return
	}
	f, err := s.d.Faults.Create(req.Context(), p.Name, spec)
	if err != nil {
		failFault(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"fault": f})
}

func (s server) endProjectFaults(w http.ResponseWriter, req *http.Request) {
	p, ok := s.project(w, req)
	if !ok {
		return
	}
	if err := s.d.Faults.EndProject(req.Context(), p.Name); err != nil {
		failFault(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s server) endFault(w http.ResponseWriter, req *http.Request) {
	p, ok := s.project(w, req)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusNotFound, "not_found", "fault not found")
		return
	}
	if err := s.d.Faults.End(req.Context(), p.Name, id); err != nil {
		failFault(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
