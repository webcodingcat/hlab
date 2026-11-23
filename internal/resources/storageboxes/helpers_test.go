package storageboxes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/nthash"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/storageboxes"
	"github.com/webcodingcat/hlab/internal/scheduler"
	"github.com/webcodingcat/hlab/internal/shacrypt"
	"github.com/webcodingcat/hlab/internal/store"
)

// boxesPath is the collection of storage boxes under the Hetzner prefix.
const boxesPath = "/hetzner/v1/storage_boxes"

// validPassword meets the Storage Box password policy.
const validPassword = "Valid-Passw0rd"

// sshKey is a public key in OpenSSH format.
const sshKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGwlGJHbN/WPoDzAAKGfXG7xfegxs03VqyfwiwMd/K8z test@hlab"

// publicRange is the public range the module hands box IPs out of.
var publicRange = netip.MustParsePrefix("198.19.0.0/16")

// plentyFree is the default free space of the fake host drive.
const plentyFree = uint64(1) << 40

// boxEnv is the storage box module with the recorded storage box types,
// the actions module, an Apply that waits on gate and then fails with
// applyErr, a fake storage agent, a fake host drive and a test clock.
type boxEnv struct {
	*apitest.Env
	cat   *catalog.Catalog
	gate  *apitest.PushGate
	mod   *storageboxes.Module
	agent *fakeAgent
	clock *tickClock
	free  atomic.Uint64

	mu       sync.Mutex
	applyErr error
}

// newBoxEnv mounts the storage box module on the shipped catalog with the
// recorded storage box types (bx11, bx21, bx31; bx11 without nbg1).
func newBoxEnv(t *testing.T) *boxEnv {
	t.Helper()
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	cat.StorageBoxTypes = apitest.StorageBoxTypes(t)
	e := &boxEnv{cat: cat, gate: apitest.NewPushGate(t), agent: &fakeAgent{}, clock: newTickClock()}
	e.free.Store(plentyFree)
	e.mod = storageboxes.New(storageboxes.Deps{
		Catalog:     cat,
		PublicRange: publicRange,
		Apply:       e.apply,
		Job:         e.agent.job,
		FreeBytes:   func() (uint64, error) { return e.free.Load(), nil },
		Clock:       e.clock,
	})
	e.Env = apitest.New(t, e.mod, resactions.New())
	return e
}

// apply waits while the gate is closed, then returns the error set by
// failApply.
func (e *boxEnv) apply(ctx context.Context, _ int64) error {
	if err := e.gate.Wait(ctx); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.applyErr
}

// failApply makes every later Apply fail with err; nil lets it succeed.
func (e *boxEnv) failApply(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.applyErr = err
}

// fakeAgent is the storage agent's job endpoint: usage answers the set
// usage, folders the set answer; it records every job.
type fakeAgent struct {
	mu      sync.Mutex
	calls   []agentapi.StorageJob
	usage   agentapi.Usage
	folders func(agentapi.StorageJob) (agentapi.StorageJobResult, error)
}

func (a *fakeAgent) job(_ context.Context, j agentapi.StorageJob) (agentapi.StorageJobResult, error) {
	a.mu.Lock()
	a.calls = append(a.calls, j)
	usage, folders := a.usage, a.folders
	a.mu.Unlock()
	if j.Kind == agentapi.StorageJobFolders && folders != nil {
		return folders(j)
	}
	return agentapi.StorageJobResult{Usage: usage}, nil
}

// setUsage sets the usage the agent reports for every box.
func (a *fakeAgent) setUsage(u agentapi.Usage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.usage = u
}

// setFolders sets the answer of folders jobs.
func (a *fakeAgent) setFolders(f func(agentapi.StorageJob) (agentapi.StorageJobResult, error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.folders = f
}

// jobsOf returns the recorded jobs of kind.
func (a *fakeAgent) jobsOf(kind string) []agentapi.StorageJob {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []agentapi.StorageJob
	for _, j := range a.calls {
		if j.Kind == kind {
			out = append(out, j)
		}
	}
	return out
}

// tickClock is a scheduler.Clock that only moves when the test ticks it.
type tickClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []tickWaiter
}

type tickWaiter struct {
	d  time.Duration
	at time.Time
	ch chan time.Time
}

var _ scheduler.Clock = (*tickClock)(nil)

func newTickClock() *tickClock {
	return &tickClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *tickClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *tickClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waiters = append(c.waiters, tickWaiter{d: d, at: c.now.Add(d), ch: ch})
	return ch
}

