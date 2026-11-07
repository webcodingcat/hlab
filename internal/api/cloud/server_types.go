package cloud

import (
	"net/http"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/web"
)

// registerServerTypes adds the routes of the server types, from the catalog.
func registerServerTypes(api *web.API, cat *catalog.Catalog) {
	api.Handle("GET /server_types", func(req *web.Request) (int, any, error) {
		var serverTypes []schema.ServerType
		for _, serverType := range byName(req, cat.ServerTypes, func(t catalog.ServerType) string { return t.Name }) {
			serverTypes = append(serverTypes, serverTypeJSON(cat, serverType))
		}
		return listJSON(req, "server_types", serverTypes)
	})

	api.Handle("GET /server_types/{id}", func(req *web.Request) (int, any, error) {
		for _, serverType := range cat.ServerTypes {
			if serverType.ID == web.PathID(req, "id") {
				return http.StatusOK, schema.ServerTypeGetResponse{ServerType: serverTypeJSON(cat, serverType)}, nil
			}
		}
		return 0, nil, web.NotFound("server type")
	})
}

// serverTypeJSON returns a server type as the API shows it, with the IDs of
// its locations.
func serverTypeJSON(cat *catalog.Catalog, serverType catalog.ServerType) schema.ServerType {
	out := schema.ServerType{
		ID:           serverType.ID,
		Name:         serverType.Name,
		Description:  serverType.Description,
		Category:     serverType.Category,
		Cores:        serverType.Cores,
		Memory:       float32(serverType.Memory),
		Disk:         serverType.Disk,
		StorageType:  serverType.StorageType,
		CPUType:      serverType.CPUType,
		Architecture: serverType.Architecture,
		Prices:       typePricesJSON(serverType.Prices),
		Locations:    []schema.ServerTypeLocation{},
	}
	// the old "deprecated" stays false: Hetzner phases it out for the
	// deprecation of the type and of each location
	out.Deprecation = deprecationJSON(serverType.Deprecation)
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
