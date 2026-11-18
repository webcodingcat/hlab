package servers_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/resources/servers"
)

// testISOs holds the rows of iso_test.go; TestServers runs them.
func testISOs(t *testing.T) {
	t.Run("SRV-15", func(t *testing.T) {
		cat, err := catalog.Load("../../../catalog")
		if err != nil {
			t.Fatalf("load shipped catalog: %v", err)
		}
		var isos []catalog.ISO
		for _, iso := range cat.ISOs {
			if !iso.Deprecated {
				isos = append(isos, iso)
			}
		}
		if len(isos) < 2 {
			t.Fatalf("shipped catalog has %d usable ISOs, want at least 2", len(isos))
		}
		byName, byID := isos[0], isos[1]

		p := newRecPlatform()
		imgs := newTestImages(t)
		env, _, _ := newImageEnv(t, p, func(d *servers.Deps) { d.Images = imgs })
		id := createdServer(t, env, "iso", nil)

		// attached checks the server after an attach of iso.
		attached := func(t *testing.T, iso catalog.ISO, how string) {
			t.Helper()
			if reqs := imgs.isoRequests(); !slices.Contains(reqs, iso.Name) {
				t.Errorf("attach_iso %s: EnsureISO requests %v, want %q", how, reqs, iso.Name)
			}
			if d := p.lastDVD(t, 1); !sameFile(d.iso, imgs.isoPath(iso.Name)) {
				t.Errorf("attach_iso %s: DVD slot 1 holds %q, want %q", how, d.iso, imgs.isoPath(iso.Name))
			}
			order := lastBootOrder(t, p.Fake)
			want := []platform.BootDevice{{Kind: "dvd", Slot: 1}, {Kind: "disk", Slot: 0}}
			if len(order) < 2 || order[0] != want[0] || order[1] != want[1] {
				t.Errorf("attach_iso %s: boot order %+v, want DVD slot 1 then LUN 0 first", how, order)
			}
			if got := serverISO(t, env, id); got == nil || got.ID != iso.ID || got.Name != iso.Name {
				t.Errorf("attach_iso %s: server iso %+v, want {%d %s}", how, got, iso.ID, iso.Name)
			}
		}

		runAction(t, env, id, "attach_iso", map[string]any{"iso": byName.Name})
		attached(t, byName, "by name")
		runAction(t, env, id, "attach_iso", map[string]any{"iso": fmt.Sprint(byID.ID)})
		attached(t, byID, "by ID replacing the first")

		wantError(t, postAction(env, id, "attach_iso", map[string]any{"iso": "no-such-iso"}),
			http.StatusUnprocessableEntity, "invalid_input", "attach_iso with an unknown ISO")

		runAction(t, env, id, "detach_iso", nil)
		if d := p.lastDVD(t, 1); d.iso != "" {
			t.Errorf("DVD slot 1 after detach_iso holds %q, want ejected", d.iso)
		}
		if first := firstBoot(t, p.Fake); first != (platform.BootDevice{Kind: "disk", Slot: 0}) {
			t.Errorf("first boot device after detach_iso %+v, want disk LUN 0", first)
		}
		if got := serverISO(t, env, id); got != nil {
			t.Errorf("server iso after detach_iso %+v, want null", got)
		}

		imgs.setFailISO(byName.Name)
		inserts := len(p.dvdSets(1))
		act, _ := startAction(t, env, id, "attach_iso", map[string]any{"iso": byName.Name})
		if a := env.WaitAction(act); a.Status != "error" {
			t.Errorf("attach_iso with a failing download: action %q, want error", a.Status)
		}
		for _, d := range p.dvdSets(1)[inserts:] {
			if d.iso != "" {
				t.Errorf("attach_iso with a failing download inserted %q into DVD slot 1", d.iso)
			}
		}
		if got := serverISO(t, env, id); got != nil {
			t.Errorf("server iso after the failed attach_iso %+v, want null", got)
		}
	})
}

type isoRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// serverISO is the server's iso field.
func serverISO(t *testing.T, env *apitest.Env, id int64) *isoRef {
	t.Helper()
	resp := env.Do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /servers/%d: status %d, want 200", id, resp.StatusCode)
	}
	var b struct {
		Server struct {
			ISO *isoRef `json:"iso"`
		} `json:"server"`
	}
	apitest.Decode(t, resp, &b)
	return b.Server.ISO
}
