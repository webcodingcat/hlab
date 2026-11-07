package cloud_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"
)

// Two ed25519 keys and their MD5 fingerprints, as `ssh-keygen -l -E md5`
// shows them.
const (
	adminKey         = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDL6XL6m8gJr0QxtReL9rS3hC5leQMngP9+/HtxBjN9J admin@example"
	adminFingerprint = "3e:4e:ae:60:ee:78:80:f1:0a:91:8f:6e:c2:61:96:6c"
	otherKey         = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAICru5wsKpwp6x2GSqUfPmrIQFbrtjtE3bGughaqw0loX"
	otherFingerprint = "78:20:b8:11:0d:df:63:f4:54:1d:f6:01:d3:e4:6d:60"
)

// createSSHKey creates an SSH key through the API and fails the test if it
// cannot.
func createSSHKey(t *testing.T, test *testLab, body map[string]any) schema.SSHKey {
	t.Helper()
	response := test.client.Do(http.MethodPost, "/v1/ssh_keys", body)
	if response.Status != http.StatusCreated {
		t.Fatalf("POST /v1/ssh_keys answered %d %s", response.Status, response.Body)
	}
	var created schema.SSHKeyCreateResponse
	response.Decode(t, &created)
	return created.SSHKey
}

func TestSSHKeyLifeCycle(t *testing.T) {
	test := newTestLab(t)
	key := createSSHKey(t, test, map[string]any{"name": "admin", "public_key": adminKey + "\n", "labels": map[string]string{"env": "dev"}})
	if key.ID < 1_000_000 || key.Name != "admin" || key.Fingerprint != adminFingerprint ||
		key.PublicKey != adminKey || key.Labels["env"] != "dev" || key.Created.IsZero() {
		t.Errorf("created %+v", key)
	}

	path := fmt.Sprintf("/v1/ssh_keys/%d", key.ID)
	var got schema.SSHKeyGetResponse
	test.client.Get(path).Decode(t, &got)
	if got.SSHKey.ID != key.ID || got.SSHKey.Fingerprint != adminFingerprint {
		t.Errorf("GET answered %+v", got.SSHKey)
	}

	// a PUT changes only what it names
	var updated schema.SSHKeyUpdateResponse
	test.client.Do(http.MethodPut, path, map[string]any{"name": "root"}).Decode(t, &updated)
	if updated.SSHKey.Name != "root" || updated.SSHKey.Labels["env"] != "dev" {
		t.Errorf("renamed to %+v", updated.SSHKey)
	}
	var relabeled schema.SSHKeyUpdateResponse
	test.client.Do(http.MethodPut, path, map[string]any{"labels": map[string]string{}}).Decode(t, &relabeled)
	if relabeled.SSHKey.Name != "root" || len(relabeled.SSHKey.Labels) != 0 {
		t.Errorf("labels changed to %+v", relabeled.SSHKey)
	}

	if response := test.client.Do(http.MethodDelete, path, nil); response.Status != http.StatusNoContent || len(response.Body) != 0 {
		t.Errorf("DELETE answered %d %s", response.Status, response.Body)
	}
	if response := test.client.Get(path); response.Status != http.StatusNotFound || errorOf(t, response).Code != "not_found" {
		t.Errorf("GET after DELETE answered %d %s", response.Status, response.Body)
	}
}

func TestSSHKeysOfOtherProjectsAreNotFound(t *testing.T) {
	test := newTestLab(t)
	key := createSSHKey(t, test, map[string]any{"name": "admin", "public_key": adminKey})
	test.client.Token = newToken(t, test.lab, "other")
	path := fmt.Sprintf("/v1/ssh_keys/%d", key.ID)
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		if response := test.client.Do(method, path, map[string]any{}); response.Status != http.StatusNotFound {
			t.Errorf("%s answered %d %s", method, response.Status, response.Body)
		}
	}
	var list schema.SSHKeyListResponse
	test.client.Get("/v1/ssh_keys").Decode(t, &list)
	if len(list.SSHKeys) != 0 {
		t.Errorf("the other project lists %+v", list.SSHKeys)
	}
	// names and fingerprints are unique per project only
	createSSHKey(t, test, map[string]any{"name": "admin", "public_key": adminKey})
}

