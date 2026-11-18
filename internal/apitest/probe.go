package apitest

import (
	"encoding/json"
	"net/http"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
)

// ProbeModule returns a minimal resource module for tests of the API core
// (authentication, read-only guard) that runs before the real modules
// exist. It serves, in Hetzner's response shapes:
//
//   - GET /ssh_keys: one SSH key whose name is the name of the project the
//     request's token selected,
//   - POST /ssh_keys: 201 with an SSH key named after that project.
func ProbeModule() resource.Module { return probe{} }

type probe struct{}

func (probe) Type() string { return "ssh_keys" }

func (probe) Routes(r resource.Registrar) {
	r.Handle("GET /ssh_keys", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ssh_keys": []any{probeKey(req)},
			"meta": map[string]any{"pagination": map[string]any{
				"page": 1, "per_page": 25, "previous_page": nil, "next_page": nil,
				"last_page": 1, "total_entries": 1,
			}},
		})
	}))
	r.Handle("POST /ssh_keys", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]any{"ssh_key": probeKey(req)})
	}))
}

func probeKey(req *http.Request) map[string]any {
	name := ""
	if p, ok := api.ProjectFrom(req.Context()); ok {
		name = p.Name
	}
	return map[string]any{
		"id":          1,
		"name":        name,
		"fingerprint": "b7:2f:30:a0:2f:6c:58:6c:21:04:58:61:ba:06:3b:2f",
		"public_key":  "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIProbeKeyForTestsOnly probe",
		"labels":      map[string]string{},
		"created":     "2026-11-18T00:00:00Z",
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
