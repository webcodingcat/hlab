package cloud

import (
	"cmp"
	"net/http"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/web"
)

// locationSortKeys are the sort values of GET /locations.
var locationSortKeys = web.SortKeys[schema.Location]{
	"id":   func(a, b schema.Location) int { return cmp.Compare(a.ID, b.ID) },
	"name": func(a, b schema.Location) int { return cmp.Compare(a.Name, b.Name) },
}

// registerLocations adds the routes of the locations, from the catalog.
func registerLocations(api *web.API, cat *catalog.Catalog) {
	api.Handle("GET /locations", func(req *web.Request) (int, any, error) {
		var locations []schema.Location
		for _, location := range byName(req, cat.Locations, func(l catalog.Location) string { return l.Name }) {
			locations = append(locations, locationJSON(location))
		}
		if err := web.Sort(req, locations, locationSortKeys); err != nil {
			return 0, nil, err
		}
		return listJSON(req, "locations", locations)
	})

	api.Handle("GET /locations/{id}", func(req *web.Request) (int, any, error) {
		for _, location := range cat.Locations {
			if location.ID == web.PathID(req, "id") {
				return http.StatusOK, schema.LocationGetResponse{Location: locationJSON(location)}, nil
			}
		}
		return 0, nil, web.NotFound("location")
	})
}

// locationJSON returns a location as the API shows it.
func locationJSON(location catalog.Location) schema.Location {
	return schema.Location{
		ID:          location.ID,
		Name:        location.Name,
		Description: location.Description,
		Country:     location.Country,
		City:        location.City,
		Latitude:    location.Latitude,
		Longitude:   location.Longitude,
		NetworkZone: location.NetworkZone,
	}
}
