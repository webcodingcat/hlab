package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
)

// MinOwnID is the smallest ID of an own server type.
const MinOwnID = 100000

// LoadAll reads the catalog files in shippedDir. The server types come from
// <dataDir>\catalog\server-types.json when present, else from shippedDir;
// the own types of customFile are added to them. A custom file that cannot
// be read or whose types are invalid gives a warning and no own types.
func LoadAll(shippedDir, dataDir, customFile string) (*Catalog, []string, error) {
	c, err := Load(shippedDir)
	if err != nil {
		return nil, nil, err
	}
	dataCatalog := filepath.Join(dataDir, "catalog")
	var types []json.RawMessage
	switch err := readList(dataCatalog, "server-types.json", "server_types", &types); {
	case err == nil:
		if c.serverTypes, err = serverTypes(types); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", filepath.Join(dataCatalog, "server-types.json"), err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return nil, nil, err
	}

	own, err := readOwn(customFile)
	if err == nil {
		err = conflicts(c.serverTypes, own)
	}
	if err != nil {
		return c, []string{fmt.Sprintf("own server types in %s are ignored: %v", customFile, err)}, nil
	}
	c.own = own
	c.serverTypes = append(c.serverTypes, own...)
	return c, nil, nil
}

// ReplaceServerTypes replaces the catalog server types with raw, for
// example after an update, and adds the own types again. Own types that
// now collide with a catalog type are left out with a logged warning that
// OwnTypesWarning reports from then on.
func (c *Catalog) ReplaceServerTypes(raw []json.RawMessage) error {
	types, err := serverTypes(raw)
	if err != nil {
		return fmt.Errorf("server types: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := conflicts(types, c.own); err != nil {
		slog.Warn("own server types are ignored", "err", err)
		c.own = nil
		c.ownWarning = fmt.Sprintf("own server types are ignored after the catalog update: %v", err)
	}
	c.serverTypes = append(types, c.own...)
	return nil
}

// OwnTypesWarning says why ReplaceServerTypes left the own types out, or
// is "" while they are served.
func (c *Catalog) OwnTypesWarning() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ownWarning
}

// readOwn reads the own server types of file; a missing file has none.
func readOwn(file string) ([]ServerType, error) {
	_, own, err := readOwnRaw(file)
	if err != nil {
		return nil, err
	}
	for _, st := range own {
		if st.ID < MinOwnID {
			return nil, fmt.Errorf("server type %q has ID %d, own IDs start at %d", st.Name, st.ID, MinOwnID)
		}
		if st.Name == "" {
			return nil, fmt.Errorf("server type %d has no name", st.ID)
		}
	}
	return own, nil
}

// conflicts reports the first own type whose name or ID is taken by a
// catalog type or by another own type.
func conflicts(types, own []ServerType) error {
	names := map[string]bool{}
	ids := map[int64]bool{}
	for _, st := range types {
		names[st.Name] = true
		ids[st.ID] = true
	}
	for _, st := range own {
		if names[st.Name] {
			return fmt.Errorf("server type name %q is taken", st.Name)
		}
		if ids[st.ID] {
			return fmt.Errorf("server type ID %d is taken", st.ID)
		}
		names[st.Name] = true
		ids[st.ID] = true
	}
	return nil
}

// Pricing returns the pricing object of pricing.json with server_types
// built from Types(): each type with the prices of its object, a type
// without prices with zero prices in each of its locations.
func (c *Catalog) Pricing() json.RawMessage {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(c.pricing, &obj); err != nil || obj == nil {
		obj = map[string]json.RawMessage{}
	}
	type typePrices struct {
		ID     int64           `json:"id"`
		Name   string          `json:"name"`
		Prices json.RawMessage `json:"prices"`
	}
	list := []typePrices{}
	for _, st := range c.Types() {
		var p struct {
			Prices []json.RawMessage `json:"prices"`
		}
		_ = json.Unmarshal(st.Raw, &p) // Raw decoded into st already
		prices, _ := json.Marshal(p.Prices)
		if len(p.Prices) == 0 {
			prices = zeroPrices(st.Locations)
		}
		list = append(list, typePrices{ID: st.ID, Name: st.Name, Prices: prices})
	}
	obj["server_types"], _ = json.Marshal(list)
	out, _ := json.Marshal(obj)
	return out
}

func zeroPrices(locs []ServerTypeLocation) json.RawMessage {
	zero := map[string]string{"net": "0.0000000000", "gross": "0.0000000000"}
	prices := []map[string]any{}
	for _, l := range locs {
		prices = append(prices, map[string]any{
			"location":             l.Name,
			"price_hourly":         zero,
			"price_monthly":        zero,
			"included_traffic":     0,
			"price_per_tb_traffic": zero,
		})
	}
	b, _ := json.Marshal(prices)
	return b
}

// OwnSpec describes an own server type to add.
type OwnSpec struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	CPUType     string   `json:"cpu_type"` // "shared" or "dedicated"
	Cores       int      `json:"cores"`
	Memory      float64  `json:"memory"`    // GB
	Disk        int      `json:"disk"`      // GB
	Locations   []string `json:"locations"` // location names; empty means all
}