// tick waits up to apitest.ActionTimeout until something waits for d,
// then moves the clock forward by d and fires every wait that is due.
func (c *tickClock) tick(t *testing.T, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(apitest.ActionTimeout)
	for !c.waitsFor(d) {
		if time.Now().After(deadline) {
			t.Fatalf("nothing waits on the clock for %s within %s", d, apitest.ActionTimeout)
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.at.After(c.now) {
			w.ch <- c.now
			continue
		}
		kept = append(kept, w)
	}
	c.waiters = kept
}

func (c *tickClock) waitsFor(d time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.ContainsFunc(c.waiters, func(w tickWaiter) bool { return w.d == d })
}

// storageType returns the recorded type name.
func (e *boxEnv) storageType(t *testing.T, name string) catalog.StorageBoxType {
	t.Helper()
	for _, st := range e.cat.StorageBoxTypes {
		if st.Name == name {
			return st
		}
	}
	t.Fatalf("no recorded storage box type %q", name)
	return catalog.StorageBoxType{}
}

type ref struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type actionView struct {
	ID        int64  `json:"id"`
	Command   string `json:"command"`
	Status    string `json:"status"`
	Resources []ref  `json:"resources"`
}

// hasResource reports whether a references id of type typ.
func (a actionView) hasResource(typ string, id int64) bool {
	return slices.Contains(a.Resources, ref{ID: id, Type: typ})
}

type accessView struct {
	ReachableExternally bool `json:"reachable_externally"`
	SambaEnabled        bool `json:"samba_enabled"`
	SSHEnabled          bool `json:"ssh_enabled"`
	WebDAVEnabled       bool `json:"webdav_enabled"`
	ZFSEnabled          bool `json:"zfs_enabled"`
	Readonly            bool `json:"readonly"`
}

type snapshotPlanView struct {
	MaxSnapshots int  `json:"max_snapshots"`
	Minute       int  `json:"minute"`
	Hour         int  `json:"hour"`
	DayOfWeek    *int `json:"day_of_week"`
	DayOfMonth   *int `json:"day_of_month"`
}

type boxView struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	StorageBoxType struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"storage_box_type"`
	Location struct {
		Name string `json:"name"`
	} `json:"location"`
	AccessSettings accessView        `json:"access_settings"`
	SnapshotPlan   *snapshotPlanView `json:"snapshot_plan"`
	Protection     struct {
		Delete bool `json:"delete"`
	} `json:"protection"`
	Labels   map[string]string `json:"labels"`
	Status   string            `json:"status"`
	Username *string           `json:"username"`
	Server   *string           `json:"server"`
	System   *string           `json:"system"`
	Stats    struct {
		Size          int64 `json:"size"`
		SizeData      int64 `json:"size_data"`
		SizeSnapshots int64 `json:"size_snapshots"`
	} `json:"stats"`
}

type subaccountView struct {
	ID             int64             `json:"id"`
	StorageBox     int64             `json:"storage_box"`
	Name           string            `json:"name"`
	Username       string            `json:"username"`
	Server         string            `json:"server"`
	HomeDirectory  string            `json:"home_directory"`
	Description    string            `json:"description"`
	Labels         map[string]string `json:"labels"`
	AccessSettings accessView        `json:"access_settings"`
}

func boxPath(id int64) string { return fmt.Sprintf("%s/%d", boxesPath, id) }

func subaccountsPath(box int64) string { return boxPath(box) + "/subaccounts" }

func subaccountPath(box, sub int64) string { return fmt.Sprintf("%s/%d", subaccountsPath(box), sub) }

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details struct {
			Fields []struct {
				Name string `json:"name"`
			} `json:"fields"`
			Limits []struct {
				Name string `json:"name"`
			} `json:"limits"`
		} `json:"details"`
	} `json:"error"`
}

// wantError checks status and error.code.
func wantError(t *testing.T, resp *http.Response, status int, code, what string) errorBody {
	t.Helper()
	var b errorBody
	apitest.Decode(t, resp, &b)
	if resp.StatusCode != status || b.Error.Code != code {
		t.Errorf("%s: status %d code %q (%s), want %d %s", what, resp.StatusCode, b.Error.Code, b.Error.Message, status, code)
	}
	return b
}

// wantFieldError checks a 422 invalid_input naming field.
func wantFieldError(t *testing.T, resp *http.Response, field, what string) {
	t.Helper()
	b := wantError(t, resp, http.StatusUnprocessableEntity, "invalid_input", what)
	var names []string
	for _, f := range b.Error.Details.Fields {
		names = append(names, f.Name)
	}
	if !slices.Contains(names, field) {
		t.Errorf("%s: error on fields %v, want %s", what, names, field)
	}
}

// waitSuccess waits for action a and fails unless it succeeds.
func waitSuccess(t *testing.T, e *boxEnv, a actionView, what string) {
	t.Helper()
	if got := e.For(t).WaitAction(a.ID); got.Status != "success" {
		t.Fatalf("%s: action %d %s ended %q %+v, want success", what, a.ID, a.Command, got.Status, got.Error)
	}
}

// boxBody returns a valid create body for a bx11 box in fsn1.
func boxBody(name string) map[string]any {
	return map[string]any{
		"name":             name,
		"location":         "fsn1",
		"storage_box_type": "bx11",
		"password":         validPassword,
	}
}

// postBox posts a box create and returns the response.
func postBox(e *boxEnv, body map[string]any) *http.Response {
	return e.Do(http.MethodPost, boxesPath, body)
}

type createdBox struct {
	Box    boxView    `json:"storage_box"`
	Action actionView `json:"action"`
}

// startBox posts a box create, expects 201 and returns box and action as
// answered, without waiting.
func startBox(t *testing.T, e *boxEnv, body map[string]any) createdBox {
	t.Helper()
	resp := postBox(e, body)
	if resp.StatusCode != http.StatusCreated {
		wantError(t, resp, http.StatusCreated, "", fmt.Sprintf("POST %s %v", boxesPath, body))
		t.FailNow()
	}
	var b createdBox
	apitest.Decode(t, resp, &b)
	if b.Action.Command != "create_storage_box" {
		t.Errorf("create action %+v, want create_storage_box", b.Action)
	}
	return b
}

// createBox creates a box, waits for its create_storage_box action to
// succeed and returns the box as GET shows it then.
func createBox(t *testing.T, e *boxEnv, body map[string]any) boxView {
	t.Helper()
	b := startBox(t, e, body)
	waitSuccess(t, e, b.Action, "create storage box "+b.Box.Name)
	return mustBox(t, e, b.Box.ID)
}

// getBox returns the box id and whether it exists.
func getBox(t *testing.T, e *boxEnv, id int64) (boxView, bool) {
	t.Helper()
	resp := e.Do(http.MethodGet, boxPath(id), nil)
	if resp.StatusCode == http.StatusNotFound {
		wantError(t, resp, http.StatusNotFound, "not_found", "GET "+boxPath(id))
		return boxView{}, false
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", boxPath(id), resp.StatusCode)
	}
	var b struct {
		Box boxView `json:"storage_box"`
	}
	apitest.Decode(t, resp, &b)
	return b.Box, true
}

func mustBox(t *testing.T, e *boxEnv, id int64) boxView {
	t.Helper()
	b, ok := getBox(t, e, id)
	if !ok {
		t.Fatalf("storage box %d: 404, want the box", id)
	}
	return b
}

// startAction sends body to path, expects status and returns the action.
func startAction(t *testing.T, e *boxEnv, method, path string, body any, status int) actionView {
	t.Helper()
	resp := e.Do(method, path, body)
	if resp.StatusCode != status {
		wantError(t, resp, status, "", fmt.Sprintf("%s %s %v", method, path, body))
		t.FailNow()
	}
	var b struct {
		Action actionView `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	return b.Action
}

