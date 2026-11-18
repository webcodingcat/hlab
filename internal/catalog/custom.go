package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
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
// now collide with a catalog type are left out with a logged warning.
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
	}
	c.serverTypes = append(types, c.own...)
	return nil
}

// readOwn reads the own server types of file; a missing file has none.
func readOwn(file string) ([]ServerType, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	own, err := serverTypes(raw)
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
