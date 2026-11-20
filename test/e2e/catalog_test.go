//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// realEndpoint is the endpoint a usable hcloud context points at.
const realEndpoint = "https://api.hetzner.cloud/v1"

// tokenVar is the variable the harness hands the real Hetzner token in.
const tokenVar = "HLAB_E2E_HETZNER_TOKEN"

// testCAT03: with a usable hcloud context, the real token goes through an
// environment variable to `hlab catalog update --hetzner-token-env`, and
// `hcloud server-type list` against hlab then lists the real types. The
// token is taken through hcloud itself and never printed or written.
func testCAT03(t *testing.T) {
	e := sharedEnv(t)
	ctx := context.Background()

	acc, why := usableHetzner(ctx)
	if why != "" {
		t.Skipf("no usable hcloud context: %s", why)
	}
	want, token := acc.types, acc.token

	// The update stays: the dev profile keeps Hetzner's current server types, as after a user's hlab catalog update.

	// The token goes only into the environment of this one child process.
	environ := append(os.Environ(), tokenVar+"="+token)
	r, err := command(ctx, 5*time.Minute, environ, "", harness.hlab, "catalog", "update", "--hetzner-token-env", tokenVar)
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("hlab catalog update --hetzner-token-env %s: %v\n%s", tokenVar, err, redact(r, token))
	}
	if strings.Contains(r.Stdout+r.Stderr, token) {
		t.Error("hlab catalog update printed the token")
	}

	r, err = e.hcloudRun(ctx, 2*time.Minute, "server-type", "list", "-o", "json")
	if err != nil {
		t.Fatalf("hcloud server-type list against hlab: %v\n%s", err, r)
	}
	got, err := typeNames(r.Stdout)
	if err != nil {
		t.Fatalf("hcloud server-type list against hlab: %v\n%s", err, r.Stdout)
	}
	for name := range want {
		if !got[name] {
			t.Errorf("hlab does not list server type %q of the real API after the update", name)
		}
	}
}

func typeNames(out string) (map[string]bool, error) {
	var list []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(list))
	for _, st := range list {
		names[st.Name] = true
	}
	return names, nil
}

// redact hides the token in a command result before it reaches a log.
func redact(r result, token string) result {
	r.Stdout = strings.ReplaceAll(r.Stdout, token, "<token>")
	r.Stderr = strings.ReplaceAll(r.Stderr, token, "<token>")
	return r
}
