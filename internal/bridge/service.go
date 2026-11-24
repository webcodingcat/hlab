// Package bridge holds the services the tray binds into its window: the
// management calls, the settings and own server types, the cloud status
// and the log stream. It does not import Wails; events go out through
// callbacks.
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/mgmtclient"
	"github.com/webcodingcat/hlab/internal/projectname"
	"github.com/webcodingcat/hlab/internal/settings"
	"github.com/webcodingcat/hlab/internal/sysint"
)

// Response is the answer of the management API to Call.
type Response struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

// Invalid names the field of a refused value and why it was refused.
type Invalid struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Deps are the parts a Service uses.
type Deps struct {
	Client  *mgmtclient.Client
	Paths   sysint.Paths
	Running func(ctx context.Context) bool // SCM state is running
}

// Service forwards management calls and edits the settings file and the
// own server types.
type Service struct {
	d Deps
}

// NewService returns a Service on d.
func NewService(d Deps) *Service {
	return &Service{d: d}
}

func (s *Service) settingsPath() string { return filepath.Join(s.d.Paths.Config, "settings.toml") }

func (s *Service) customFile() string {
	return filepath.Join(s.d.Paths.Config, "server-types.custom.json")
}

// Call sends method and path below /mgmt/v1 with body ("" sends none) and
// returns the status and body of the answer. A body that is not JSON comes
// back as a JSON string; an empty one as null.
func (s *Service) Call(method, path, body string) (Response, error) {
	var b []byte
	if body != "" {
		b = []byte(body)
	}
	status, resp, err := s.d.Client.Do(context.Background(), method, path, b)
	if err != nil {
		return Response{}, err
	}
	r := Response{Status: status}
	switch {
	case len(resp) == 0:
	case json.Valid(resp):
		r.Body = resp
	default:
		if r.Body, err = json.Marshal(string(resp)); err != nil {
			return Response{}, err
		}
	}
	return r, nil
}

// Settings returns the settings of the settings file.
func (s *Service) Settings() (settings.Settings, error) {
	return settings.Load(s.settingsPath())
}

// SaveSettings writes set to the settings file and has a running cloud
// re-read it. An invalid value returns its settings key and writes
// nothing.
func (s *Service) SaveSettings(set settings.Settings) (*Invalid, error) {
	if err := settings.Write(s.settingsPath(), set); err != nil {
		if fe, ok := errors.AsType[*settings.FieldError](err); ok {
			return &Invalid{Field: fe.Key, Message: fe.Message}, nil
		}
		return nil, err
	}
	return nil, s.reload()
}

// ListedType is a server type with where it comes from.
type ListedType struct {
	Type   json.RawMessage `json:"type"`   // Hetzner server_type object
	Source string          `json:"source"` // "catalog" | "own"
}

// ServerTypeList is the catalog and own server types and why own types
// are left out.
type ServerTypeList struct {
	Types   []ListedType `json:"types"`
	Problem string       `json:"problem,omitempty"` // like custom_server_types
}

// ServerTypes lists the server types as hlabd merges them from the
// shipped catalog, the updated catalog and the custom file.
func (s *Service) ServerTypes() (ServerTypeList, error) {
	cat, warns, err := s.catalog()
	if err != nil {
		return ServerTypeList{}, err
	}
	own := map[int64]bool{}
	for _, st := range cat.Own() {
		own[st.ID] = true
	}
	list := ServerTypeList{Types: []ListedType{}, Problem: strings.Join(warns, "; ")}
	for _, st := range cat.Types() {
		src := "catalog"
		if own[st.ID] {
			src = "own"
		}
		list.Types = append(list.Types, ListedType{Type: st.Raw, Source: src})
	}
	return list, nil
}

// AddServerType adds an own server type to the custom file and has a
// running cloud re-read it. An invalid spec returns its field.
func (s *Service) AddServerType(spec catalog.OwnSpec) (*Invalid, error) {
	cat, _, err := s.catalog()
	if err != nil {
		return nil, err
	}
	if _, err := catalog.AddOwn(cat, s.customFile(), spec); err != nil {
		if fe, ok := errors.AsType[*catalog.FieldError](err); ok {
			return &Invalid{Field: fe.Field, Message: fe.Message}, nil
		}
		return nil, err
	}
	return nil, s.reload()
}

// UpdateServerType replaces the own server type spec.Name in the custom
// file and has a running cloud re-read it. An invalid spec returns its
// field.
func (s *Service) UpdateServerType(spec catalog.OwnSpec) (*Invalid, error) {
	cat, _, err := s.catalog()
	if err != nil {
		return nil, err
	}
	if _, err := catalog.UpdateOwn(cat, s.customFile(), spec); err != nil {
		if fe, ok := errors.AsType[*catalog.FieldError](err); ok {
			return &Invalid{Field: fe.Field, Message: fe.Message}, nil
		}
		return nil, err
	}
	return nil, s.reload()
}