// runAction posts body to path, expects 201 and an action command that
// succeeds, and returns the action.
func runAction(t *testing.T, e *boxEnv, path string, body any, command string) actionView {
	t.Helper()
	a := startAction(t, e, http.MethodPost, path, body, http.StatusCreated)
	if a.Command != command {
		t.Errorf("POST %s: action %q, want %s", path, a.Command, command)
	}
	waitSuccess(t, e, a, "POST "+path)
	return a
}

// holdAction closes the Apply gate, posts body to path (201, action
// command) and waits until the action's Apply is held, so the action stays
// running. The returned finish lets it through, opens the gate and waits
// for the action to succeed.
func holdAction(t *testing.T, e *boxEnv, path string, body any, command string) (actionView, func()) {
	t.Helper()
	e.gate.Close()
	a := startAction(t, e, http.MethodPost, path, body, http.StatusCreated)
	if a.Command != command {
		t.Errorf("POST %s: action %q, want %s", path, a.Command, command)
	}
	release := e.gate.Next(t, "POST "+path)
	return a, func() {
		t.Helper()
		release()
		e.gate.Open()
		waitSuccess(t, e, a, "POST "+path)
	}
}

// subaccountBody returns a valid subaccount create body.
func subaccountBody(home string) map[string]any {
	return map[string]any{"home_directory": home, "password": validPassword}
}

// postSubaccount posts a subaccount create for box and returns the response.
func postSubaccount(e *boxEnv, box int64, body map[string]any) *http.Response {
	return e.Do(http.MethodPost, subaccountsPath(box), body)
}

type createdSubaccount struct {
	Subaccount struct {
		ID         int64 `json:"id"`
		StorageBox int64 `json:"storage_box"`
	} `json:"subaccount"`
	Action actionView `json:"action"`
}

