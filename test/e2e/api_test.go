//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

const locationsURL = apiEndpoint + "/locations"

// curlStatus fetches the locations with the token read from stdin as a
// header (never on a command line) and prints only the status code.
const curlStatus = "curl -sS -o /dev/null -w '%{http_code}' -H @- " + locationsURL

// testAPI01: GET /v1/locations with the token answers 200 from the host, from
// WSL2 and from a server.
func testAPI01(t *testing.T) {
	e, srv := sharedServer(t)
	header := "Authorization: Bearer " + e.token + "\n"

	t.Run("host", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, locationsURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+e.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status %d, want 200", resp.StatusCode)
		}
	})

	t.Run("wsl", func(t *testing.T) {
		_, w := sharedWSL(t)
		r, err := command(context.Background(), 2*time.Minute, nil, header, "wsl.exe", "-d", w.distro, "-e", "sh", "-c", curlStatus)
		if err != nil || strings.TrimSpace(r.Stdout) != "200" {
			t.Errorf("curl from WSL2: %v\n%s", err, r)
		}
	})

	t.Run("server", func(t *testing.T) {
		r, err := e.ssh(srv, 2*time.Minute, header, curlStatus)
		if err != nil || strings.TrimSpace(r.Stdout) != "200" {
			t.Errorf("curl from server %s: %v\n%s", srv.Name, err, r)
		}
	})
}
