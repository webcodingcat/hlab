package catalog

import (
	"fmt"
	"log/slog"
	"slices"
	"strconv"
)

// OwnStorageBoxTypesFile is the file of the own storage box types in the
// config folder.
const OwnStorageBoxTypesFile = "storage-box-types.custom.json"

// StorageBoxTypes returns the catalog storage box types followed by the
// own types.
func (c *Catalog) StorageBoxTypes() []StorageBoxType {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append(slices.Clone(c.storageBoxTypes), c.ownStorageBoxTypes...)
}

// SetStorageBoxTypes replaces the catalog storage box types, for example
// with recorded ones; the own types stay.
func (c *Catalog) SetStorageBoxTypes(types []StorageBoxType) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.storageBoxTypes = slices.Clone(types)
}

// StorageBoxTypeByRef returns the catalog or own storage box type whose
// name or ID is ref.
func (c *Catalog) StorageBoxTypeByRef(ref string) (StorageBoxType, bool) {
	for _, st := range c.StorageBoxTypes() {
		if st.Name == ref || strconv.FormatInt(st.ID, 10) == ref {
			return st, true
		}
	}
	return StorageBoxType{}, false
}

// ReloadOwnStorageBoxTypes reads file (missing: none) and replaces the
// served own storage box types. An invalid file, an ID below MinOwnID, a
// type without name, or an ID or name taken by a catalog type or another
// own type serves none and OwnStorageBoxTypesWarning reports why; a valid
// file clears the warning.
func (c *Catalog) ReloadOwnStorageBoxTypes(file string) {
	own, err := readOwnStorageBoxTypes(file)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		err = storageBoxTypeConflicts(c.storageBoxTypes, own)
	}
	if err != nil {
		slog.Warn("own storage box types are ignored", "file", file, "err", err)
		c.ownStorageBoxTypes = nil
		c.ownBoxTypesWarning = fmt.Sprintf("own storage box types in %s are ignored: %v", file, err)
		return
	}
	c.ownStorageBoxTypes = own
	c.ownBoxTypesWarning = ""
}

// OwnStorageBoxTypesWarning says why the own storage box types are left
// out, or is "" while they are served.
func (c *Catalog) OwnStorageBoxTypesWarning() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ownBoxTypesWarning
}

// readOwnStorageBoxTypes reads the JSON array of storage_box_type objects
// of file; a missing file has none.
func readOwnStorageBoxTypes(file string) ([]StorageBoxType, error) {
	_, own, err := readOwnBoxRaw(file)
	if err != nil {
		return nil, err
	}
	for _, st := range own {
		if st.ID < MinOwnID {
			return nil, fmt.Errorf("storage box type %q has ID %d, own IDs start at %d", st.Name, st.ID, MinOwnID)
		}
		if st.Name == "" {
			return nil, fmt.Errorf("storage box type %d has no name", st.ID)
		}
	}
	return own, nil
}

// storageBoxTypeConflicts reports the first own type whose name or ID is
// taken by a catalog type or by another own type.
func storageBoxTypeConflicts(types, own []StorageBoxType) error {
	names := map[string]bool{}
	ids := map[int64]bool{}
	for _, st := range types {
		names[st.Name] = true
		ids[st.ID] = true
	}
	for _, st := range own {
		if names[st.Name] {
			return fmt.Errorf("storage box type name %q is taken", st.Name)
		}
		if ids[st.ID] {
			return fmt.Errorf("storage box type ID %d is taken", st.ID)
		}
		names[st.Name] = true
		ids[st.ID] = true
	}
	return nil
}