// FieldError is an invalid field of an OwnSpec.
type FieldError struct {
	Field   string // "name", "cores", "memory", "disk", "cpu_type", "locations"
	Message string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

// Own returns the own server types served now.
func (c *Catalog) Own() []ServerType {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]ServerType(nil), c.own...)
}

// ReloadOwn re-reads customFile. Invalid or colliding own types are dropped
// and OwnTypesWarning reports why; a valid file clears the warning.
func (c *Catalog) ReloadOwn(customFile string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	types := c.serverTypes[:len(c.serverTypes)-len(c.own)]
	own, err := readOwn(customFile)
	if err == nil {
		err = conflicts(types, own)
	}
	if err != nil {
		slog.Warn("own server types are ignored", "file", customFile, "err", err)
		own = nil
		c.ownWarning = fmt.Sprintf("own server types in %s are ignored: %v", customFile, err)
	} else {
		c.ownWarning = ""
	}
	c.own = own
	c.serverTypes = append(append([]ServerType(nil), types...), own...)
}

// AddOwn checks s against c and the own types of customFile, builds the
// Hetzner server_type object (next free ID from MinOwnID, x86, local
// storage, zero prices, the given locations or all) and rewrites
// customFile in place in one write.
func AddOwn(c *Catalog, customFile string, s OwnSpec) (ServerType, error) {
	raw, own, err := readOwnRaw(customFile)
	if err != nil {
		return ServerType{}, fmt.Errorf("read %s: %w", customFile, err)
	}
	served := c.Types()
	taken := func(match func(ServerType) bool) bool {
		return slices.ContainsFunc(served, match) || slices.ContainsFunc(own, match)
	}
	switch {
	case s.Name == "":
		return ServerType{}, &FieldError{"name", "is required"}
	case taken(func(st ServerType) bool { return st.Name == s.Name }):
		return ServerType{}, &FieldError{"name", fmt.Sprintf("server type %q exists", s.Name)}
	case s.CPUType != "shared" && s.CPUType != "dedicated":
		return ServerType{}, &FieldError{"cpu_type", `must be "shared" or "dedicated"`}
	case s.Cores < 1:
		return ServerType{}, &FieldError{"cores", "must be at least 1"}
	case s.Memory <= 0:
		return ServerType{}, &FieldError{"memory", "must be more than 0"}
	case s.Disk < 1:
		return ServerType{}, &FieldError{"disk", "must be at least 1"}
	}

	locs := []map[string]any{}
	var stLocs []ServerTypeLocation
	add := func(l Location) {
		locs = append(locs, map[string]any{"id": l.ID, "name": l.Name, "available": true, "recommended": false, "deprecation": nil})
		stLocs = append(stLocs, ServerTypeLocation{ID: l.ID, Name: l.Name, Available: true})
	}
	if len(s.Locations) == 0 {
		for _, l := range c.Locations {
			add(l)
		}
	}
	for _, name := range s.Locations {
		l, ok := c.Location(name)
		if !ok {
			return ServerType{}, &FieldError{"locations", fmt.Sprintf("unknown location %q", name)}
		}
		add(l)
	}

	id := int64(MinOwnID)
	for taken(func(st ServerType) bool { return st.ID == id }) {
		id++
	}
	obj, err := json.Marshal(map[string]any{
		"id":           id,
		"name":         s.Name,
		"description":  s.Description,
		"architecture": "x86",
		"cores":        s.Cores,
		"cpu_type":     s.CPUType,
		"category":     "regular_purpose",
		"memory":       s.Memory,
		"disk":         s.Disk,
		"storage_type": "local",
		"deprecation":  nil,
		"prices":       zeroPrices(stLocs),
		"locations":    locs,
	})
	if err != nil {
		return ServerType{}, err
	}
	sts, err := serverTypes([]json.RawMessage{obj})
	if err != nil {
		return ServerType{}, err
	}
	if err := writeOwn(customFile, append(raw, obj)); err != nil {
		return ServerType{}, err
	}
	return sts[0], nil
}

// RemoveOwn removes the own type name from customFile in place; the error
// wraps fs.ErrNotExist when the file has no such type.
func RemoveOwn(customFile, name string) error {
	raw, own, err := readOwnRaw(customFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", customFile, err)
	}
	i := slices.IndexFunc(own, func(st ServerType) bool { return st.Name == name })
	if i < 0 {
		return fmt.Errorf("own server type %q: %w", name, fs.ErrNotExist)
	}
	return writeOwn(customFile, slices.Delete(raw, i, i+1))
}

// readOwnRaw reads the objects of customFile and their decoded types; a
// missing file has none.
func readOwnRaw(file string) ([]json.RawMessage, []ServerType, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, err
	}
	own, err := serverTypes(raw)
	if err != nil {
		return nil, nil, err
	}
	return raw, own, nil
}

// writeOwn writes raw as the JSON array of customFile in place in one
// write, which keeps the file's ACL.
func writeOwn(file string, raw []json.RawMessage) error {
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, append(data, '\n'), 0o644)
}
