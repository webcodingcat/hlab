// Package catalog loads the shipped catalogs (locations, server types, load
// balancer types, pricing, images) and the user's own server types. The
// files hold Hetzner's API shapes; images carry their download source in
// `_hlab.source`, which is never served.
package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/webcodingcat/hlab/internal/images"
)

// Location is one Hetzner location.
type Location struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	NetworkZone string `json:"network_zone"`
	// Raw is the location object as served.
	Raw json.RawMessage `json:"-"`
}

// ServerTypeLocation is one entry of a server type's locations.
type ServerTypeLocation struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

// ServerType is one server type.
type ServerType struct {
	ID           int64                `json:"id"`
	Name         string               `json:"name"`
	Cores        int                  `json:"cores"`
	Memory       float64              `json:"memory"` // GB
	Disk         int                  `json:"disk"`   // GB
	Architecture string               `json:"architecture"`
	Locations    []ServerTypeLocation `json:"locations"`
	// Raw is the server type object as served.
	Raw json.RawMessage `json:"-"`
}

// Image is one system image.
type Image struct {
	ID           int64         `json:"id"`
	Name         string        `json:"name"`
	Type         string        `json:"type"`
	Status       string        `json:"status"`
	OSFlavor     string        `json:"os_flavor"`
	OSVersion    string        `json:"os_version"`
	Architecture string        `json:"architecture"`
	DiskSize     float64       `json:"disk_size"` // GB
	Deprecated   bool          `json:"-"`
	Source       images.Source `json:"-"`
	// Raw is the image object as served, without `_hlab`.
	Raw json.RawMessage `json:"-"`
}

// Catalog holds the loaded catalogs, each list sorted as in its file.
type Catalog struct {
	Locations         []Location
	ServerTypes       []ServerType
	Images            []Image
	LoadBalancerTypes []json.RawMessage
	Pricing           json.RawMessage
}

// Option changes how Load reads the catalogs.
type Option func(*options)

type options struct{ customServerTypes string }

// WithCustomServerTypes adds the server types of the JSON array in path
// (server-types.custom.json) to the shipped ones. A missing file is no
// error.
func WithCustomServerTypes(path string) Option {
	return func(o *options) { o.customServerTypes = path }
}

// Load reads the catalog files in dir.
func Load(dir string, opts ...Option) (*Catalog, error) {
	var o options
	for _, f := range opts {
		f(&o)
	}
	c := &Catalog{}

	var locs []json.RawMessage
	if err := readList(dir, "locations.json", "locations", &locs); err != nil {
		return nil, err
	}
	for _, raw := range locs {
		var l Location
		if err := json.Unmarshal(raw, &l); err != nil {
			return nil, fmt.Errorf("locations.json: %w", err)
		}
		l.Raw = raw
		c.Locations = append(c.Locations, l)
	}

	var types []json.RawMessage
	if err := readList(dir, "server-types.json", "server_types", &types); err != nil {
		return nil, err
	}
	if o.customServerTypes != "" {
		data, err := os.ReadFile(o.customServerTypes)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return nil, err
		default:
			var custom []json.RawMessage
			if err := json.Unmarshal(data, &custom); err != nil {
				return nil, fmt.Errorf("%s: %w", o.customServerTypes, err)
			}
			types = append(types, custom...)
		}
	}
	for _, raw := range types {
		st, err := c.serverType(raw)
		if err != nil {
			return nil, fmt.Errorf("server types: %w", err)
		}
		c.ServerTypes = append(c.ServerTypes, st)
	}

	var imgs []json.RawMessage
	if err := readList(dir, "images.json", "images", &imgs); err != nil {
		return nil, err
	}
	for _, raw := range imgs {
		img, err := image(raw)
		if err != nil {
			return nil, fmt.Errorf("images.json: %w", err)
		}
		c.Images = append(c.Images, img)
	}

	if err := readList(dir, "load-balancer-types.json", "load_balancer_types", &c.LoadBalancerTypes); err != nil {
		return nil, err
	}
	var pricing struct {
		Pricing json.RawMessage `json:"pricing"`
	}
	if err := readJSON(dir, "pricing.json", &pricing); err != nil {
		return nil, err
	}
	c.Pricing = pricing.Pricing
	return c, nil
}

// serverType decodes raw and adds an entry for every location when the
// object has no locations list, so clients always find one.
func (c *Catalog) serverType(raw json.RawMessage) (ServerType, error) {
	var st ServerType
	if err := json.Unmarshal(raw, &st); err != nil {
		return st, err
	}
	if st.Locations == nil {
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			return st, err
		}
		locs := make([]map[string]any, 0, len(c.Locations))
		for _, l := range c.Locations {
			st.Locations = append(st.Locations, ServerTypeLocation{ID: l.ID, Name: l.Name, Available: true})
			locs = append(locs, map[string]any{
				"id": l.ID, "name": l.Name, "available": true, "recommended": false, "deprecation": nil,
			})
		}
		obj["locations"] = locs
		b, err := json.Marshal(obj)
		if err != nil {
			return st, err
		}
		raw = b
	}
	st.Raw = raw
	return st, nil
}

// image decodes raw and splits off `_hlab`.
func image(raw json.RawMessage) (Image, error) {
	var img Image
	if err := json.Unmarshal(raw, &img); err != nil {
		return img, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return img, err
	}
	var extra struct {
		Source struct {
			URL         string `json:"url"`
			ChecksumURL string `json:"checksum_url"`
			Format      string `json:"format"`
		} `json:"source"`
	}
	if h, ok := obj["_hlab"]; ok {
		if err := json.Unmarshal(h, &extra); err != nil {
			return img, fmt.Errorf("%s: _hlab: %w", img.Name, err)
		}
		delete(obj, "_hlab")
	}
	img.Source = images.Source{URL: extra.Source.URL, ChecksumURL: extra.Source.ChecksumURL, Format: extra.Source.Format}
	img.Deprecated = string(obj["deprecation"]) != "" && string(obj["deprecation"]) != "null"
	b, err := json.Marshal(obj)
	if err != nil {
		return img, err
	}
	img.Raw = b
	return img, nil
}

// ImageSources maps the name of every image with a source to it, for
// images.Options.Catalog.
func (c *Catalog) ImageSources() map[string]images.Source {
	m := map[string]images.Source{}
	for _, img := range c.Images {
		if img.Source.URL != "" {
			m[img.Name] = img.Source
		}
	}
	return m
}

// ServerType returns the server type with the given name.
func (c *Catalog) ServerType(name string) (ServerType, bool) {
	for _, st := range c.ServerTypes {
		if st.Name == name {
			return st, true
		}
	}
	return ServerType{}, false
}

// Location returns the location with the given name.
func (c *Catalog) Location(name string) (Location, bool) {
	for _, l := range c.Locations {
		if l.Name == name {
			return l, true
		}
	}
	return Location{}, false
}

// Image returns the image with the given name.
func (c *Catalog) Image(name string) (Image, bool) {
	for _, img := range c.Images {
		if img.Name == name {
			return img, true
		}
	}
	return Image{}, false
}

func readList(dir, file, key string, items *[]json.RawMessage) error {
	var obj map[string]json.RawMessage
	if err := readJSON(dir, file, &obj); err != nil {
		return err
	}
	list, ok := obj[key]
	if !ok {
		return fmt.Errorf("%s: no %q list", file, key)
	}
	if err := json.Unmarshal(list, items); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	return nil
}

func readJSON(dir, file string, v any) error {
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return fmt.Errorf("read catalog: %w", err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	return nil
}
