package sshkeys_test

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/resources/sshkeys"
)

// Public key fixtures with the fingerprints `ssh-keygen -l -E md5 -f`
// printed for them (without the "MD5:" prefix).
var fixtures = []struct {
	name, publicKey, fingerprint string
}{
	{
		"ed25519",
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA42AvjmOSeFhkGqi7LL8SMihzTLFoX9Kd9sDKhkOtDv fixture-ed25519",
		"f9:78:70:8a:38:ca:7b:c5:6e:f4:6f:8b:09:bd:4c:31",
	},
	{
		"rsa",
		"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCkCpjd6R5GGuiTcX02kQFQSTxU2Bll9Se4BY0bRZzwLbdXFkPjpX4TjooyrtTEFl/ZEvTtTK0eAZI9/OVGMeqQsqLPOi4emWZrgJudKamFcbx8mDavX6XWhKQJAgUhZTg/9eFOcHcLidmfFvdqdD61052b1+YKCZ7QBoPK64oIe278At07JLPhcGebUGbmbywhxCCJBCatuuSEGnrgJ/aYsdQzuE+HoxkYgLjDYn7DUqZnBgLMCAcW/4dYR3zpfRaYtt7PiRShUgaxigLpz7yWsIa/MtRKVKHjNQ3rlNiC7tgxUG6/ZwRWQYKqVgUy1WSFkDoXv0vfhG/mY5HdWp6p fixture-rsa",
		"5b:27:35:75:09:20:1d:50:70:cc:1e:0e:4d:79:af:e6",
	},
	{
		"ecdsa",
		"ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBB0bUyFZZnLeVcIUfiOGIoi4DyF0gWh1CmmYBj4j/P2v4YCqkqE12xdaHKNVLYo2Fr83xVZaUZHwq3EdSKPh13s= fixture-ecdsa",
		"03:92:4a:87:32:56:4e:1c:b2:a6:c3:8e:83:10:70:d2",
	},
}

type key struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Fingerprint string            `json:"fingerprint"`
	PublicKey   string            `json:"public_key"`
	Labels      map[string]string `json:"labels"`
}