// startSubaccount posts a subaccount create, expects 201 and returns the
// answer without waiting.
func startSubaccount(t *testing.T, e *boxEnv, box int64, body map[string]any) createdSubaccount {
	t.Helper()
	resp := postSubaccount(e, box, body)
	if resp.StatusCode != http.StatusCreated {
		wantError(t, resp, http.StatusCreated, "", fmt.Sprintf("POST %s %v", subaccountsPath(box), body))
		t.FailNow()
	}
	var b createdSubaccount
	apitest.Decode(t, resp, &b)
	if b.Action.Command != "create_subaccount" {
		t.Errorf("create action %+v, want create_subaccount", b.Action)
	}
	return b
}

// createSubaccount creates a subaccount of box, waits for its
// create_subaccount action to succeed and returns it as GET shows it.
func createSubaccount(t *testing.T, e *boxEnv, box int64, body map[string]any) subaccountView {
	t.Helper()
	b := startSubaccount(t, e, box, body)
	waitSuccess(t, e, b.Action, "create subaccount of box "+fmt.Sprint(box))
	return mustSubaccount(t, e, box, b.Subaccount.ID)
}

// getSubaccount returns the subaccount and whether it exists.
func getSubaccount(t *testing.T, e *boxEnv, box, sub int64) (subaccountView, bool) {
	t.Helper()
	resp := e.Do(http.MethodGet, subaccountPath(box, sub), nil)
	if resp.StatusCode == http.StatusNotFound {
		wantError(t, resp, http.StatusNotFound, "not_found", "GET "+subaccountPath(box, sub))
		return subaccountView{}, false
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", subaccountPath(box, sub), resp.StatusCode)
	}
	var b struct {
		Subaccount subaccountView `json:"subaccount"`
	}
	apitest.Decode(t, resp, &b)
	return b.Subaccount, true
}

func mustSubaccount(t *testing.T, e *boxEnv, box, sub int64) subaccountView {
	t.Helper()
	s, ok := getSubaccount(t, e, box, sub)
	if !ok {
		t.Fatalf("subaccount %d of box %d: 404, want it", sub, box)
	}
	return s
}

// hashes is the _hlab.hashes part of a stored box or subaccount.
type hashes struct {
	Crypt string `json:"crypt"`
	NT    string `json:"nt"`
}

// stored reads the stored document kind/id of project "p": its raw JSON
// and its _hlab.hashes.
func stored(t *testing.T, e *boxEnv, kind string, id int64) (string, hashes) {
	t.Helper()
	var raw json.RawMessage
	err := e.Store.Tx(func(tx store.Tx) error {
		var err error
		raw, err = store.Get[json.RawMessage](tx, kind, e.Project.ID, id)
		return err
	})
	if err != nil {
		t.Fatalf("stored %s %d: %v", kind, id, err)
	}
	var doc struct {
		HLab struct {
			Hashes hashes `json:"hashes"`
		} `json:"_hlab"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("stored %s %d: %v", kind, id, err)
	}
	return string(raw), doc.HLab.Hashes
}

// wantHashes checks that h holds a sha512-crypt hash and the NT hash of pw.
func wantHashes(t *testing.T, h hashes, pw, what string) {
	t.Helper()
	if !strings.HasPrefix(h.Crypt, "$6$") {
		t.Errorf("%s: crypt hash %q, want a $6$ hash", what, h.Crypt)
	} else if got, err := shacrypt.SHA512(pw, h.Crypt); err != nil || got != h.Crypt {
		t.Errorf("%s: crypt hash does not verify the password (err %v)", what, err)
	}
	if want := nthash.Hex(pw); h.NT != want {
		t.Errorf("%s: NT hash %q, want %q", what, h.NT, want)
	}
}

// storedBox reads the stored document of box id.
func storedBox(t *testing.T, e *boxEnv, id int64) (string, hashes) {
	t.Helper()
	return stored(t, e, "storage_boxes", id)
}

// storedSubaccount reads the stored document of subaccount id.
func storedSubaccount(t *testing.T, e *boxEnv, id int64) (string, hashes) {
	t.Helper()
	return stored(t, e, "storage_box_subaccounts", id)
}

// doRaw sends a request and returns status and body text.
func doRaw(t *testing.T, e *boxEnv, method, path string, body any) (int, string) {
	t.Helper()
	resp := e.Do(method, path, body)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	return resp.StatusCode, string(data)
}

// logBuffer collects log output written from several goroutines.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLog sends the default logger, at debug level, to a buffer until
// t ends. A test using it must not run in parallel.
func captureLog(t *testing.T) *logBuffer {
	t.Helper()
	buf := &logBuffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}
