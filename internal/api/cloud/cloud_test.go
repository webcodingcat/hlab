package cloud_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/webcodingcat/hlab/internal/api/cloud"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/platform/fake"
	"github.com/webcodingcat/hlab/internal/spectest"
	"github.com/webcodingcat/hlab/internal/state"
	"github.com/webcodingcat/hlab/internal/system"
	"github.com/webcodingcat/hlab/internal/web"
)

// testLab is a lab on a fake machine with the Cloud API in front of it.
type testLab struct {
	lab     *lab.Lab
	client  *spectest.Client // with a read_write token of the project "default"
	machine *fake.Fake
}

// newTestLab opens a lab with the built-in catalog, the project "default"
// and the Cloud API; every answer of its client is checked against the spec.
// Its system images come from a local server, as a small qcow2 file.
func newTestLab(t *testing.T) *testLab {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	machine := fake.New()
	l, err := lab.Open(lab.Options{DataDir: t.TempDir(), Logger: logger, Catalog: localImages(t)}, system.New(machine.Platform()))
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
	return &testLab{lab: l, client: client, machine: machine}
}

// localImages returns the built-in catalog with its images' sources on a
// local HTTP server: a small qcow2 file and its SHA256SUMS.
func localImages(t *testing.T) *catalog.Catalog {
	t.Helper()
	disk, err := os.ReadFile(filepath.Join("..", "..", "imagefmt", "testdata", "plain.qcow2"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(disk)
	files := http.NewServeMux()
	files.HandleFunc("GET /image.qcow2", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(disk) })
	files.HandleFunc("GET /SHA256SUMS", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "%s *image.qcow2\n", hex.EncodeToString(sum[:]))
	})
	server := httptest.NewServer(files)
	t.Cleanup(server.Close)
	offer := *catalog.Builtin()
	offer.Images = slices.Clone(offer.Images)
	for i := range offer.Images {
		offer.Images[i].Source = catalog.ImageSource{URL: server.URL + "/image.qcow2", Checksums: server.URL + "/SHA256SUMS"}
	}
	return &offer
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
