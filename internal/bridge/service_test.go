package bridge_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/bridge"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/mgmtclient"
	"github.com/webcodingcat/hlab/internal/settings"
	"github.com/webcodingcat/hlab/internal/sysint"
)

// shippedCatalog is the catalog folder shipped with the program.
const shippedCatalog = "../../catalog"

// waitTimeout bounds every wait for a condition.
const waitTimeout = 5 * time.Second

// fakeMgmt is the management API over a local TCP listener. While down,
// dialing fails and open connections are closed without an answer, as
// when hlabd stopped.
type fakeMgmt struct {
	srv  *httptest.Server
	down atomic.Bool
	// failedDials counts the dials refused while down.
	failedDials atomic.Int32

	mu   sync.Mutex
	reqs []string // "METHOD /path" below /mgmt/v1, without the query
}

// newFakeMgmt serves mux, whose patterns carry the /mgmt/v1 prefix, and
// answers POST /mgmt/v1/settings/reload with 200 {} unless mux has it.
func newFakeMgmt(t *testing.T, mux *http.ServeMux) *fakeMgmt {
	t.Helper()
	if mux == nil {
		mux = http.NewServeMux()
	}
	f := &fakeMgmt{}
	inner := http.NewServeMux()
	inner.Handle("/", mux)
	inner.HandleFunc("POST /mgmt/v1/settings/reload", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	})
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.down.Load() {
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
					return
				}
			}
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		f.mu.Lock()
		f.reqs = append(f.reqs, r.Method+" "+strings.TrimPrefix(r.URL.Path, "/mgmt/v1"))
		f.mu.Unlock()
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// client is a management client that dials the fake.
func (f *fakeMgmt) client() *mgmtclient.Client {
	return mgmtclient.New(func(ctx context.Context) (net.Conn, error) {
		if f.down.Load() {
			f.failedDials.Add(1)
			return nil, errors.New("the management pipe does not exist")
		}
		var d net.Dialer
		return d.DialContext(ctx, "tcp", f.srv.Listener.Addr().String())
	})
}

// count is how often req ("METHOD /path") reached the fake.
func (f *fakeMgmt) count(req string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.reqs {
		if r == req {
			n++
		}
	}
	return n
}

// waitFor polls cond until it holds, failing the test after waitTimeout.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not reached within %s", what, waitTimeout)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

const reload = "POST /settings/reload"

// newService returns a Service on a fake management API with the config
// folder dir, the shipped catalog and the SCM state running.
func newService(t *testing.T, dir string, running *atomic.Bool) (*bridge.Service, *fakeMgmt) {
	t.Helper()
	f := newFakeMgmt(t, nil)
	f.down.Store(!running.Load())
	svc := bridge.NewService(bridge.Deps{
		Client:  f.client(),
		Paths:   sysint.Paths{Config: dir, Catalog: shippedCatalog, State: dir, Logs: dir},
		Running: func(context.Context) bool { return running.Load() },
	})
	return svc, f
}

// setRunning switches the SCM state and the fake management API together.
func setRunning(running *atomic.Bool, f *fakeMgmt, on bool) {
	running.Store(on)
	f.down.Store(!on)
}

