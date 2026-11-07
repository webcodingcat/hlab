package cloud

import (
	"cmp"
	"net/http"
	"slices"
	"strconv"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/web"
)

// imageSortKeys are the sort values of GET /images.
var imageSortKeys = web.SortKeys[schema.Image]{
	"id":      func(a, b schema.Image) int { return cmp.Compare(a.ID, b.ID) },
	"name":    func(a, b schema.Image) int { return cmp.Compare(*a.Name, *b.Name) },
	"created": func(a, b schema.Image) int { return a.Created.Compare(*b.Created) },
}

// registerImages adds the routes of the images. For now these are the
// catalog's system images; snapshots and backups come with their phase.
func registerImages(api *web.API, cat *catalog.Catalog) {
	api.Handle("GET /images", func(req *web.Request) (int, any, error) {
		selector, err := web.ParseSelector(req.URL.Query().Get("label_selector"))
		if err != nil {
			return 0, nil, err
		}
		var images []schema.Image
		for _, image := range cat.Images {
			if out := imageJSON(image); imageWanted(req, out, image.Deprecation != nil) && selector.Matches(out.Labels) {
				images = append(images, out)
			}
		}
		if err := web.Sort(req, images, imageSortKeys); err != nil {
			return 0, nil, err
		}
		return listJSON(req, "images", images)
	})

	api.Handle("GET /images/{id}", func(req *web.Request) (int, any, error) {
		for _, image := range cat.Images {
			if image.ID == web.PathID(req, "id") {
				return http.StatusOK, schema.ImageGetResponse{Image: imageJSON(image)}, nil
			}
		}
		return 0, nil, web.NotFound("image")
	})
}

// imageWanted tells whether an image passes the filters of GET /images:
// type, status and bound_to (each given once or more), name and architecture.
// Deprecated images only come with include_deprecated=true.
func imageWanted(req *web.Request, image schema.Image, deprecated bool) bool {
	query := req.URL.Query()
	oneOf := func(parameter, value string) bool {
		values, given := query[parameter]
		return !given || slices.Contains(values, value)
	}
	boundTo := ""
	if image.BoundTo != nil {
		boundTo = strconv.FormatInt(*image.BoundTo, 10)
	}
	return oneOf("type", image.Type) &&
		oneOf("status", image.Status) &&
		oneOf("bound_to", boundTo) &&
		oneOf("name", *image.Name) &&
		oneOf("architecture", image.Architecture) &&
		(!deprecated || query.Get("include_deprecated") == "true")
}

// imageJSON returns a system image of the catalog as the API shows it.
func imageJSON(image catalog.Image) schema.Image {
	out := schema.Image{
		ID:           image.ID,
		Status:       "available",
		Type:         "system",
		Name:         &image.Name,
		Description:  image.Description,
		DiskSize:     float32(image.DiskSize),
		Created:      &image.Created,
		OSFlavor:     image.OSFlavor,
		OSVersion:    &image.OSVersion,
		Architecture: image.Architecture,
		RapidDeploy:  image.RapidDeploy,
		Labels:       map[string]string{},
	}
	out.Deprecation = deprecationJSON(image.Deprecation) // the old "deprecated" stays null
	return out
}
