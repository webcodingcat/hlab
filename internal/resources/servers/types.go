package servers

import (
	"encoding/json"
	"fmt"

	"github.com/webcodingcat/hlab/internal/catalog"
)

// recordType returns the server type of r from the catalog or, when the
// type is gone (a removed own type), the cores, memory and disk of r's
// stored server_type. Create and change_type do not use it: they need the
// catalog.
func (m *Module) recordType(r Record) (catalog.ServerType, error) {
	if typ, ok := m.d.Catalog.ServerType(r.Hlab.ServerType); ok {
		return typ, nil
	}
	var typ catalog.ServerType
	if err := json.Unmarshal(r.ServerType, &typ); err != nil || typ.Cores <= 0 || typ.Memory <= 0 {
		return catalog.ServerType{}, fmt.Errorf("server type %q not in the catalog and not stored with the server", r.Hlab.ServerType)
	}
	return typ, nil
}
