package control_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/webcodingcat/hlab/internal/api/cloud"
	"github.com/webcodingcat/hlab/internal/api/control"
	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/labapi"
	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/system"
	"github.com/webcodingcat/hlab/internal/web"
)

// controlToken is the tests' control token.
const controlToken = "control-token"

// uiHeader marks a request as the web UI's, which the tests' Admit vouches
// for.
const uiHeader = "X-Test-Web-UI"

// testControl is a lab with its control in front of it.
type testControl struct {
	t        *testing.T
	lab      *lab.Lab
	handler  http.Handler
	shutdown int // calls of Options.Shutdown
}

func newTestControl(t *testing.T) *testControl {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	l, err := lab.Open(lab.Options{DataDir: t.TempDir(), Logger: logger}, system.New(fake.New().Platform()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Error(err)
		}
	})
	api := web.NewAPI(web.Options{Backend: l, Logger: logger})
	cloud.Register(api, l)
	tc := &testControl{t: t, lab: l}
	tc.handler = control.New(control.Options{
		Lab:      l,
		Cloud:    api,
		Token:    controlToken,
		Admit:    func(r *http.Request) bool { return r.Header.Get(uiHeader) != "" },
		Status:   func() labapi.Status { return labapi.Status{Version: "test", State: labapi.StateRunning} },
		Shutdown: func() { tc.shutdown++ },
		Logger:   logger,
	})
	return tc
}

// do sends a request with the control token and decodes a JSON answer into
// answer, if given; it returns the status.
func (tc *testControl) do(method, path string, body, answer any) int {
	tc.t.Helper()
	return tc.send(method, path, body, answer, map[string]string{"Authorization": "Bearer " + controlToken})
}

// send sends a request with the headers.
func (tc *testControl) send(method, path string, body, answer any, headers map[string]string) int {
	tc.t.Helper()
	data, raw := body.([]byte) // sent as it is: broken JSON
	if body != nil && !raw {
		var err error
		if data, err = json.Marshal(body); err != nil {
			tc.t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(data))
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	tc.handler.ServeHTTP(recorder, request)
	if answer != nil && recorder.Body.Len() > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), answer); err != nil {
			tc.t.Fatalf("%s %s: %v in %s", method, path, err, recorder.Body.String())
		}
	}
	return recorder.Code
}

func TestControlAdmitsTheControlTokenAndTheWebUI(t *testing.T) {
	tc := newTestControl(t)
	for name, test := range map[string]struct {
		headers map[string]string
		want    int
	}{
		"the control token": {map[string]string{"Authorization": "Bearer " + controlToken}, http.StatusOK},
		"the web UI":        {map[string]string{uiHeader: "yes"}, http.StatusOK},
		"no token":          {nil, http.StatusUnauthorized},
		"a wrong token":     {map[string]string{"Authorization": "Bearer wrong"}, http.StatusUnauthorized},
		// a wrong token is not made good by the web UI's mark
		"a wrong token from the web UI": {map[string]string{"Authorization": "Bearer wrong", uiHeader: "yes"}, http.StatusUnauthorized},
	} {
		var failure labapi.Error
		got := tc.send(http.MethodGet, "/_lab/status", nil, &failure, test.headers)
		if got != test.want {
			t.Errorf("%s: got %d, want %d", name, got, test.want)
		}
		if got == http.StatusUnauthorized && failure.Error.Code != "unauthorized" {
			t.Errorf("%s: got the code %q, want unauthorized", name, failure.Error.Code)
		}
	}
}

func TestStatusCountsTheProjects(t *testing.T) {
	tc := newTestControl(t)
	if _, err := tc.lab.CreateProject("one", state.Limits{}); err != nil {
		t.Fatal(err)
	}
	var status labapi.Status
	if got := tc.do(http.MethodGet, "/_lab/status", nil, &status); got != http.StatusOK {
		t.Fatalf("got %d, want 200", got)
	}
	if status.Version != "test" || status.Projects != 1 {
		t.Errorf("got %+v, want version test and one project", status)
	}
}

func TestShutdownAsksTheServiceToStop(t *testing.T) {
	tc := newTestControl(t)
	if got := tc.do(http.MethodPost, "/_lab/shutdown", nil, nil); got != http.StatusAccepted || tc.shutdown != 1 {
		t.Errorf("got %d and %d calls, want 202 and one call", got, tc.shutdown)
	}
}

func TestProjectsAreCreatedListedAndDeleted(t *testing.T) {
	tc := newTestControl(t)
	var created labapi.Project
	if got := tc.do(http.MethodPost, "/_lab/projects", labapi.ProjectRequest{Name: "web"}, &created); got != http.StatusCreated {
		t.Fatalf("create: got %d, want 201", got)
	}
	if created.Name != "web" || !created.Internet || created.ID < state.FirstID {
		t.Errorf("got %+v", created)
	}
	var projects []labapi.Project
	tc.do(http.MethodGet, "/_lab/projects", nil, &projects)
	if len(projects) != 1 || projects[0] != created {
		t.Errorf("got %+v, want [%+v]", projects, created)
	}
	if got := tc.do(http.MethodDelete, "/_lab/projects/web", nil, nil); got != http.StatusNoContent {
		t.Errorf("delete: got %d, want 204", got)
	}
	tc.do(http.MethodGet, "/_lab/projects", nil, &projects)
	if len(projects) != 0 {
		t.Errorf("got %+v after the delete, want none", projects)
	}
}

