//go:build windows

package cloud

import (
	"net/http"
	"slices"

	"github.com/webcodingcat/hlab/internal/catalog"
)

// APIRoutes returns the http.ServeMux pattern of every endpoint the Hetzner
// API of hlabd serves, relative to /v1 (for example
// "POST /servers/{id}/actions/poweron"), sorted and without duplicates.
// It builds the same module list as cloud start (Cloud.modules) without
// opening the store, the platform or the agent, and records what each
// module's Routes registers.
func APIRoutes() []string {
	var rec routeRecorder
	// The constructors read the catalog; an empty one is enough for the
	// routes, which do not depend on its content.
	for _, m := range (&Cloud{cat: &catalog.Catalog{}}).modules() {
		m.Routes(&rec)
	}
	slices.Sort(rec)
	return slices.Compact(rec)
}

// routeRecorder is a resource.Registrar that keeps only the patterns.
type routeRecorder []string

func (r *routeRecorder) Handle(pattern string, _ http.Handler) {
	*r = append(*r, pattern)
}
