//go:build windows

package cloud

import (
	"net/http"
	"slices"
	"strings"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resource"
)

// APIRoutes returns the http.ServeMux pattern of every endpoint the Hetzner
// and Cloud API of hlabd serves, relative to /v1 (for example
// "POST /servers/{id}/actions/poweron") or, for a resource.Prefixed module,
// with its full prefix (for example "GET /hetzner/v1/storage_boxes"),
// sorted and without duplicates.
// It builds the same module list as cloud start (Cloud.modules) without
// opening the store, the platform or the agent, and records what each
// module's Routes registers.
func APIRoutes() []string {
	var rec routeRecorder
	// The constructors read the catalog; an empty one is enough for the
	// routes, which do not depend on its content.
	for _, m := range (&Cloud{cat: &catalog.Catalog{}}).modules() {
		rec.prefix = ""
		if p, ok := m.(resource.Prefixed); ok {
			rec.prefix = p.Prefix()
		}
		m.Routes(&rec)
	}
	slices.Sort(rec.patterns)
	return slices.Compact(rec.patterns)
}

// routeRecorder is a resource.Registrar that keeps only the patterns, with
// prefix put before the path.
type routeRecorder struct {
	prefix   string
	patterns []string
}

func (r *routeRecorder) Handle(pattern string, _ http.Handler) {
	method, path, _ := strings.Cut(pattern, " ")
	r.patterns = append(r.patterns, method+" "+r.prefix+path)
}