func TestControlAnswersTheLabsErrors(t *testing.T) {
	tc := newTestControl(t)
	tc.do(http.MethodPost, "/_lab/projects", labapi.ProjectRequest{Name: "web"}, nil)
	for name, test := range map[string]struct {
		method, path string
		body         any
		status       int
		code         string
	}{
		"an invalid name":     {http.MethodPost, "/_lab/projects", labapi.ProjectRequest{Name: "Web!"}, http.StatusUnprocessableEntity, "invalid_input"},
		"a name taken":        {http.MethodPost, "/_lab/projects", labapi.ProjectRequest{Name: "web"}, http.StatusConflict, "uniqueness_error"},
		"no such project":     {http.MethodDelete, "/_lab/projects/db", nil, http.StatusNotFound, "not_found"},
		"no such token":       {http.MethodDelete, "/_lab/projects/web/tokens/ci", nil, http.StatusNotFound, "not_found"},
		"another permission":  {http.MethodPost, "/_lab/projects/web/tokens", labapi.TokenRequest{Name: "ci", Permission: "admin"}, http.StatusUnprocessableEntity, "invalid_input"},
		"no such route":       {http.MethodGet, "/_lab/servers", nil, http.StatusNotFound, "not_found"},
		"broken JSON":         {http.MethodPost, "/_lab/projects", []byte(`{`), http.StatusUnprocessableEntity, "json_error"},
		"a project elsewhere": {http.MethodGet, "/_lab/projects/db/cloud/v1/locations", nil, http.StatusNotFound, "not_found"},
	} {
		var failure labapi.Error
		got := tc.do(test.method, test.path, test.body, &failure)
		if got != test.status || failure.Error.Code != test.code {
			t.Errorf("%s: got %d %q, want %d %q", name, got, failure.Error.Code, test.status, test.code)
		}
	}
}

func TestTokensAreCreatedListedAndDeleted(t *testing.T) {
	tc := newTestControl(t)
	tc.do(http.MethodPost, "/_lab/projects", labapi.ProjectRequest{Name: "web"}, nil)
	request := labapi.TokenRequest{Name: "ci", Permission: "read"}
	var created labapi.CreatedToken
	if got := tc.do(http.MethodPost, "/_lab/projects/web/tokens", request, &created); got != http.StatusCreated {
		t.Fatalf("create: got %d, want 201", got)
	}
	if len(created.Secret) != 64 || created.Token.Prefix != created.Secret[:4] || created.Token.Permission != "read" {
		t.Errorf("got %+v", created)
	}
	if got := tc.do(http.MethodPost, "/_lab/projects/web/tokens", request, nil); got != http.StatusConflict {
		t.Errorf("the same name again: got %d, want 409", got)
	}
	var tokens []labapi.Token
	tc.do(http.MethodGet, "/_lab/projects/web/tokens", nil, &tokens)
	if len(tokens) != 1 || tokens[0] != created.Token {
		t.Errorf("got %+v, want [%+v]", tokens, created.Token)
	}
	if got := tc.do(http.MethodDelete, "/_lab/projects/web/tokens/ci", nil, nil); got != http.StatusNoContent {
		t.Errorf("delete: got %d, want 204", got)
	}
	if _, found := tc.lab.TokenByHash(state.HashToken(created.Secret)); found {
		t.Error("the deleted token still works")
	}
}

func TestEnvTokenIsTheSameOnEveryCall(t *testing.T) {
	tc := newTestControl(t)
	tc.do(http.MethodPost, "/_lab/projects", labapi.ProjectRequest{Name: "web"}, nil)
	var first, second labapi.EnvToken
	if got := tc.do(http.MethodPost, "/_lab/projects/web/env-token", nil, &first); got != http.StatusOK {
		t.Fatalf("got %d, want 200", got)
	}
	tc.do(http.MethodPost, "/_lab/projects/web/env-token", nil, &second)
	if len(first.Secret) != 64 || second != first {
		t.Errorf("got %q and %q, want the same token twice", first.Secret, second.Secret)
	}
	var failure labapi.Error
	if got := tc.do(http.MethodPost, "/_lab/projects/db/env-token", nil, &failure); got != http.StatusNotFound {
		t.Errorf("another project: got %d, want 404", got)
	}
}

func TestTheWebUIReachesTheCloudAPIOfAProject(t *testing.T) {
	tc := newTestControl(t)
	tc.do(http.MethodPost, "/_lab/projects", labapi.ProjectRequest{Name: "web"}, nil)
	ui := map[string]string{uiHeader: "yes"}
	var created struct {
		SSHKey struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"ssh_key"`
	}
	key := map[string]string{"name": "laptop", "public_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGd1bO4CVdUQs8SYyXqm0p4M14Ql7Cw+oOgVYcgsv0Xr laptop"}
	if got := tc.send(http.MethodPost, "/_lab/projects/web/cloud/v1/ssh_keys", key, &created, ui); got != http.StatusCreated {
		t.Fatalf("got %d, want 201", got)
	}
	var listed struct {
		SSHKeys []struct {
			ID int64 `json:"id"`
		} `json:"ssh_keys"`
	}
	tc.send(http.MethodGet, "/_lab/projects/web/cloud/v1/ssh_keys", nil, &listed, ui)
	if len(listed.SSHKeys) != 1 || listed.SSHKeys[0].ID != created.SSHKey.ID {
		t.Errorf("got %+v, want the key %d", listed, created.SSHKey.ID)
	}
}
