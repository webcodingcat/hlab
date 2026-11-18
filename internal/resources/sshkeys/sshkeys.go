// Package sshkeys serves Hetzner's SSH keys of a project.
package sshkeys

import (
	"crypto/md5" //nolint:gosec // Hetzner's fingerprint is the MD5 of the key
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/store"
)

// Kind is the store kind of SSH keys.
const Kind = "ssh_keys"

// Key is an SSH key in Hetzner's API shape.
type Key struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Fingerprint string            `json:"fingerprint"`
	PublicKey   string            `json:"public_key"`
	Labels      map[string]string `json:"labels"`
	Created     time.Time         `json:"created"`
}

// Module is the ssh_keys resource type.
type Module struct{}

// New returns the module.
func New() *Module { return &Module{} }

// Type implements resource.Module.
func (m *Module) Type() string { return "ssh_keys" }

// Routes implements resource.Module.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /ssh_keys", http.HandlerFunc(m.list))
	r.Handle("POST /ssh_keys", http.HandlerFunc(m.create))
	r.Handle("GET /ssh_keys/{id}", http.HandlerFunc(m.get))
	r.Handle("DELETE /ssh_keys/{id}", http.HandlerFunc(m.delete))
}

// Find returns the key of projectID whose ID or name is ref, or
// store.ErrNotFound.
func Find(tx store.Tx, projectID int64, ref string) (Key, error) {
	keys, err := store.List[Key](tx, Kind, projectID)
	if err != nil {
		return Key{}, err
	}
	for _, k := range keys {
		if k.Name == ref {
			return k, nil
		}
	}
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		for _, k := range keys {
			if k.ID == id {
				return k, nil
			}
		}
	}
	return Key{}, store.ErrNotFound
}

func (m *Module) list(w http.ResponseWriter, req *http.Request) {
	p, _ := api.ProjectFrom(req.Context())
	q := req.URL.Query()
	var keys []Key
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		keys, err = store.List[Key](tx, Kind, p.ID)
		return err
	})
	if err != nil {
		api.ServerError(w, err)
		return
	}
	var out []Key
	for _, k := range keys {
		if (len(q["name"]) == 0 || slices.Contains(q["name"], k.Name)) &&
			(len(q["fingerprint"]) == 0 || slices.Contains(q["fingerprint"], k.Fingerprint)) {
			out = append(out, k)
		}
	}
	api.List(w, "ssh_keys", out)
}

func (m *Module) get(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	var k Key
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		k, err = store.Get[Key](tx, Kind, p.ID, id)
		return err
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		api.NotFound(w)
	case err != nil:
		api.ServerError(w, err)
	default:
		api.JSON(w, http.StatusOK, map[string]any{"ssh_key": k})
	}
}

func (m *Module) create(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Name      string            `json:"name"`
		PublicKey string            `json:"public_key"`
		Labels    map[string]string `json:"labels"`
	}
	if !api.Decode(w, req, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		api.InvalidInput(w, "name", "name is required")
		return
	}
	fp, err := Fingerprint(body.PublicKey)
	if err != nil {
		api.InvalidInput(w, "public_key", err.Error())
		return
	}
	if body.Labels == nil {
		body.Labels = map[string]string{}
	}
	p, _ := api.ProjectFrom(req.Context())
	k := Key{
		Name:        body.Name,
		Fingerprint: fp,
		PublicKey:   body.PublicKey,
		Labels:      body.Labels,
		Created:     time.Now().UTC().Truncate(time.Second),
	}
	err = api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		var err error
		if k.ID, err = store.NextID(tx, Kind); err != nil {
			return err
		}
		return store.Put(tx, store.Meta{Kind: Kind, ID: k.ID, ProjectID: p.ID, Name: k.Name, Labels: k.Labels, Created: k.Created}, k)
	})
	if err != nil {
		api.ServerError(w, err)
		return
	}
	api.JSON(w, http.StatusCreated, map[string]any{"ssh_key": k})
}

func (m *Module) delete(w http.ResponseWriter, req *http.Request) {
	id, ok := api.PathID(w, req)
	if !ok {
		return
	}
	p, _ := api.ProjectFrom(req.Context())
	err := api.StoreFrom(req.Context()).Tx(func(tx store.Tx) error {
		return store.Delete(tx, Kind, p.ID, id)
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		api.NotFound(w)
	case err != nil:
		api.ServerError(w, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// Fingerprint returns the MD5 fingerprint of an OpenSSH public key line
// ("<type> <base64> [comment]") as colon-separated hex, like Hetzner.
func Fingerprint(publicKey string) (string, error) {
	fields := strings.Fields(publicKey)
	if len(fields) < 2 {
		return "", errors.New("invalid public key")
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil || len(blob) < 4 {
		return "", errors.New("invalid public key")
	}
	n := binary.BigEndian.Uint32(blob)
	if uint64(n) > uint64(len(blob)-4) || string(blob[4:4+n]) != fields[0] {
		return "", errors.New("invalid public key")
	}
	sum := md5.Sum(blob) //nolint:gosec // see import
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02x", b)
	}
	return strings.Join(parts, ":"), nil
}
