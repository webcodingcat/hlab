package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// StorageBoxType is one Storage Box type of storage-box-types.json.
type StorageBoxType struct {
	ID                     int64
	Name                   string
	Size                   int64 // bytes
	SnapshotLimit          *int
	AutomaticSnapshotLimit *int
	SubaccountsLimit       int
	// Locations are the location names with a price.
	Locations []string
	// Raw is the type object as served.
	Raw json.RawMessage `json:"-"`
}

// ParseStorageBoxTypes decodes a storage-box-types.json document
// ({"storage_box_types":[...]}).
func ParseStorageBoxTypes(data []byte) ([]StorageBoxType, error) {
	var doc struct {
		StorageBoxTypes *[]json.RawMessage `json:"storage_box_types"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.StorageBoxTypes == nil {
		return nil, errors.New(`no "storage_box_types" list`)
	}
	return parseStorageBoxTypeObjects(*doc.StorageBoxTypes)
}

// parseStorageBoxTypeObjects decodes storage_box_type objects.
func parseStorageBoxTypeObjects(raws []json.RawMessage) ([]StorageBoxType, error) {
	out := make([]StorageBoxType, 0, len(raws))
	for _, raw := range raws {
		var item struct {
			ID                     int64  `json:"id"`
			Name                   string `json:"name"`
			Size                   int64  `json:"size"`
			SnapshotLimit          *int   `json:"snapshot_limit"`
			AutomaticSnapshotLimit *int   `json:"automatic_snapshot_limit"`
			SubaccountsLimit       int    `json:"subaccounts_limit"`
			Prices                 []struct {
				Location string `json:"location"`
			} `json:"prices"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		st := StorageBoxType{
			ID:                     item.ID,
			Name:                   item.Name,
			Size:                   item.Size,
			SnapshotLimit:          item.SnapshotLimit,
			AutomaticSnapshotLimit: item.AutomaticSnapshotLimit,
			SubaccountsLimit:       item.SubaccountsLimit,
			Raw:                    raw,
		}
		for _, p := range item.Prices {
			st.Locations = append(st.Locations, p.Location)
		}
		out = append(out, st)
	}
	return out, nil
}

// loadStorageBoxTypes reads storage-box-types.json of dir; a missing file
// gives no types.
func loadStorageBoxTypes(dir string) ([]StorageBoxType, error) {
	data, err := os.ReadFile(filepath.Join(dir, "storage-box-types.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read catalog: %w", err)
	}
	types, err := ParseStorageBoxTypes(data)
	if err != nil {
		return nil, fmt.Errorf("storage-box-types.json: %w", err)
	}
	return types, nil
}
