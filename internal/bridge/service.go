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
	"path/filepath"
	"strings"

	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/mgmtclient"
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

// RemoveServerType removes the own server type name from the custom file
// and has a running cloud re-read it.
func (s *Service) RemoveServerType(name string) error {
	if err := catalog.RemoveOwn(s.customFile(), name); err != nil {
		return err
	}
	return s.reload()
}

// catalog loads the catalog the way hlabd does.
func (s *Service) catalog() (*catalog.Catalog, []string, error) {
	return catalog.LoadAll(s.d.Paths.Catalog, s.d.Paths.Config, s.customFile())
}

// reload asks a running cloud to re-read the settings file and the own
// server types; a stopped cloud reads them at its next start.
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