// RemoveServerType removes the own server type name from the custom file
// and has a running cloud re-read it.
func (s *Service) RemoveServerType(name string) error {
	if err := catalog.RemoveOwn(s.customFile(), name); err != nil {
		return err
	}
	return s.reload()
}

// ListedISO is an ISO with where it comes from; an own ISO also carries
// its spec for the edit form.
type ListedISO struct {
	ISO    json.RawMessage     `json:"iso"`    // Hetzner iso object
	Source string              `json:"source"` // "catalog" | "own"
	Own    *catalog.OwnISOSpec `json:"own,omitempty"`
}

// ISOList is the catalog and own ISOs and why own ISOs are left out.
type ISOList struct {
	ISOs    []ListedISO `json:"isos"`
	Problem string      `json:"problem,omitempty"` // like custom_isos
}

// ISOs lists the catalog ISOs followed by every entry of the own ISOs
// file, also those left out of serving, so they can be edited or removed;
// Problem says why own ISOs are left out. A file that cannot be read lists
// no own ISOs. It reads the files, so it works while the cloud is stopped.
func (s *Service) ISOs() (ISOList, error) {
	cat, warns, err := s.isoCatalog()
	if err != nil {
		return ISOList{}, err
	}
	list := ISOList{ISOs: []ListedISO{}, Problem: strings.Join(warns, "; ")}
	for _, iso := range cat.ISOs {
		list.ISOs = append(list.ISOs, ListedISO{ISO: iso.Raw, Source: "catalog"})
	}
	own, err := catalog.ReadOwnISOs(s.isoFile())
	if err != nil {
		return list, nil // the problem names the file
	}
	for _, iso := range own {
		var obj struct {
			Description string `json:"description"`
		}
		if err := json.Unmarshal(iso.Raw, &obj); err != nil {
			return ISOList{}, fmt.Errorf("own ISO %d: %w", iso.ID, err)
		}
		list.ISOs = append(list.ISOs, ListedISO{ISO: iso.Raw, Source: "own",
			Own: &catalog.OwnISOSpec{ID: iso.ID, Description: obj.Description,
				Architecture: iso.Architecture, Source: iso.OwnSource}})
	}
	return list, nil
}

// AddISO adds an own ISO to the own ISOs file and has a running cloud
// re-read it. An invalid spec returns its field.
func (s *Service) AddISO(spec catalog.OwnISOSpec) (*Invalid, error) {
	cat, _, err := s.isoCatalog()
	if err != nil {
		return nil, err
	}
	_, err = catalog.AddOwnISO(cat, s.isoFile(), spec)
	return s.ownResult(err)
}

// UpdateISO replaces the own ISO spec.ID in the own ISOs file and has a
// running cloud re-read it. An invalid spec returns its field.
func (s *Service) UpdateISO(spec catalog.OwnISOSpec) (*Invalid, error) {
	cat, _, err := s.isoCatalog()
	if err != nil {
		return nil, err
	}
	_, err = catalog.UpdateOwnISO(cat, s.isoFile(), spec)
	return s.ownResult(err)
}

// RemoveISO removes the own ISO id from the own ISOs file and has a
// running cloud re-read it.
func (s *Service) RemoveISO(id int64) error {
	if err := catalog.RemoveOwnISO(s.isoFile(), id); err != nil {
		return err
	}
	return s.reload()
}

func (s *Service) isoFile() string { return filepath.Join(s.d.Paths.Config, catalog.OwnISOsFile) }

// isoCatalog loads the catalog with the own ISOs of the own ISOs file and
// returns why own ISOs are left out.
func (s *Service) isoCatalog() (*catalog.Catalog, []string, error) {
	cat, _, err := s.catalog()
	if err != nil {
		return nil, nil, err
	}
	return cat, cat.ReloadOwnISOs(s.isoFile()), nil
}

// ownResult turns the error of a change of an own ISOs or storage box
// types file into a refused field, or reloads a running cloud after it.
func (s *Service) ownResult(err error) (*Invalid, error) {
	if err != nil {
		if fe, ok := errors.AsType[*catalog.FieldError](err); ok {
			return &Invalid{Field: fe.Field, Message: fe.Message}, nil
		}
		return nil, err
	}
	return nil, s.reload()
}

// ListedStorageBoxType is a storage box type with where it comes from; an
// own type also carries its spec for the edit form.
type ListedStorageBoxType struct {
	Type   json.RawMessage                `json:"type"`   // Hetzner storage_box_type object
	Source string                         `json:"source"` // "catalog" | "own"
	Own    *catalog.OwnStorageBoxTypeSpec `json:"own,omitempty"`
}

// StorageBoxTypeList is the catalog and own types and why own types are
// left out.
type StorageBoxTypeList struct {
	Types   []ListedStorageBoxType `json:"types"`
	Problem string                 `json:"problem,omitempty"` // like custom_storage_box_types
}

