package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
)

// OwnStorageBoxTypeSpec describes an own storage box type to add or update.
type OwnStorageBoxTypeSpec struct {
	Name                   string   `json:"name"`
	Description            string   `json:"description"`
	Size                   int64    `json:"size"`                     // bytes, >= 1
	SnapshotLimit          int      `json:"snapshot_limit"`           // >= 0
	AutomaticSnapshotLimit int      `json:"automatic_snapshot_limit"` // >= 0
	SubaccountsLimit       int      `json:"subaccounts_limit"`        // >= 0
	Locations              []string `json:"locations"`                // catalog location names, at least one
}

// AddOwnStorageBoxType checks s against the served types of c and the own
// types of file, builds the Hetzner storage_box_type object (next free ID
// from MinOwnID, deprecation null, one price entry per location with every
// price 0) and rewrites file in place in one write.
func AddOwnStorageBoxType(c *Catalog, file string, s OwnStorageBoxTypeSpec) (StorageBoxType, error) {
	raw, own, err := readOwnBoxRaw(file)
	if err != nil {
		return StorageBoxType{}, fmt.Errorf("read %s: %w", file, err)
	}
	served := c.StorageBoxTypes()
	taken := func(match func(StorageBoxType) bool) bool {
		return slices.ContainsFunc(served, match) || slices.ContainsFunc(own, match)
	}
	switch {
	case s.Name == "":
		return StorageBoxType{}, &FieldError{"name", "is required"}
	case taken(func(st StorageBoxType) bool { return st.Name == s.Name }):
		return StorageBoxType{}, &FieldError{"name", fmt.Sprintf("storage box type %q exists", s.Name)}
	}
	id := int64(MinOwnID)
	for taken(func(st StorageBoxType) bool { return st.ID == id }) {
		id++
	}
	obj, st, err := ownBoxObject(c, s, id)
	if err != nil {
		return StorageBoxType{}, err
	}
	if err := writeOwn(file, append(raw, obj)); err != nil {
		return StorageBoxType{}, err
	}
	return st, nil
}

// UpdateOwnStorageBoxType replaces the own type named s.Name, checked as
// by AddOwnStorageBoxType; ID and position stay. The error wraps
// fs.ErrNotExist when file has no own type of that name.
func UpdateOwnStorageBoxType(c *Catalog, file string, s OwnStorageBoxTypeSpec) (StorageBoxType, error) {
	raw, own, err := readOwnBoxRaw(file)
	if err != nil {
		return StorageBoxType{}, fmt.Errorf("read %s: %w", file, err)
	}
	i := slices.IndexFunc(own, func(st StorageBoxType) bool { return st.Name == s.Name })
	if s.Name == "" || i < 0 {
		return StorageBoxType{}, fmt.Errorf("own storage box type %q: %w", s.Name, fs.ErrNotExist)
	}
	obj, st, err := ownBoxObject(c, s, own[i].ID)
	if err != nil {
		return StorageBoxType{}, err
	}
	raw[i] = obj
	if err := writeOwn(file, raw); err != nil {
		return StorageBoxType{}, err
	}
	return st, nil
}

// RemoveOwnStorageBoxType removes the own type name from file in place;
// the error wraps fs.ErrNotExist when there is none.
func RemoveOwnStorageBoxType(file, name string) error {
	raw, own, err := readOwnBoxRaw(file)
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}
	i := slices.IndexFunc(own, func(st StorageBoxType) bool { return st.Name == name })
	if i < 0 {
		return fmt.Errorf("own storage box type %q: %w", name, fs.ErrNotExist)
	}
	return writeOwn(file, slices.Delete(raw, i, i+1))
}

// OwnStorageBoxTypes returns the entries of file as specs (for the edit
// form), also those not served; a missing file has none.
func OwnStorageBoxTypes(file string) ([]OwnStorageBoxTypeSpec, error) {
	_, own, err := readOwnBoxRaw(file)
	if err != nil {
		return nil, err
	}
	specs := make([]OwnStorageBoxTypeSpec, 0, len(own))
	for _, st := range own {
		var obj struct {
			Description string `json:"description"`
		}
		if err := json.Unmarshal(st.Raw, &obj); err != nil {
			return nil, err
		}
		specs = append(specs, OwnStorageBoxTypeSpec{
			Name:                   st.Name,
			Description:            obj.Description,
			Size:                   st.Size,
			SnapshotLimit:          intOrZero(st.SnapshotLimit),
			AutomaticSnapshotLimit: intOrZero(st.AutomaticSnapshotLimit),
			SubaccountsLimit:       st.SubaccountsLimit,
			Locations:              st.Locations,
		})
	}
	return specs, nil
}

func intOrZero(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// ownBoxObject checks the values of s other than its name and builds the
// Hetzner storage_box_type object with ID id.
func ownBoxObject(c *Catalog, s OwnStorageBoxTypeSpec, id int64) (json.RawMessage, StorageBoxType, error) {
	switch {
	case s.Size < 1:
		return nil, StorageBoxType{}, &FieldError{"size", "must be at least 1"}
	case s.SnapshotLimit < 0:
		return nil, StorageBoxType{}, &FieldError{"snapshot_limit", "must not be negative"}
	case s.AutomaticSnapshotLimit < 0:
		return nil, StorageBoxType{}, &FieldError{"automatic_snapshot_limit", "must not be negative"}
	case s.SubaccountsLimit < 0:
		return nil, StorageBoxType{}, &FieldError{"subaccounts_limit", "must not be negative"}
	case len(s.Locations) == 0:
		return nil, StorageBoxType{}, &FieldError{"locations", "at least one location is required"}
	}
	zero := map[string]string{"net": "0.0000", "gross": "0.0000"}
	prices := []map[string]any{}
	for _, name := range s.Locations {
		if _, ok := c.Location(name); !ok {
			return nil, StorageBoxType{}, &FieldError{"locations", fmt.Sprintf("unknown location %q", name)}
		}
		prices = append(prices, map[string]any{
			"location":      name,
			"price_hourly":  zero,
			"price_monthly": zero,
			"setup_fee":     zero,
		})
	}
	obj, err := json.Marshal(map[string]any{
		"id":                       id,
		"name":                     s.Name,
		"description":              s.Description,
		"snapshot_limit":           s.SnapshotLimit,
		"automatic_snapshot_limit": s.AutomaticSnapshotLimit,
		"subaccounts_limit":        s.SubaccountsLimit,
		"size":                     s.Size,
		"prices":                   prices,
		"deprecation":              nil,
	})
	if err != nil {
		return nil, StorageBoxType{}, err
	}
	sts, err := parseStorageBoxTypeObjects([]json.RawMessage{obj})
	if err != nil {
		return nil, StorageBoxType{}, err
	}
	return obj, sts[0], nil
}

// readOwnBoxRaw reads the objects of file and their decoded types; a
// missing file has none.
func readOwnBoxRaw(file string) ([]json.RawMessage, []StorageBoxType, error) {
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
	own, err := parseStorageBoxTypeObjects(raw)
	if err != nil {
		return nil, nil, err
	}
	return raw, own, nil
}
