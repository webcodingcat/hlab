package images_test

import (
	"net/http"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/resources/images"
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

func TestImages(t *testing.T) {
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
				t.Errorf("unexpected system image %q", img.Name)
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
		if len(list.Images) != len(want) {
			t.Errorf("GET /images?type=system returned %d images, want %d", len(list.Images), len(want))
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
}