// StorageBoxTypes lists the catalog storage box types followed by every
// entry of the own storage box types file, also those left out of
// serving, so they can be edited or removed; Problem says why own types
// are left out. A file that cannot be read lists no own types. It reads
// the files, so it works while the cloud is stopped.
func (s *Service) StorageBoxTypes() (StorageBoxTypeList, error) {
	cat, _, err := s.catalog()
	if err != nil {
		return StorageBoxTypeList{}, err
	}
	list := StorageBoxTypeList{Types: []ListedStorageBoxType{}}
	for _, st := range cat.StorageBoxTypes() {
		list.Types = append(list.Types, ListedStorageBoxType{Type: st.Raw, Source: "catalog"})
	}
	file := s.boxTypesFile()
	cat.ReloadOwnStorageBoxTypes(file)
	list.Problem = cat.OwnStorageBoxTypesWarning()
	specs, err := catalog.OwnStorageBoxTypes(file)
	if err != nil || len(specs) == 0 {
		return list, nil // the problem names the file
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return list, nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || len(raw) != len(specs) {
		return list, nil
	}
	for i := range specs {
		list.Types = append(list.Types, ListedStorageBoxType{Type: raw[i], Source: "own", Own: &specs[i]})
	}
	return list, nil
}

// AddStorageBoxType adds an own storage box type to the own storage box
// types file and has a running cloud re-read it. An invalid spec returns
// its field.
func (s *Service) AddStorageBoxType(spec catalog.OwnStorageBoxTypeSpec) (*Invalid, error) {
	cat, err := s.boxTypesCatalog()
	if err != nil {
		return nil, err
	}
	_, err = catalog.AddOwnStorageBoxType(cat, s.boxTypesFile(), spec)
	return s.ownResult(err)
}

// UpdateStorageBoxType replaces the own storage box type spec.Name in the
// own storage box types file and has a running cloud re-read it. An
// invalid spec returns its field.
func (s *Service) UpdateStorageBoxType(spec catalog.OwnStorageBoxTypeSpec) (*Invalid, error) {
	cat, err := s.boxTypesCatalog()
	if err != nil {
		return nil, err
	}
	_, err = catalog.UpdateOwnStorageBoxType(cat, s.boxTypesFile(), spec)
	return s.ownResult(err)
}

// RemoveStorageBoxType removes the own storage box type name from the own
// storage box types file and has a running cloud re-read it.
func (s *Service) RemoveStorageBoxType(name string) error {
	if err := catalog.RemoveOwnStorageBoxType(s.boxTypesFile(), name); err != nil {
		return err
	}
	return s.reload()
}

func (s *Service) boxTypesFile() string {
	return filepath.Join(s.d.Paths.Config, catalog.OwnStorageBoxTypesFile)
}

// boxTypesCatalog loads the catalog with the own storage box types it
// serves, as hlabd does.
func (s *Service) boxTypesCatalog() (*catalog.Catalog, error) {
	cat, _, err := s.catalog()
	if err != nil {
		return nil, err
	}
	cat.ReloadOwnStorageBoxTypes(s.boxTypesFile())
	return cat, nil
}

// Location is one catalog location as the views show it.
type Location struct {
	Name string `json:"name"`
	City string `json:"city"`
}

// ErrorCodes is api.Codes() for the fault form's error code select; no
// cloud needed.
func (s *Service) ErrorCodes() []api.CodeStatus {
	return api.Codes()
}

// Locations lists the catalog's locations sorted by name. It reads the
// catalog files, so it works while the cloud is stopped.
func (s *Service) Locations() ([]Location, error) {
	cat, _, err := s.catalog()
	if err != nil {
		return nil, err
	}
	locs := make([]Location, 0, len(cat.Locations))
	for _, l := range cat.Locations {
		locs = append(locs, Location{Name: l.Name, City: l.City})
	}
	slices.SortFunc(locs, func(a, b Location) int { return strings.Compare(a.Name, b.Name) })
	return locs, nil
}

// ProjectName returns the project name derived from a display name, for
// the live preview in the create dialog. It needs no cloud.
func (s *Service) ProjectName(display string) string {
	return projectname.Derive(display)
}

// catalog loads the catalog the way hlabd does.
func (s *Service) catalog() (*catalog.Catalog, []string, error) {
	return catalog.LoadAll(s.d.Paths.Catalog, s.d.Paths.Config, s.customFile())
}

// reload asks a running cloud to re-read the settings file, the own
// server types and the own ISOs; a stopped cloud reads them at its next start.
func (s *Service) reload() error {
	ctx := context.Background()
	if !s.d.Running(ctx) {
		return nil
	}
	status, body, err := s.d.Client.Do(ctx, "POST", "/settings/reload", nil)
	if err != nil {
		return fmt.Errorf("reload the settings: %w", err)
	}
	if status >= 300 {
		return fmt.Errorf("reload the settings: %w", mgmtclient.AnswerError(status, body))
	}
	return nil
}
