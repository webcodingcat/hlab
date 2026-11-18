// Package catalog loads the shipped catalogs (locations, server types, load
// balancer types, pricing, images, ISOs). The files hold Hetzner's API
// shapes; images and ISOs carry their download source in `_hlab.source`,
// which is never served.
package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

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

// ISO is one catalog ISO.
type ISO struct {
	ID           int64         `json:"id"`
	Name         string        `json:"name"`
	Architecture string        `json:"architecture"` // "" for a wildcard (null)
	Deprecated   bool          `json:"-"`
	Source       images.Source `json:"-"` // Format "iso"
	// Raw is the ISO object as served, without `_hlab`.
	Raw json.RawMessage `json:"-"`
}

// Catalog holds the loaded catalogs, each list sorted as in its file. The
// server types can be replaced at run time (catalog update) and are only
// reached through the guarded methods.
type Catalog struct {
	Locations         []Location
	Images            []Image
	ISOs              []ISO
	LoadBalancerTypes []json.RawMessage

	mu          sync.RWMutex
	serverTypes []ServerType // catalog types followed by the own types
	own         []ServerType // own types, merged again after an update
	ownWarning  string       // why an update dropped the own types
	pricing     json.RawMessage
}

// Load reads the catalog files in dir.
func Load(dir string) (*Catalog, error) {
	c := &Catalog{}
	var err error

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
	if c.serverTypes, err = serverTypes(types); err != nil {
		return nil, fmt.Errorf("server-types.json: %w", err)
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

	var isos []json.RawMessage
	if err := readList(dir, "isos.json", "isos", &isos); err != nil {
		return nil, err
	}
	for _, raw := range isos {
		var iso ISO
		if err := json.Unmarshal(raw, &iso); err != nil {
			return nil, fmt.Errorf("isos.json: %w", err)
		}
		if iso.Raw, iso.Source, iso.Deprecated, err = splitHlab(raw); err != nil {
			return nil, fmt.Errorf("isos.json: %s: %w", iso.Name, err)
		}
		iso.Source.Format = "iso"
		c.ISOs = append(c.ISOs, iso)
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
	c.pricing = pricing.Pricing
	return c, nil
}

// serverTypes decodes the server type objects raw.
func serverTypes(raw []json.RawMessage) ([]ServerType, error) {
	out := make([]ServerType, 0, len(raw))
	for _, r := range raw {
		var st ServerType
		if err := json.Unmarshal(r, &st); err != nil {
			return nil, err
		}
		st.Raw = r
		out = append(out, st)
	}
	return out, nil
}

// Types returns the catalog server types followed by the own types.
func (c *Catalog) Types() []ServerType {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]ServerType(nil), c.serverTypes...)
}

// ServerTypeByRef returns the server type with the given name or ID.
func (c *Catalog) ServerTypeByRef(ref string) (ServerType, bool) {
	if st, ok := c.ServerType(ref); ok {
		return st, true
	}
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil {
		return ServerType{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, st := range c.serverTypes {
		if st.ID == id {
			return st, true
		}
	}
	return ServerType{}, false
}

// image decodes raw and splits off `_hlab`.
func image(raw json.RawMessage) (Image, error) {
	var img Image
	if err := json.Unmarshal(raw, &img); err != nil {
		return img, err
	}
	var err error
	if img.Raw, img.Source, img.Deprecated, err = splitHlab(raw); err != nil {
		return img, fmt.Errorf("%s: %w", img.Name, err)
	}
	return img, nil
}

// splitHlab returns the object raw without `_hlab`, the source in
// `_hlab.source` and whether the object is deprecated.
func splitHlab(raw json.RawMessage) (json.RawMessage, images.Source, bool, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, images.Source{}, false, err
	}
	var extra struct {
		Source struct {
			URL                 string `json:"url"`
			ChecksumURL         string `json:"checksum_url"`
			Format              string `json:"format"`
			FallbackURL         string `json:"fallback_url"`
			FallbackChecksumURL string `json:"fallback_checksum_url"`
		} `json:"source"`
	}
	if h, ok := obj["_hlab"]; ok {
		if err := json.Unmarshal(h, &extra); err != nil {
			return nil, images.Source{}, false, fmt.Errorf("_hlab: %w", err)
		}
		delete(obj, "_hlab")
	}
	src := images.Source{
		URL:                 extra.Source.URL,
		ChecksumURL:         extra.Source.ChecksumURL,
		Format:              extra.Source.Format,
		FallbackURL:         extra.Source.FallbackURL,
		FallbackChecksumURL: extra.Source.FallbackChecksumURL,
	}
	deprecated := string(obj["deprecation"]) != "" && string(obj["deprecation"]) != "null"
	b, err := json.Marshal(obj)
	if err != nil {
		return nil, images.Source{}, false, err
	}
	return b, src, deprecated, nil
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

// ISO returns the ISO with the given name or ID.
func (c *Catalog) ISO(ref string) (ISO, bool) {
	id, err := strconv.ParseInt(ref, 10, 64)
	for _, iso := range c.ISOs {
		if iso.Name == ref || (err == nil && iso.ID == id) {
			return iso, true
		}
	}
	return ISO{}, false
}

// ISOSources maps the name of every ISO to its source, for
// images.Options.ISOs.
func (c *Catalog) ISOSources() map[string]images.Source {
	m := make(map[string]images.Source, len(c.ISOs))
	for _, iso := range c.ISOs {
		m[iso.Name] = iso.Source
	}
	return m
}

// ServerType returns the server type with the given name.
func (c *Catalog) ServerType(name string) (ServerType, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, st := range c.serverTypes {
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
