package cloud

import (
	"cmp"
	"net/http"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/web"
)

// isoSortKeys are the sort values of GET /isos, which the spec does not list
// but hcloud-go sends and the real API takes (measured 2026-11-07).
var isoSortKeys = web.SortKeys[schema.ISO]{
	"id":   func(a, b schema.ISO) int { return cmp.Compare(a.ID, b.ID) },
	"name": func(a, b schema.ISO) int { return cmp.Compare(a.Name, b.Name) },
}

// registerISOs adds the routes of the ISOs, from the catalog. Mounting one
// comes with its phase.
func registerISOs(api *web.API, cat *catalog.Catalog) {
	api.Handle("GET /isos", func(req *web.Request) (int, any, error) {
		query := req.URL.Query()
		architecture := query.Get("architecture")
		// with include_architecture_wildcard, ISOs that fit every
		// architecture pass an architecture filter too
		wildcard := query.Get("include_architecture_wildcard") == "true"
		var isos []schema.ISO
		for _, iso := range byName(req, cat.ISOs, func(i catalog.ISO) string { return i.Name }) {
			if architecture == "" || iso.Architecture == architecture || (wildcard && iso.Architecture == "") {
				isos = append(isos, isoJSON(iso))
			}
		}
		if err := web.Sort(req, isos, isoSortKeys); err != nil {
			return 0, nil, err
		}
		return listJSON(req, "isos", isos)
	})

	api.Handle("GET /isos/{id}", func(req *web.Request) (int, any, error) {
		for _, iso := range cat.ISOs {
			if iso.ID == web.PathID(req, "id") {
				return http.StatusOK, schema.ISOGetResponse{ISO: isoJSON(iso)}, nil
			}
		}
		return 0, nil, web.NotFound("iso")
	})
}

// isoJSON returns a public ISO as the API shows it; one without an
// architecture fits every architecture and shows null.
func isoJSON(iso catalog.ISO) schema.ISO {
	out := schema.ISO{ID: iso.ID, Name: iso.Name, Description: iso.Description, Type: "public"}
	if iso.Architecture != "" {
		out.Architecture = &iso.Architecture
	}
	out.Deprecation = deprecationJSON(iso.Deprecation)
	return out
}
