package images_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/images"
	"github.com/webcodingcat/hlab/internal/store"
)

type image struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Status       string `json:"status"`
	OSFlavor     string `json:"os_flavor"`
	OSVersion    string `json:"os_version"`
	Architecture string `json:"architecture"`
}

// projectImage is the part of a project image tests check.
type projectImage struct {
	ID          int64             `json:"id"`
	Type        string            `json:"type"`
	Description string            `json:"description"`
	BoundTo     *int64            `json:"bound_to"`
	Labels      map[string]string `json:"labels"`
}

// putImage stores a project image record of type typ (bound to boundTo
// when not nil) with its disk file in dataFolder and returns its ID and
// the file's path.
func putImage(t *testing.T, env *apitest.Env, dataFolder, typ string, boundTo *int64) (int64, string) {
	t.Helper()
	var id int64
	var path string
	err := env.Store.Tx(func(tx store.Tx) error {
		var err error
		id, err = store.NextID(tx, images.Kind)
		if err != nil {
			return err
		}
		path = filepath.Join(dataFolder, "images", fmt.Sprintf("image-%d.vhdx", id))
		doc := map[string]any{
			"id":           id,
			"type":         typ,
			"status":       "available",
			"name":         nil,
			"description":  typ + " of srv",
			"image_size":   1.5,
			"disk_size":    40,
			"created":      "2026-11-18T10:00:00Z",
			"created_from": map[string]any{"id": 4711, "name": "srv"},
			"bound_to":     boundTo,
			"os_flavor":    "ubuntu",
			"os_version":   "24.04",
			"rapid_deploy": false,
			"protection":   map[string]any{"delete": false},
			"deprecation":  nil,
			"deleted":      nil,
			"labels":       map[string]string{"kind": typ},
			"architecture": "x86",
			"_hlab":        map[string]any{"project_id": env.Project.ID, "path": path},
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		var r images.Record
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		return images.Put(tx, r)
	})
	if err != nil {
		t.Fatalf("store %s image: %v", typ, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	return id, path
}

func listImages(t *testing.T, env *apitest.Env, query string) map[int64]projectImage {
	t.Helper()
	resp := env.Do(http.MethodGet, "/images"+query, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /images%s: status %d, want 200", query, resp.StatusCode)
	}
	var list struct {
		Images []projectImage `json:"images"`
	}
	apitest.Decode(t, resp, &list)
	out := map[int64]projectImage{}
	for _, img := range list.Images {
		out[img.ID] = img
	}
	return out
}

func putUpdate(t *testing.T, env *apitest.Env, id int64, body map[string]any) projectImage {
	t.Helper()
	resp := env.Do(http.MethodPut, fmt.Sprintf("/images/%d", id), body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /images/%d %v: status %d, want 200", id, body, resp.StatusCode)
	}
	var one struct {
		Image projectImage `json:"image"`
	}
	apitest.Decode(t, resp, &one)
	return one.Image
}

func changeProtection(t *testing.T, env *apitest.Env, id int64, del bool) {
	t.Helper()
	resp := env.Do(http.MethodPost, fmt.Sprintf("/images/%d/actions/change_protection", id), map[string]any{"delete": del})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("change_protection delete=%v on image %d: status %d, want 201", del, id, resp.StatusCode)
	}
	var body struct {
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &body)
	if a := env.WaitAction(body.Action.ID); a.Status != "success" {
		t.Fatalf("change_protection delete=%v on image %d: action %q %+v, want success", del, id, a.Status, a.Error)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestImages(t *testing.T) {
	t.Run("SRV-12", func(t *testing.T) {
		cat, err := catalog.Load("../../../catalog")
		if err != nil {
			t.Fatalf("load shipped catalog: %v", err)
		}
		dir := t.TempDir()
		env := apitest.New(t, images.NewModule(images.Deps{Catalog: cat, DataFolder: dir}), resactions.New())

		var server int64 = 4711
		snap, snapPath := putImage(t, env, dir, "snapshot", nil)
		backup, backupPath := putImage(t, env, dir, "backup", &server)

		all := listImages(t, env, "")
		if _, ok := all[snap]; !ok {
			t.Errorf("GET /images lacks snapshot %d", snap)
		}
		system := 0
		for _, img := range all {
			if img.Type == "system" {
				system++
			}
		}
		if system == 0 {
			t.Errorf("GET /images lists no system image beside the project images")
		}
		if got := listImages(t, env, "?type=snapshot"); len(got) != 1 || got[snap].Type != "snapshot" {
			t.Errorf("GET /images?type=snapshot = %v, want exactly snapshot %d", got, snap)
		}

		img := putUpdate(t, env, snap, map[string]any{"description": "changed", "labels": map[string]string{"env": "test"}})
		if img.Description != "changed" || !reflect.DeepEqual(img.Labels, map[string]string{"env": "test"}) {
			t.Errorf("PUT /images/%d = %+v, want description changed and labels {env: test}", snap, img)
		}
		img = putUpdate(t, env, backup, map[string]any{"type": "snapshot"})
		if img.Type != "snapshot" || img.BoundTo != nil {
			t.Errorf("PUT /images/%d type=snapshot = %+v, want type snapshot and bound_to null", backup, img)
		}
		if got := listImages(t, env, "")[backup]; got.Type != "snapshot" || got.BoundTo != nil {
			t.Errorf("GET /images after PUT shows %+v for %d, want snapshot with bound_to null", got, backup)
		}

		path := fmt.Sprintf("/images/%d", snap)
		resp := env.Do(http.MethodDelete, path, nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("DELETE %s: status %d, want 204", path, resp.StatusCode)
		}
		if resp := env.Do(http.MethodGet, path, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s after delete: status %d, want 404", path, resp.StatusCode)
		}
		if exists(snapPath) {
			t.Errorf("disk file %s still exists after delete", snapPath)
		}

		path = fmt.Sprintf("/images/%d", backup)
		changeProtection(t, env, backup, true)
		resp = env.Do(http.MethodDelete, path, nil)
		if resp.StatusCode != http.StatusLocked {
			t.Errorf("DELETE %s with delete protection: status %d, want 423", path, resp.StatusCode)
		}
		if code := apitest.ErrorCode(t, resp); code != "protected" {
			t.Errorf("DELETE %s with delete protection: error.code %q, want protected", path, code)
		}
		if resp := env.Do(http.MethodGet, path, nil); resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s after refused delete: status %d, want 200", path, resp.StatusCode)
		}
		if !exists(backupPath) {
			t.Errorf("disk file %s removed by a refused delete", backupPath)
		}
		changeProtection(t, env, backup, false)
		if resp := env.Do(http.MethodDelete, path, nil); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("DELETE %s after removing protection: status %d, want 204", path, resp.StatusCode)
		}
		if exists(backupPath) {
			t.Errorf("disk file %s still exists after delete", backupPath)
		}
	})

	t.Run("SRV-12/recover", func(t *testing.T) {
		cat, err := catalog.Load("../../../catalog")
		if err != nil {
			t.Fatalf("load shipped catalog: %v", err)
		}
		dir := t.TempDir()
		env := apitest.New(t, images.NewModule(images.Deps{Catalog: cat, DataFolder: dir}), resactions.New())

		kept, keptPath := putImage(t, env, dir, "snapshot", nil)
		cut, cutPath := putImage(t, env, dir, "snapshot", nil)
		err = env.Store.Tx(func(tx store.Tx) error {
			r, err := images.Load(tx, env.Project.ID, cut)
			if err != nil {
				return err
			}
			r.Status = images.StatusCreating
			return images.Put(tx, r)
		})
		if err != nil {
			t.Fatalf("mark image %d creating: %v", cut, err)
		}

		mod := images.NewModule(images.Deps{Catalog: cat, DataFolder: dir, Store: env.Store})
		if err := mod.Recover(context.Background()); err != nil {
			t.Fatalf("Recover: %v", err)
		}

		path := fmt.Sprintf("/images/%d", cut)
		if resp := env.Do(http.MethodGet, path, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s of the image left creating after Recover: status %d, want 404", path, resp.StatusCode)
		}
		if exists(cutPath) {
			t.Errorf("file %s of the image left creating still exists after Recover", cutPath)
		}
		path = fmt.Sprintf("/images/%d", kept)
		if resp := env.Do(http.MethodGet, path, nil); resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s of the available image after Recover: status %d, want 200", path, resp.StatusCode)
		}
		if !exists(keptPath) {
			t.Errorf("file %s of the available image removed by Recover", keptPath)
		}
	})

	t.Run("CAT-06", func(t *testing.T) {
		cat, err := catalog.Load("../../../catalog")
		if err != nil {
			t.Fatalf("load shipped catalog: %v", err)
		}
		env := apitest.New(t, images.New(cat))

		want := map[string][2]string{ // name: os_flavor, os_version
			"ubuntu-22.04": {"ubuntu", "22.04"},
			"ubuntu-24.04": {"ubuntu", "24.04"},
			"ubuntu-26.04": {"ubuntu", "26.04"},
			"debian-12":    {"debian", "12"},
			"debian-13":    {"debian", "13"},
		}
		resp := env.Do(http.MethodGet, "/images?type=system", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /images?type=system: status %d, want 200", resp.StatusCode)
		}
		var list struct {
			Images []image `json:"images"`
		}
		apitest.Decode(t, resp, &list)
		seen := map[string]bool{}
		for _, img := range list.Images {
			w, ok := want[img.Name]
			if !ok {
				continue
			}
			seen[img.Name] = true
			if img.OSFlavor != w[0] || img.OSVersion != w[1] || img.Architecture != "x86" ||
				img.Type != "system" || img.Status != "available" {
				t.Errorf("%s: os_flavor %q os_version %q architecture %q type %q status %q, want %q %q x86 system available",
					img.Name, img.OSFlavor, img.OSVersion, img.Architecture, img.Type, img.Status, w[0], w[1])
			}
		}
		for name := range want {
			if !seen[name] {
				t.Errorf("system image %s missing", name)
			}
		}

		resp = env.Do(http.MethodGet, "/images?name=ubuntu-24.04", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /images?name=ubuntu-24.04: status %d, want 200", resp.StatusCode)
		}
		var byName struct {
			Images []image `json:"images"`
		}
		apitest.Decode(t, resp, &byName)
		if len(byName.Images) != 1 || byName.Images[0].Name != "ubuntu-24.04" {
			t.Errorf("GET /images?name=ubuntu-24.04 = %+v, want exactly ubuntu-24.04", byName.Images)
		}
	})

	t.Run("CAT-09", func(t *testing.T) {
		cat, err := catalog.Load("../../../catalog")
		if err != nil {
			t.Fatalf("load shipped catalog: %v", err)
		}
		env := apitest.New(t, images.New(cat))

		// list returns the images of GET /images<query> by name, as raw
		// fields.
		list := func(query string) map[string]map[string]json.RawMessage {
			t.Helper()
			resp := env.Do(http.MethodGet, "/images"+query, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /images%s: status %d, want 200", query, resp.StatusCode)
			}
			var body struct {
				Images []map[string]json.RawMessage `json:"images"`
			}
			apitest.Decode(t, resp, &body)
			out := map[string]map[string]json.RawMessage{}
			for _, img := range body.Images {
				var name string
				if err := json.Unmarshal(img["name"], &name); err != nil {
					t.Fatalf("GET /images%s: image name %s: %v", query, img["name"], err)
				}
				out[name] = img
			}
			return out
		}
		all := list("?type=system&include_deprecated=true")
		plain := list("?type=system")

		const fedora43Deprecation = `{"announced":"2026-11-12T00:00:00Z","unavailable_after":"2027-02-09T00:00:00Z"}`
		cases := []struct {
			name, description, flavor, version, created string
			id                                          int64
			deprecation                                 string // JSON; "null" when none
		}{
			{"rocky-8", "Rocky Linux 8", "rocky", "8", "2021-08-19T06:30:23Z", 45780948, "null"},
			{"rocky-9", "Rocky Linux 9", "rocky", "9", "2022-07-20T13:55:52Z", 76766499, "null"},
			{"rocky-10", "Rocky Linux 10", "rocky", "10", "2025-06-17T13:03:39Z", 245249370, "null"},
			{"alma-8", "AlmaLinux 8", "alma", "8", "2023-03-27T11:32:49Z", 104788983, "null"},
			{"alma-9", "AlmaLinux 9", "alma", "9", "2023-03-27T11:33:04Z", 104788996, "null"},
			{"alma-10", "AlmaLinux 10", "alma", "10", "2025-06-17T13:06:30Z", 245249013, "null"},
			{"centos-stream-9", "CentOS Stream 9", "centos", "Stream 9", "2022-01-27T07:52:03Z", 59752342, "null"},
			{"centos-stream-10", "CentOS Stream 10", "centos", "Stream 10", "2025-01-08T09:54:17Z", 210163277, "null"},
			{"fedora-43", "Fedora 43", "fedora", "43", "2025-12-18T13:14:36Z", 342035601, fedora43Deprecation},
			{"fedora-44", "Fedora 44", "fedora", "44", "2026-05-18T10:57:10Z", 387893188, "null"},
			{"opensuse-16", "openSUSE Leap 16", "opensuse", "16", "2025-12-19T09:04:13Z", 342262088, "null"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				img, ok := all[c.name]
				if !ok {
					t.Fatalf("GET /images?type=system&include_deprecated=true lacks %s", c.name)
				}
				want := map[string]any{
					"id": c.id, "name": c.name, "description": c.description,
					"os_flavor": c.flavor, "os_version": c.version, "disk_size": 5,
					"rapid_deploy": true, "created": c.created, "type": "system",
					"status": "available", "architecture": "x86",
				}
				for field, w := range want {
					wantJSON, err := json.Marshal(w)
					if err != nil {
						t.Fatal(err)
					}
					if !jsonEqual(t, img[field], wantJSON) {
						t.Errorf("%s: %s = %s, want %s", c.name, field, img[field], wantJSON)
					}
				}
				if raw, ok := img["deprecated"]; ok {
					t.Errorf("%s has the field deprecated (%s), want none", c.name, raw)
				}
				if raw, ok := img["deprecation"]; !ok || !jsonEqual(t, raw, []byte(c.deprecation)) {
					t.Errorf("%s: deprecation = %s, want %s", c.name, raw, c.deprecation)
				}
				_, listed := plain[c.name]
				if wantListed := c.name != "fedora-43"; listed != wantListed {
					t.Errorf("GET /images?type=system lists %s: %v, want %v", c.name, listed, wantListed)
				}
			})
		}

		if _, ok := all["debian-11"]; ok {
			t.Errorf("GET /images?type=system&include_deprecated=true lists debian-11")
		}
		if _, ok := plain["debian-11"]; ok {
			t.Errorf("GET /images?type=system lists debian-11")
		}
	})
}

// jsonEqual reports whether two JSON documents hold the same value; an
// absent value (nil) equals nothing.
func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	if a == nil || b == nil {
		return false
	}
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("decode %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return reflect.DeepEqual(va, vb)
}