func TestSSHKeyFilters(t *testing.T) {
	test := newTestLab(t)
	createSSHKey(t, test, map[string]any{"name": "admin", "public_key": adminKey, "labels": map[string]string{"env": "dev"}})
	createSSHKey(t, test, map[string]any{"name": "backup", "public_key": otherKey, "labels": map[string]string{"env": "prod"}})
	for query, want := range map[string][]string{
		"":                                 {"admin", "backup"},
		"?name=backup":                     {"backup"},
		"?fingerprint=" + adminFingerprint: {"admin"},
		"?label_selector=env%3Dprod":       {"backup"},
		"?label_selector=env":              {"admin", "backup"},
		"?sort=name:desc":                  {"backup", "admin"},
		"?name=nobody":                     {},
	} {
		var list schema.SSHKeyListResponse
		response := test.client.Get("/v1/ssh_keys" + query)
		response.Decode(t, &list)
		var names []string
		for _, key := range list.SSHKeys {
			names = append(names, key.Name)
		}
		if response.Status != http.StatusOK || fmt.Sprint(names) != fmt.Sprint(want) {
			t.Errorf("GET /v1/ssh_keys%s listed %v, want %v", query, names, want)
		}
	}
	if response := test.client.Get("/v1/ssh_keys?sort=fingerprint"); response.Status != http.StatusUnprocessableEntity {
		t.Errorf("an unknown sort value answered %d", response.Status)
	}
}

// Hetzner's API: a name or fingerprint the project has already is a
// uniqueness_error naming the field; the fingerprint's message is the error
// example of Hetzner's documentation.
func TestSSHKeyNamesAndFingerprintsAreUnique(t *testing.T) {
	test := newTestLab(t)
	createSSHKey(t, test, map[string]any{"name": "admin", "public_key": adminKey})
	backup := createSSHKey(t, test, map[string]any{"name": "backup", "public_key": otherKey})
	for name, request := range map[string]struct {
		method, path string
		body         map[string]any
		message      string
		field        string
	}{
		"the same name":        {http.MethodPost, "/v1/ssh_keys", map[string]any{"name": "admin", "public_key": otherKey}, "SSH key with the same name already exists", "name"},
		"the same fingerprint": {http.MethodPost, "/v1/ssh_keys", map[string]any{"name": "other", "public_key": adminKey + " another comment"}, "SSH key with the same fingerprint already exists", "public_key"},
		"a rename to a name":   {http.MethodPut, fmt.Sprintf("/v1/ssh_keys/%d", backup.ID), map[string]any{"name": "admin"}, "SSH key with the same name already exists", "name"},
	} {
		response := test.client.Do(request.method, request.path, request.body)
		got := errorOf(t, response)
		if response.Status != http.StatusConflict || got.Code != "uniqueness_error" || got.Message != request.message ||
			string(got.Details) != `{"fields":[{"name":"`+request.field+`"}]}` {
			t.Errorf("%s: answered %d %s", name, response.Status, response.Body)
		}
	}
}

// Hetzner's API: invalid fields are invalid_input, the message names the
// first field and its first message, the details list every field, as the
// error example of Hetzner's documentation shows.
func TestSSHKeyRequestsAreChecked(t *testing.T) {
	test := newTestLab(t)
	for name, request := range map[string]struct {
		body    any
		message string
		details string
	}{
		"an empty body": {map[string]any{}, "invalid input in field 'name': is required",
			`{"fields":[{"name":"name","messages":["is required"]},{"name":"public_key","messages":["is required"]}]}`},
		"a broken key": {map[string]any{"name": "admin", "public_key": "ssh-ed25519 AAAA"}, "invalid input in field 'public_key': invalid public key",
			`{"fields":[{"name":"public_key","messages":["invalid public key"]}]}`},
		"leading whitespace": {map[string]any{"name": "admin", "public_key": " " + adminKey}, "invalid input in field 'public_key': invalid public key",
			`{"fields":[{"name":"public_key","messages":["invalid public key"]}]}`},
		"a number for a name": {map[string]any{"name": 5, "public_key": adminKey}, "invalid input in field 'name': must be of type string",
			`{"fields":[{"name":"name","messages":["must be of type string"]}]}`},
		"an invalid label": {map[string]any{"name": "admin", "public_key": adminKey, "labels": map[string]string{"bad key": "x"}}, "invalid input in field 'labels': label key 'bad key' is invalid",
			`{"fields":[{"name":"labels","messages":["label key 'bad key' is invalid"]}]}`},
	} {
		response := test.client.Do(http.MethodPost, "/v1/ssh_keys", request.body)
		got := errorOf(t, response)
		if response.Status != http.StatusUnprocessableEntity || got.Code != "invalid_input" || got.Message != request.message || string(got.Details) != request.details {
			t.Errorf("%s: answered %d %s", name, response.Status, response.Body)
		}
	}
}
