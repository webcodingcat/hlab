package cloud_test

import (
	"encoding/json"
	"log/slog"
	"testing"

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

// newTestLab opens a lab with the built-in catalog, the project "default"
// and the Cloud API; every answer of its client is checked against the spec.
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

// apiError is an error answer of the API, with its details kept as JSON.
type apiError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details"`
}

// errorOf returns the error of an answer.
func errorOf(t *testing.T, response *spectest.Response) apiError {
	t.Helper()
	var body struct {
		Error apiError `json:"error"`
	}
	response.Decode(t, &body)
	return body.Error
}
