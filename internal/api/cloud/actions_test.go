package cloud_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud/schema"

	"github.com/webcodingcat/hlab/internal/api/cloud"
	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/spectest"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/system"
	"github.com/webcodingcat/hlab/internal/web"
)

// testLab is a lab on a fake machine with the Cloud API in front of it.
type testLab struct {
	lab    *lab.Lab
	client *spectest.Client // with a read_write token of the project "default"
}

// newTestLab opens a lab with the project "default" and the Cloud API; every
// answer of its client is checked against the spec.
func newTestLab(t *testing.T) *testLab {
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
	api := web.NewAPI(web.Options{Backend: l, RateLimit: web.HetznerRateLimit, Logger: logger})
	cloud.Register(api, l)
	client := spectest.New(t, api, spectest.Cloud)
	client.Token = newToken(t, l, "default")
	return &testLab{lab: l, client: client}
}

// newToken creates a project and a read_write token of it.
func newToken(t *testing.T, l *lab.Lab, project string) string {
	t.Helper()
	created, err := l.CreateProject(project, state.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := l.CreateToken(created.ID, "test", state.PermissionReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

// act runs an action on a server of a project; with release, its steps run
// until release is closed.
func act(t *testing.T, l *lab.Lab, project string, server int64, release chan struct{}) state.Action {
	t.Helper()
	found, err := l.ProjectByName(project)
	if err != nil {
		t.Fatal(err)
	}
	table := lab.ActionTable{ResourceType: "server", Busy: "server is locked", Actions: map[string]lab.ActionSpec{
		"poweron": {Name: "start_server", Plan: func(*state.Tx, lab.Request) (lab.Plan, error) {
			if release == nil {
				return lab.Plan{}, nil
			}
			return lab.Plan{Steps: func(ctx context.Context) error {
				select {
				case <-release:
				case <-ctx.Done():
				}
				return nil
			}}, nil
		}},
	}}
	action, err := l.Act(table, "poweron", lab.Request{ProjectID: found.ID, ID: server})
	if err != nil {
		t.Fatal(err)
	}
	return action
}

// Hetzner's API: GET /actions takes IDs and returns only those of the
// token's project; listing all actions is gone.
func TestActions(t *testing.T) {
	test := newTestLab(t)
	release := make(chan struct{})
	defer close(release)
	done := act(t, test.lab, "default", 42, nil)
	running := act(t, test.lab, "default", 43, release)
	newToken(t, test.lab, "other")
	foreign := act(t, test.lab, "other", 44, nil)

	var list schema.ActionListResponse
	response := test.client.Get(fmt.Sprintf("/v1/actions?id=%d&id=%d&id=%d", done.ID, running.ID, foreign.ID))
	response.Decode(t, &list)
	if response.Status != 200 || len(list.Actions) != 2 {
		t.Fatalf("answered %d %s", response.Status, response.Body)
	}
	first, second := list.Actions[0], list.Actions[1]
	if first.ID != done.ID || first.Status != "success" || first.Finished == nil || first.Command != "start_server" {
		t.Errorf("the finished action is %+v", first)
	}
	if second.ID != running.ID || second.Status != "running" || second.Finished != nil {
		t.Errorf("the running action is %+v", second)
	}
	if len(first.Resources) != 1 || first.Resources[0] != (schema.ActionResourceReference{ID: 42, Type: "server"}) {
		t.Errorf("the resources are %+v", first.Resources)
	}

	var one schema.ActionGetResponse
	response = test.client.Get(fmt.Sprintf("/v1/actions/%d", done.ID))
	response.Decode(t, &one)
	if response.Status != 200 || one.Action.ID != done.ID {
		t.Errorf("GET /actions/%d answered %d %s", done.ID, response.Status, response.Body)
	}
}

// Hetzner's API (measured 2026-11-07): an action of no project of the token
// is 404 with the plain text "Not Found", not JSON.
func TestActionsOfOtherProjectsAreNotFound(t *testing.T) {
	test := newTestLab(t)
	newToken(t, test.lab, "other")
	foreign := act(t, test.lab, "other", 44, nil)
	for _, path := range []string{fmt.Sprintf("/v1/actions/%d", foreign.ID), "/v1/actions/999999999"} {
		response := test.client.Get(path)
		if response.Status != 404 || string(response.Body) != "Not Found\n" ||
			response.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Errorf("GET %s answered %d %q (%s)", path, response.Status, response.Body, response.Header.Get("Content-Type"))
		}
	}
}

// Hetzner's API (measured 2026-11-07): without IDs, listing actions is a
// removed functionality (410); an ID that is no number is invalid input, but
// with the status 406.
func TestActionsNeedIDs(t *testing.T) {
	test := newTestLab(t)
	for query, want := range map[string]struct {
		status        int
		code, message string
	}{
		"":        {410, "deprecated_api_endpoint", "API functionality was removed"},
		"?id=abc": {406, "invalid_input", "invalid integer value"},
	} {
		response := test.client.Get("/v1/actions" + query)
		var body schema.ErrorResponse
		response.Decode(t, &body)
		if response.Status != want.status || body.Error.Code != want.code || body.Error.Message != want.message {
			t.Errorf("GET /v1/actions%s answered %d %s", query, response.Status, response.Body)
		}
	}
}
