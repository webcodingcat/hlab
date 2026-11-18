// Package apitest runs the Hetzner API handlers in process for tests: a
// temporary store, project "p" with a read/write token, and a request
// helper that validates every response against the frozen Hetzner OpenAPI
// description. It is imported by tests only.
package apitest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/api"
	"github.com/webcodingcat/hlab/internal/resource"
	"github.com/webcodingcat/hlab/internal/store"
)

// BaseURL is the URL clients use for the Hetzner API.
const BaseURL = "http://api.hetzner.internal/v1"

// Env is one in-process API with its own store.
type Env struct {
	t       testing.TB
	Store   *store.Store
	Actions *actions.Manager
	Router  *api.Router
	// Project is project "p"; Token is its read/write token.
	Project store.Project
	Token   string

	mu       *sync.Mutex
	projects map[string]store.Project
}

// New creates the environment and mounts mods. It fails the test when the
// OpenAPI description is not cached.
func New(t testing.TB, mods ...resource.Module) *Env {
	t.Helper()
	RequireDescription(t)

	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	mgr := actions.New(st)
	router := api.New(api.Deps{Store: st, Actions: mgr})
	for _, m := range mods {
		router.Mount(m)
	}
	e := &Env{
		t:        t,
		Store:    st,
		Actions:  mgr,
		Router:   router,
		mu:       &sync.Mutex{},
		projects: map[string]store.Project{},
	}
	e.Token = e.NewToken("p", false)
	e.Project = e.projects["p"]
	return e
}

// For returns a copy of the environment that reports on t, for use in
// subtests.
func (e *Env) For(t testing.TB) *Env {
	c := *e
	c.t = t
	return &c
}

// NewToken creates a token for project, creating the project first if this
// environment has not created it yet, and returns the token value.
func (e *Env) NewToken(project string, readOnly bool) string {
	e.t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	value := RandomToken()
	sum := sha256.Sum256([]byte(value))
	err := e.Store.Tx(func(tx store.Tx) error {
		p, ok := e.projects[project]
		if !ok {
			var err error
			p, err = store.CreateProject(tx, project)
			if err != nil {
				return err
			}
		}
		if err := store.CreateToken(tx, p.ID, "t-"+value[:8], sum[:], readOnly); err != nil {
			return err
		}
		e.projects[project] = p
		return nil
	})
	if err != nil {
		e.t.Fatalf("create token for project %q: %v", project, err)
	}
	return value
}

// Do sends a request with the read/write token of project "p". path is
// the API path without "/v1" (as in the OpenAPI description), with an
// optional query. body is nil, raw []byte or a value encoded as JSON.
func (e *Env) Do(method, path string, body any) *http.Response {
	e.t.Helper()
	return e.DoAuth("Bearer "+e.Token, method, path, body)
}

// DoAuth is Do with the given Authorization header value; "" sends none.
func (e *Env) DoAuth(authorization, method, path string, body any) *http.Response {
	e.t.Helper()
	var raw []byte
	switch b := body.(type) {
	case nil:
	case []byte:
		raw = b
	default:
		var err error
		raw, err = json.Marshal(b)
		if err != nil {
			e.t.Fatalf("encode request body: %v", err)
		}
	}
	req := httptest.NewRequestWithContext(context.Background(), method, BaseURL+path, bytes.NewReader(raw))
	if raw != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	e.Router.ServeHTTP(rec, req)
	resp := rec.Result()
	// A fresh request for the validator: the handler may have read the body.
	check := httptest.NewRequestWithContext(context.Background(), method, BaseURL+path, bytes.NewReader(raw))
	if raw != nil {
		check.Header.Set("Content-Type", "application/json")
	}
	ValidateResponse(e.t, check, resp)
	return resp
}

// Decode reads the JSON body of resp into v and closes it.
func Decode(t testing.TB, resp *http.Response, v any) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode body %q: %v", truncate(data, 500), err)
	}
}

// ErrorCode returns error.code of a Hetzner error body and closes it.
func ErrorCode(t testing.TB, resp *http.Response) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	Decode(t, resp, &body)
	return body.Error.Code
}

const tokenAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// RandomToken returns 64 random characters [A-Za-z0-9].
func RandomToken() string {
	b := make([]byte, 64)
	max := big.NewInt(int64(len(tokenAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		b[i] = tokenAlphabet[n.Int64()]
	}
	return string(b)
}

// NoopMachines satisfies the servers module's machine dependency
// (servers.Machines) without touching Hyper-V: every call succeeds.
type NoopMachines struct{}

// Create pretends to create and start the server's VM.
func (NoopMachines) Create(context.Context, int64) error { return nil }

// Delete pretends to delete the server's VM and disk.
func (NoopMachines) Delete(context.Context, int64) error { return nil }

// errBlockingFailed is what BlockingMachines.Create returns after Fail.
var errBlockingFailed = errors.New("machine creation failed (test)")

// BlockingMachines satisfies servers.Machines for lock and progress tests:
// Create blocks until Release or Fail. Release lets every waiting and later
// Create succeed; Fail makes them fail. Delete always succeeds.
type BlockingMachines struct {
	done   chan struct{}
	once   sync.Once
	failed atomic.Bool
}

// NewBlockingMachines returns machines whose Create blocks.
func NewBlockingMachines() *BlockingMachines {
	return &BlockingMachines{done: make(chan struct{})}
}

// Release lets every blocked and later Create succeed.
func (b *BlockingMachines) Release() { b.once.Do(func() { close(b.done) }) }

// Fail lets every blocked and later Create fail.
func (b *BlockingMachines) Fail() {
	b.once.Do(func() {
		b.failed.Store(true)
		close(b.done)
	})
}

// Create blocks until Release or Fail, or until ctx ends.
func (b *BlockingMachines) Create(ctx context.Context, _ int64) error {
	select {
	case <-b.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	if b.failed.Load() {
		return errBlockingFailed
	}
	return nil
}

// Delete pretends to delete the server's VM and disk.
func (*BlockingMachines) Delete(context.Context, int64) error { return nil }