func TestSSHKeys(t *testing.T) {
	t.Run("KEY-01", func(t *testing.T) {
		env := apitest.New(t, sshkeys.New())

		ids := map[string]int64{}
		for _, f := range fixtures {
			resp := env.Do(http.MethodPost, "/ssh_keys", map[string]any{
				"name":       f.name,
				"public_key": f.publicKey,
				"labels":     map[string]string{"kind": f.name},
			})
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("POST /ssh_keys %s: status %d, want 201", f.name, resp.StatusCode)
			}
			var body struct {
				SSHKey key `json:"ssh_key"`
			}
			apitest.Decode(t, resp, &body)
			if body.SSHKey.Fingerprint != f.fingerprint {
				t.Errorf("%s: fingerprint %q, want %q (ssh-keygen -l -E md5)", f.name, body.SSHKey.Fingerprint, f.fingerprint)
			}
			ids[f.name] = body.SSHKey.ID
		}

		var one struct {
			SSHKey key `json:"ssh_key"`
		}
		resp := env.Do(http.MethodGet, fmt.Sprintf("/ssh_keys/%d", ids["rsa"]), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /ssh_keys/%d: status %d, want 200", ids["rsa"], resp.StatusCode)
		}
		apitest.Decode(t, resp, &one)
		if one.SSHKey.Name != "rsa" || one.SSHKey.PublicKey != fixtures[1].publicKey || one.SSHKey.Labels["kind"] != "rsa" {
			t.Errorf("GET /ssh_keys/%d = %+v, want the rsa fixture", ids["rsa"], one.SSHKey)
		}

		if got := listNames(t, env); !reflect.DeepEqual(got, map[string]bool{"ed25519": true, "rsa": true, "ecdsa": true}) {
			t.Errorf("GET /ssh_keys names = %v, want the three fixtures", got)
		}

		path := fmt.Sprintf("/ssh_keys/%d", ids["ed25519"])
		resp = env.Do(http.MethodPut, path, map[string]any{"name": "renamed", "labels": map[string]string{"env": "test"}})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT %s: status %d, want 200", path, resp.StatusCode)
		}
		one.SSHKey = key{} // json merges into a non-nil labels map
		apitest.Decode(t, resp, &one)
		if one.SSHKey.Name != "renamed" || !reflect.DeepEqual(one.SSHKey.Labels, map[string]string{"env": "test"}) {
			t.Errorf("PUT %s answered %+v, want name renamed, labels {env: test}", path, one.SSHKey)
		}
		resp = env.Do(http.MethodGet, path, nil)
		one.SSHKey = key{}
		apitest.Decode(t, resp, &one)
		if one.SSHKey.Name != "renamed" || !reflect.DeepEqual(one.SSHKey.Labels, map[string]string{"env": "test"}) || one.SSHKey.Fingerprint != fixtures[0].fingerprint {
			t.Errorf("GET %s after PUT = %+v, want name renamed, labels {env: test}, fingerprint unchanged", path, one.SSHKey)
		}

		for _, again := range []struct{ name, publicKey string }{
			{"other-name", fixtures[2].publicKey},
			{"other-comment", fieldsWithComment(fixtures[2].publicKey, "another comment")},
		} {
			resp := env.Do(http.MethodPost, "/ssh_keys", map[string]any{"name": again.name, "public_key": again.publicKey})
			if resp.StatusCode != http.StatusConflict {
				t.Errorf("POST /ssh_keys same key as %s: status %d, want 409", again.name, resp.StatusCode)
			}
			code, fields := errorFields(t, resp)
			if code != "uniqueness_error" || !fields["public_key"] {
				t.Errorf("POST /ssh_keys same key as %s: error %q fields %v, want uniqueness_error on public_key", again.name, code, fields)
			}
		}

		resp = env.Do(http.MethodPost, "/ssh_keys", map[string]any{"name": "bad", "public_key": "ssh-ed25519 not-base64!"})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("POST /ssh_keys invalid key: status %d, want 422", resp.StatusCode)
		}
		if code := apitest.ErrorCode(t, resp); code != "invalid_input" {
			t.Errorf("POST /ssh_keys invalid key: error.code %q, want invalid_input", code)
		}

		resp = env.Do(http.MethodDelete, path, nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("DELETE %s: status %d, want 204", path, resp.StatusCode)
		}
		if resp := env.Do(http.MethodGet, path, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s after delete: status %d, want 404", path, resp.StatusCode)
		}
		if got := listNames(t, env); !reflect.DeepEqual(got, map[string]bool{"rsa": true, "ecdsa": true}) {
			t.Errorf("GET /ssh_keys names after delete = %v, want rsa and ecdsa", got)
		}
	})
}

func listNames(t *testing.T, env *apitest.Env) map[string]bool {
	t.Helper()
	resp := env.Do(http.MethodGet, "/ssh_keys", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ssh_keys: status %d, want 200", resp.StatusCode)
	}
	var list struct {
		SSHKeys []key `json:"ssh_keys"`
	}
	apitest.Decode(t, resp, &list)
	names := map[string]bool{}
	for _, k := range list.SSHKeys {
		names[k.Name] = true
	}
	return names
}

// fieldsWithComment replaces the comment of an OpenSSH public key line.
func fieldsWithComment(publicKey, comment string) string {
	var typ, blob string
	_, _ = fmt.Sscanf(publicKey, "%s %s", &typ, &blob)
	return typ + " " + blob + " " + comment
}

// errorFields returns error.code and the names in error.details.fields.
func errorFields(t *testing.T, resp *http.Response) (string, map[string]bool) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Fields []struct {
					Name string `json:"name"`
				} `json:"fields"`
			} `json:"details"`
		} `json:"error"`
	}
	apitest.Decode(t, resp, &body)
	fields := map[string]bool{}
	for _, f := range body.Error.Details.Fields {
		fields[f.Name] = true
	}
	return body.Error.Code, fields
}
