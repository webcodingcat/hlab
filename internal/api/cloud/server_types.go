package cloud

import (
	"cmp"
	"net/http"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/web"
)

// serverTypeAnswer is a server type as Hetzner's API sends it. It is HLab's
// own type because schema.ServerType also sends "deprecated" and
// "included_traffic", which the real API no longer sends (measured
// 2026-11-07).
type serverTypeAnswer struct {
	ID           int64                           `json:"id"`
	Name         string                          `json:"name"`
	Description  string                          `json:"description"`
	Category     string                          `json:"category"`
	Cores        int                             `json:"cores"`
	Memory       float64                         `json:"memory"`
	Disk         int                             `json:"disk"`
	StorageType  string                          `json:"storage_type"`
	CPUType      string                          `json:"cpu_type"`
	Architecture string                          `json:"architecture"`
	Deprecation  *schema.DeprecationInfo         `json:"deprecation"`
	Locations    []schema.ServerTypeLocation     `json:"locations"`
	Prices       []schema.PricingServerTypePrice `json:"prices"`
}

// serverTypeSortKeys are the sort values of GET /server_types, which the
// spec does not list but hcloud-go sends and the real API takes.
var serverTypeSortKeys = web.SortKeys[serverTypeAnswer]{
	"id":   func(a, b serverTypeAnswer) int { return cmp.Compare(a.ID, b.ID) },
	"name": func(a, b serverTypeAnswer) int { return cmp.Compare(a.Name, b.Name) },
}

// registerServerTypes adds the routes of the server types, from the catalog.
func registerServerTypes(api *web.API, cat *catalog.Catalog) {
	api.Handle("GET /server_types", func(req *web.Request) (int, any, error) {
		var serverTypes []serverTypeAnswer
		for _, serverType := range byName(req, cat.ServerTypes, func(t catalog.ServerType) string { return t.Name }) {
			serverTypes = append(serverTypes, serverTypeJSON(cat, serverType))
		}
		if err := web.Sort(req, serverTypes, serverTypeSortKeys); err != nil {
			// Hetzner's answer to an unknown sort value here (measured
			// 2026-11-07)
			return 0, nil, web.ServiceError("internal error")
		}
		return listJSON(req, "server_types", serverTypes)
	})

	api.Handle("GET /server_types/{id}", func(req *web.Request) (int, any, error) {
		for _, serverType := range cat.ServerTypes {
			if serverType.ID == web.PathID(req, "id") {
				return http.StatusOK, map[string]serverTypeAnswer{"server_type": serverTypeJSON(cat, serverType)}, nil
			}
		}
		return 0, nil, web.NotFound("server type")
	})
}

// serverTypeJSON returns a server type as the API shows it, with the IDs of
// its locations.
func serverTypeJSON(cat *catalog.Catalog, serverType catalog.ServerType) serverTypeAnswer {
	out := serverTypeAnswer{
		ID:           serverType.ID,
		Name:         serverType.Name,
		Description:  serverType.Description,
		Category:     serverType.Category,
		Cores:        serverType.Cores,
		Memory:       serverType.Memory,
		Disk:         serverType.Disk,
		StorageType:  serverType.StorageType,
		CPUType:      serverType.CPUType,
		Architecture: serverType.Architecture,
		Deprecation:  deprecationJSON(serverType.Deprecation),
		Locations:    []schema.ServerTypeLocation{},
		Prices:       typePricesJSON(serverType.Prices),
	}
	for _, typeLocation := range serverType.Locations {
		location, _ := cat.LocationByName(typeLocation.Name) // the catalog's check found it
		entry := schema.ServerTypeLocation{
			ID:          location.ID,
			Name:        location.Name,
			Recommended: typeLocation.Recommended,
			Available:   typeLocation.Available,
		}
		entry.Deprecation = deprecationJSON(typeLocation.Deprecation)
		out.Locations = append(out.Locations, entry)
	}
	return out
}
