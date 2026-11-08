//go:build linux

package agent

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/agentproto"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/picture"
)

// newTestAgent returns an agent whose files go to a temporary folder.
func newTestAgent(t *testing.T) *Agent {
	t.Helper()
	return New(Options{
		Version:     "test",
		Dir:         t.TempDir(),
		RestartDHCP: func(string) []string { return []string{"true"} },
		StopDHCP:    func(string) []string { return []string{"true"} },
		Log:         slog.New(slog.DiscardHandler),
	})
}

// put sends a picture's body to the agent and returns the answer.
func put(t *testing.T, agent *Agent, name, body string) *http.Response {
	t.Helper()
	request := httptest.NewRequest(http.MethodPut, agentproto.PathPictures+name, strings.NewReader(body))
	recorder := httptest.NewRecorder()
	agent.Handler().ServeHTTP(recorder, request)
	return recorder.Result()
}

// mustJSON returns a value as JSON.
func mustJSON(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// putPicture sends a picture as JSON and fails the test unless it was
// applied.
func putPicture(t *testing.T, agent *Agent, name string, wanted any) {
	t.Helper()
	answer := put(t, agent, name, mustJSON(t, wanted))
	if answer.StatusCode != http.StatusNoContent {
		text, _ := io.ReadAll(answer.Body)
		t.Fatalf("PUT %s: %d %s", name, answer.StatusCode, text)
	}
}

// status returns the agent's status.
func status(t *testing.T, agent *Agent) agentproto.Status {
	t.Helper()
	recorder := httptest.NewRecorder()
	agent.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, agentproto.PathStatus, nil))
	var answer agentproto.Status
	if err := json.NewDecoder(recorder.Body).Decode(&answer); err != nil {
		t.Fatal(err)
	}
	return answer
}

func TestStatusReportsVersionAndGenerations(t *testing.T) {
	agent := newTestAgent(t)
	if got := status(t, agent); got.Version != "test" || len(got.Generations) != 0 {
		t.Errorf("a new agent reports %+v", got)
	}
	putPicture(t, agent, agentproto.PictureMetadata, agentproto.Metadata{Generation: 3})
	if got := status(t, agent).Generations; got[agentproto.PictureMetadata] != 3 {
		t.Errorf("generations %v, want metadata 3", got)
	}
}

func TestBadPicturesAreRefusedWithTheirStatus(t *testing.T) {
	agent := newTestAgent(t)
	for _, test := range []struct {
		name, picture, body string
		want                int
	}{
		{"unknown picture", "dns", `{}`, http.StatusNotFound},
		{"broken JSON", agentproto.PictureRuleset, `{"text":`, http.StatusBadRequest},
		{"unknown field from a newer hlabd", agentproto.PictureRuleset, `{"text":"x","zones":[]}`, http.StatusBadRequest},
		{"failed check", agentproto.PictureRuleset, `{"generation":2,"text":""}`, http.StatusUnprocessableEntity},
		{"overlapping sources", agentproto.PictureMetadata,
			`{"servers":[{"sources":["198.19.0.1/32"]},{"sources":["198.19.0.0/16"]}]}`, http.StatusUnprocessableEntity},
	} {
		t.Run(test.name, func(t *testing.T) {
			answer := put(t, agent, test.picture, test.body)
			if answer.StatusCode != test.want {
				t.Errorf("status %d, want %d", answer.StatusCode, test.want)
			}
			var body agentproto.Error
			if err := json.NewDecoder(answer.Body).Decode(&body); err != nil || body.Message == "" {
				t.Errorf("the answer has no error message (%v)", err)
			}
		})
	}
	if got := status(t, agent).Generations; len(got) != 0 {
		t.Errorf("refused pictures were recorded: %v", got)
	}
}

// getMetadata asks the metadata service from a source address.
func getMetadata(agent *Agent, path, source string) (int, string) {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = source
	recorder := httptest.NewRecorder()
	agent.MetadataHandler().ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}

func TestMetadataServiceAnswersEachServerByItsAddress(t *testing.T) {
	agent := newTestAgent(t)
	lab := picture.DefaultLab()
	lab.Servers = []picture.Server{
		{ID: 1000001, Name: "web", Location: "fsn1", MAC: "96:00:00:0f:42:41",
			IPv4: netip.MustParseAddr("198.19.0.1"), IPv6: netip.MustParsePrefix("2001:db8:1:1::/64"),
			UserData: "#cloud-config\n"},
		{ID: 1000002, Name: "db", Location: "nbg1", MAC: "96:00:00:0f:42:42",
			IPv4: netip.MustParseAddr("198.19.0.2")},
	}
	putPicture(t, agent, agentproto.PictureMetadata, picture.Metadata(lab, catalog.Builtin()))

	for _, test := range []struct {
		path, source string
		code         int
		body         string
	}{
		{"/hetzner/v1/metadata/hostname", "198.19.0.1:40000", 200, "web"},
		{"/hetzner/v1/metadata/hostname", "[2001:db8:1:1::1]:40000", 200, "web"},
		{"/hetzner/v1/metadata/hostname", "[::ffff:198.19.0.2]:40000", 200, "db"},
		{"/hetzner/v1/metadata/instance-id", "198.19.0.2:40000", 200, "1000002"},
		{"/hetzner/v1/metadata/no-such-key", "198.19.0.2:40000", 404, ""},
		{"/hetzner/v1/userdata", "198.19.0.1:40000", 200, "#cloud-config\n"},
		{"/hetzner/v1/userdata", "198.19.0.2:40000", 204, ""},
		{"/hetzner/v1/metadata", "198.19.0.9:40000", 404, ""},
	} {
		code, body := getMetadata(agent, test.path, test.source)
		if code != test.code || test.body != "" && body != test.body {
			t.Errorf("GET %s from %s: %d %q, want %d %q", test.path, test.source, code, body, test.code, test.body)
		}
	}
	if code, body := getMetadata(agent, "/hetzner/v1/metadata", "198.19.0.1:40000"); code != 200 ||
		!strings.Contains(body, "public-ipv4: 198.19.0.1\n") {
		t.Errorf("GET /hetzner/v1/metadata: %d\n%s", code, body)
	}
}
