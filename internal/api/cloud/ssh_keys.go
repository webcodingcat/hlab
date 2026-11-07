package cloud

import (
	"bytes"
	"cmp"
	"crypto/md5" // Hetzner's fingerprint of an SSH key is its MD5
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/web"
)

// Hetzner's answers to an SSH key whose name or fingerprint the project has
// already; the second is the example of Hetzner's documentation.
const (
	sshKeyNameTaken        = "SSH key with the same name already exists"
	sshKeyFingerprintTaken = "SSH key with the same fingerprint already exists"
)

// sshKeySortKeys are the sort values of GET /ssh_keys.
var sshKeySortKeys = web.SortKeys[*state.SSHKey]{
	"id":   func(a, b *state.SSHKey) int { return cmp.Compare(a.ID, b.ID) },
	"name": func(a, b *state.SSHKey) int { return cmp.Compare(a.Name, b.Name) },
}

// registerSSHKeys adds the routes of the SSH keys.
func registerSSHKeys(api *web.API, l *lab.Lab) {
	api.Handle("GET /ssh_keys", func(req *web.Request) (int, any, error) {
		query := req.URL.Query()
		selector, err := web.ParseSelector(query.Get("label_selector"))
		if err != nil {
			return 0, nil, err
		}
		var keys []*state.SSHKey
		_ = l.View(func(tx *state.Tx) error {
			for _, key := range state.All(tx, state.SSHKeys) {
				if key.ProjectID == req.Caller.ProjectID && selector.Matches(key.Labels) &&
					(!query.Has("name") || key.Name == query.Get("name")) &&
					(!query.Has("fingerprint") || key.Fingerprint == query.Get("fingerprint")) {
					keys = append(keys, key)
				}
			}
			return nil
		})
		if err := web.Sort(req, keys, sshKeySortKeys); err != nil {
			return 0, nil, err
		}
		var out []schema.SSHKey
		for _, key := range keys {
			out = append(out, sshKeyJSON(key))
		}
		return listJSON(req, "ssh_keys", out)
	})

	api.Handle("POST /ssh_keys", func(req *web.Request) (int, any, error) {
		var body schema.SSHKeyCreateRequest
		if err := web.DecodeJSON(req, &body); err != nil {
			return 0, nil, err
		}
		var check web.Check
		check.Required("name", body.Name != "")
		check.Required("public_key", body.PublicKey != "")
		fingerprint, keyErr := sshFingerprint(body.PublicKey)
		if body.PublicKey != "" && keyErr != nil {
			check.Fail("public_key", "invalid public key")
		}
		if body.Labels != nil {
			check.Labels(*body.Labels)
		}
		if err := check.Err(); err != nil {
			return 0, nil, err
		}
		key := &state.SSHKey{
			ProjectID:   req.Caller.ProjectID,
			Name:        body.Name,
			Labels:      map[string]string{},
			Created:     l.Now(),
			Fingerprint: fingerprint,
			PublicKey:   strings.TrimRight(body.PublicKey, " \t\r\n"),
		}
		if body.Labels != nil {
			key.Labels = *body.Labels
		}
		err := l.Change(func(tx *state.Tx) error {
			for _, other := range state.All(tx, state.SSHKeys) {
				switch {
				case other.ProjectID != key.ProjectID:
				case other.Name == key.Name:
					return web.Uniqueness(sshKeyNameTaken, "name")
				case other.Fingerprint == key.Fingerprint:
					return web.Uniqueness(sshKeyFingerprintTaken, "public_key")
				}
			}
			key.ID = state.NewID(tx, state.SSHKeys)
			state.Put(tx, state.SSHKeys, key)
			return nil
		})
		if err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, schema.SSHKeyCreateResponse{SSHKey: sshKeyJSON(key)}, nil
	})

	api.Handle("GET /ssh_keys/{id}", func(req *web.Request) (int, any, error) {
		var key *state.SSHKey
		err := l.View(func(tx *state.Tx) error {
			var err error
			key, err = callerSSHKey(tx, req)
			return err
		})
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, schema.SSHKeyGetResponse{SSHKey: sshKeyJSON(key)}, nil
	})

	api.Handle("PUT /ssh_keys/{id}", func(req *web.Request) (int, any, error) {
		var body schema.SSHKeyUpdateRequest
		if err := web.DecodeJSON(req, &body); err != nil {
			return 0, nil, err
		}
		if body.Labels != nil {
			var check web.Check
			check.Labels(*body.Labels)
			if err := check.Err(); err != nil {
				return 0, nil, err
			}
		}
		var updated *state.SSHKey
		err := l.Change(func(tx *state.Tx) error {
			key, err := callerSSHKey(tx, req)
			if err != nil {
				return err
			}
			if body.Name != "" && body.Name != key.Name {
				for _, other := range state.All(tx, state.SSHKeys) {
					if other.ProjectID == key.ProjectID && other.Name == body.Name {
						return web.Uniqueness(sshKeyNameTaken, "name")
					}
				}
				key.Name = body.Name
			}
			if body.Labels != nil {
				key.Labels = *body.Labels
			}
			state.Put(tx, state.SSHKeys, key)
			updated = key
			return nil
		})
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, schema.SSHKeyUpdateResponse{SSHKey: sshKeyJSON(updated)}, nil
	})

	api.Handle("DELETE /ssh_keys/{id}", func(req *web.Request) (int, any, error) {
		err := l.Change(func(tx *state.Tx) error {
			key, err := callerSSHKey(tx, req)
			if err != nil {
				return err
			}
			state.Delete(tx, state.SSHKeys, key.ID)
			return nil
		})
		return http.StatusNoContent, nil, err
	})
}

// callerSSHKey returns the SSH key of the path's ID if it belongs to the
// caller's project; another project's key is not found, as on Hetzner.
func callerSSHKey(tx *state.Tx, req *web.Request) (*state.SSHKey, error) {
	key, err := state.Get(tx, state.SSHKeys, web.PathID(req, "id"))
	if errors.Is(err, state.ErrNotFound) || (err == nil && key.ProjectID != req.Caller.ProjectID) {
		return nil, web.NotFound("SSH key")
	}
	return key, err
}

// sshKeyJSON returns an SSH key as the API shows it.
func sshKeyJSON(key *state.SSHKey) schema.SSHKey {
	labels := key.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	return schema.SSHKey{
		ID:          key.ID,
		Name:        key.Name,
		Fingerprint: key.Fingerprint,
		PublicKey:   key.PublicKey,
		Labels:      labels,
		Created:     key.Created,
	}
}

// sshFingerprint returns the MD5 fingerprint of an OpenSSH public key
// ("ssh-ed25519 AAAA… comment") as Hetzner shows it: hex pairs joined by
// colons. A key is invalid when its blob is not base64 or does not start
// with the key's type, or when it has leading whitespace, which Hetzner
// refuses.
func sshFingerprint(publicKey string) (string, error) {
	parts := strings.Fields(publicKey)
	if len(parts) < 2 || publicKey != strings.TrimLeft(publicKey, " \t\r\n") {
		return "", errors.New("invalid public key")
	}
	blob, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil || len(blob) < 4 {
		return "", errors.New("invalid public key")
	}
	length := binary.BigEndian.Uint32(blob)
	if uint64(len(blob)) < 4+uint64(length) || !bytes.Equal(blob[4:4+length], []byte(parts[0])) {
		return "", errors.New("invalid public key")
	}
	digest := md5.Sum(blob)
	pairs := make([]string, len(digest))
	for i, b := range digest {
		pairs[i] = fmt.Sprintf("%02x", b)
	}
	return strings.Join(pairs, ":"), nil
}
