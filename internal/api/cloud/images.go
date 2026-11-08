package cloud

import (
	"cmp"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/web"
)

// imageResource is how actions name an image.
const imageResource = "image"

// imageAnswer is an image as Hetzner's API sends it. It is HLab's own type
// because schema.Image also sends "deprecated", which the real API no longer
// sends (measured 2026-11-07).
type imageAnswer struct {
	ID           int64                    `json:"id"`
	Type         string                   `json:"type"`
	Status       string                   `json:"status"`
	Name         *string                  `json:"name"`
	Description  string                   `json:"description"`
	ImageSize    *float64                 `json:"image_size"`
	DiskSize     float64                  `json:"disk_size"`
	Created      time.Time                `json:"created"`
	CreatedFrom  *schema.ImageCreatedFrom `json:"created_from"`
	BoundTo      *int64                   `json:"bound_to"`
	OSFlavor     string                   `json:"os_flavor"`
	OSVersion    *string                  `json:"os_version"`
	RapidDeploy  bool                     `json:"rapid_deploy"`
	Protection   schema.ImageProtection   `json:"protection"`
	Deprecation  *schema.DeprecationInfo  `json:"deprecation"`
	Deleted      *time.Time               `json:"deleted"`
	Labels       map[string]string        `json:"labels"`
	Architecture string                   `json:"architecture"`
}

// imageSortKeys are the sort values of GET /images.
var imageSortKeys = web.SortKeys[imageAnswer]{
	"id":      func(a, b imageAnswer) int { return cmp.Compare(a.ID, b.ID) },
	"name":    func(a, b imageAnswer) int { return cmp.Compare(*a.Name, *b.Name) },
	"created": func(a, b imageAnswer) int { return a.Created.Compare(b.Created) },
}

// imageFilterValues are the values the image filters allow (the spec's
// enums); Hetzner refuses others with "invalid <filter> value".
var imageFilterValues = map[string][]string{
	"type":         {"system", "app", "snapshot", "backup"},
	"status":       {"available", "creating", "unavailable"},
	"architecture": {"x86", "arm"},
}

// registerImages adds the routes of the images. For now these are the
// catalog's system images; snapshots and backups come with their phase.
func registerImages(api *web.API, cat *catalog.Catalog) {
	api.Handle("GET /images", func(req *web.Request) (int, any, error) {
		query := req.URL.Query()
		for filter, allowed := range imageFilterValues {
			for _, value := range query[filter] {
				if !slices.Contains(allowed, value) {
					return 0, nil, web.InvalidValue(filter)
				}
			}
		}
		selector, err := web.ParseSelector(query.Get("label_selector"))
		if err != nil {
			return 0, nil, err
		}
		var images []imageAnswer
		for _, image := range cat.Images {
			if out := imageJSON(image); imageWanted(req, out) && selector.Matches(out.Labels) {
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
				return http.StatusOK, map[string]imageAnswer{"image": imageJSON(image)}, nil
			}
		}
		return 0, nil, web.NotFound("image")
	})
}

// imageWanted tells whether an image passes the filters of GET /images:
// type, status and bound_to (each given once or more), name and architecture.
// Deprecated images only come with include_deprecated=true.
func imageWanted(req *web.Request, image imageAnswer) bool {
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
		(image.Deprecation == nil || query.Get("include_deprecated") == "true")
}

// imageJSON returns a system image of the catalog as the API shows it.
func imageJSON(image catalog.Image) imageAnswer {
	return imageAnswer{
		ID:           image.ID,
		Type:         "system",
		Status:       "available",
		Name:         &image.Name,
		Description:  image.Description,
		DiskSize:     image.DiskSize,
		Created:      image.Created,
		OSFlavor:     image.OSFlavor,
		OSVersion:    &image.OSVersion,
		RapidDeploy:  image.RapidDeploy,
		Deprecation:  deprecationJSON(image.Deprecation),
		Labels:       map[string]string{},
		Architecture: image.Architecture,
	}
}