func TestBridge(t *testing.T) {
	t.Run("UI-03", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "settings.toml")
		original := "[logging]\nlevel = \"info\"\n"
		if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
			t.Fatal(err)
		}
		var running atomic.Bool
		svc, f := newService(t, dir, &running)

		bad := settings.Defaults()
		bad.Network.PublicRange = netip.MustParsePrefix("10.1.0.0/16")
		inv, err := svc.SaveSettings(bad)
		if err != nil {
			t.Fatalf("SaveSettings with an invalid public_range: error %v, want an Invalid answer", err)
		}
		if inv == nil || inv.Field != "network.public_range" || inv.Message == "" {
			t.Errorf("SaveSettings with an invalid public_range = %+v, want the field network.public_range with a message", inv)
		}
		if data, _ := os.ReadFile(path); string(data) != original {
			t.Errorf("settings file after the refused save:\n%s\nwant it unchanged", data)
		}

		set := settings.Defaults()
		set.Cloud.StopBehaviour = "shutdown"
		if inv, err := svc.SaveSettings(set); inv != nil || err != nil {
			t.Fatalf("SaveSettings while stopped = %+v, %v; want nil, nil", inv, err)
		}
		if got, err := settings.Load(path); err != nil || got.Cloud.StopBehaviour != "shutdown" {
			t.Errorf("settings after the save while stopped: stop_behaviour %q (%v), want shutdown", got.Cloud.StopBehaviour, err)
		}

		setRunning(&running, f, true)
		set.Logging.Level = "debug"
		if inv, err := svc.SaveSettings(set); inv != nil || err != nil {
			t.Fatalf("SaveSettings while running = %+v, %v; want nil, nil", inv, err)
		}
		if got, err := settings.Load(path); err != nil || got.Logging.Level != "debug" {
			t.Errorf("settings after the save while running: logging.level %q (%v), want debug", got.Logging.Level, err)
		}
		if n := f.count(reload); n != 1 {
			t.Errorf("%s sent %d times, want once (only for the save while running)", reload, n)
		}
	})

	t.Run("UI-04", func(t *testing.T) {
		dir := t.TempDir()
		custom := filepath.Join(dir, "server-types.custom.json")
		writeOwnTypes(t, custom, "own-a")
		var running atomic.Bool
		svc, f := newService(t, dir, &running)

		list, err := svc.ServerTypes()
		if err != nil {
			t.Fatalf("ServerTypes while stopped: %v", err)
		}
		if list.Problem != "" {
			t.Errorf("ServerTypes problem %q with a valid custom file, want none", list.Problem)
		}
		sources := listedSources(t, list)
		if sources["cx23"] != "catalog" || sources["own-a"] != "own" {
			t.Errorf("ServerTypes while stopped: cx23 %q, own-a %q; want catalog, own", sources["cx23"], sources["own-a"])
		}

		inv, err := svc.AddServerType(catalog.OwnSpec{Name: "own-b", CPUType: "shared", Cores: 0, Memory: 2, Disk: 20})
		if err != nil || inv == nil || inv.Field != "cores" {
			t.Errorf("AddServerType without cores = %+v, %v; want an Invalid for cores", inv, err)
		}

		spec := catalog.OwnSpec{Name: "own-b", CPUType: "shared", Cores: 1, Memory: 2, Disk: 20, Locations: []string{"fsn1"}}
		if inv, err := svc.AddServerType(spec); inv != nil || err != nil {
			t.Fatalf("AddServerType while stopped = %+v, %v; want nil, nil", inv, err)
		}
		list, err = svc.ServerTypes()
		if err != nil {
			t.Fatalf("ServerTypes: %v", err)
		}
		if got := listedSources(t, list)["own-b"]; got != "own" {
			t.Errorf("own-b after AddServerType listed with source %q, want own", got)
		}
		if err := svc.RemoveServerType("own-a"); err != nil {
			t.Fatalf("RemoveServerType while stopped: %v", err)
		}
		if n := f.count(reload); n != 0 {
			t.Errorf("%s sent %d times while stopped, want never", reload, n)
		}

		setRunning(&running, f, true)
		spec.Name = "own-c"
		if inv, err := svc.AddServerType(spec); inv != nil || err != nil {
			t.Fatalf("AddServerType while running = %+v, %v; want nil, nil", inv, err)
		}
		if n := f.count(reload); n != 1 {
			t.Errorf("%s after the add while running sent %d times, want once", reload, n)
		}
		if err := svc.RemoveServerType("own-c"); err != nil {
			t.Fatalf("RemoveServerType while running: %v", err)
		}
		if n := f.count(reload); n != 2 {
			t.Errorf("%s after the remove while running sent %d times in all, want twice", reload, n)
		}

		if err := os.WriteFile(custom, []byte("[{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		list, err = svc.ServerTypes()
		if err != nil {
			t.Fatalf("ServerTypes with an invalid custom file: %v", err)
		}
		if list.Problem == "" {
			t.Error("ServerTypes with an invalid custom file reports no problem")
		}
		sources = listedSources(t, list)
		if sources["cx23"] != "catalog" || sources["own-b"] != "" {
			t.Errorf("ServerTypes with an invalid custom file: cx23 %q, own-b %q; want catalog and no own type", sources["cx23"], sources["own-b"])
		}
	})

	t.Run("UI-01/tray", testTray)
	t.Run("UI-01/control", testControl)
	t.Run("UI-01/banner", testBanner)
	t.Run("UI-06", testLogs)
}

// writeOwnTypes writes a custom file with copies of the shipped cx23 named
// names, with IDs from catalog.MinOwnID.
func writeOwnTypes(t *testing.T, path string, names ...string) {
	t.Helper()
	cat, err := catalog.Load(shippedCatalog)
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	ref, ok := cat.ServerType("cx23")
	if !ok {
		t.Fatal("shipped catalog lacks cx23")
	}
	types := []map[string]any{}
	for i, name := range names {
		var st map[string]any
		if err := json.Unmarshal(ref.Raw, &st); err != nil {
			t.Fatal(err)
		}
		st["id"], st["name"] = catalog.MinOwnID+i, name
		types = append(types, st)
	}
	data, err := json.Marshal(types)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// listedSources maps the names of the listed types to their source.
func listedSources(t *testing.T, list bridge.ServerTypeList) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, lt := range list.Types {
		var st struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(lt.Type, &st); err != nil || st.Name == "" {
			t.Fatalf("listed type %s is not a server_type object: %v", lt.Type, err)
		}
		if _, dup := out[st.Name]; dup {
			t.Errorf("server type %s listed twice", st.Name)
		}
		out[st.Name] = lt.Source
	}
	if !slices.ContainsFunc(list.Types, func(lt bridge.ListedType) bool { return lt.Source == "catalog" }) {
		t.Error("no catalog type listed")
	}
	return out
}
