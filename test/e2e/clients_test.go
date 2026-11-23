//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Pinned client versions (SPEC "Stack"). The harness checks them and
// installs nothing.
const (
	tofuVersion       = "1.13.9"
	providerVersion   = "1.70.2"
	ansibleVersion    = "2.22.0"
	collectionVersion = "7.2.0"
	hcloudPyVersion   = "2.27.0"
	// distroEnv names the WSL2 distro explicitly; without it the one
	// registered distro whose name starts with Ubuntu is used.
	distroEnv = "HLAB_E2E_WSL_DISTRO"
)

// wslEnv is the selected Ubuntu distro and the temporary directory in its
// home directory holding a copy of the run's private key (mode 0600). teardown deletes
// the directory.
type wslEnv struct {
	distro string
	dir    string // temporary directory in the distro
	key    string // private key inside dir
}

var (
	wslOnce sync.Once
	wslVal  *wslEnv
	wslErr  error

	ansibleOnce sync.Once
	ansibleErr  error

	tofuOnce sync.Once
	tofuErr  error
	tofuBin  string
	// tofuInitMu serialises `tofu init`: the shared plugin cache is not safe
	// for concurrent writers.
	tofuInitMu sync.Mutex
)

// sharedWSL selects the distro and copies the run's key into it once.
func sharedWSL(t *testing.T) (*env, *wslEnv) {
	t.Helper()
	e := sharedEnv(t)
	wslOnce.Do(func() { wslVal, wslErr = newWSL(e) })
	if wslErr != nil {
		t.Fatalf("WSL2 setup: %v", wslErr)
	}
	return e, wslVal
}

func newWSL(e *env) (*wslEnv, error) {
	distro, err := selectDistro()
	if err != nil {
		return nil, err
	}
	w := &wslEnv{distro: distro}

	r, err := w.run(context.Background(), 2*time.Minute, nil, `. /etc/os-release; printf '%s %s' "$ID" "$VERSION_ID"`)
	if err != nil {
		return nil, fmt.Errorf("os-release of distro %s: %v\n%s", distro, err, r)
	}
	if id, ver, _ := strings.Cut(strings.TrimSpace(r.Stdout), " "); id != "ubuntu" || !atLeast(ver, 24, 4) {
		return nil, fmt.Errorf("distro %s is %q, want Ubuntu 24.04 or newer (set %s)", distro, r.Stdout, distroEnv)
	}

	key, err := os.ReadFile(e.key)
	if err != nil {
		return nil, err
	}
	// The directory lives in the home directory, not in /tmp: /tmp is a
	// tmpfs under systemd and WSL2 stops an idle distro, which empties it
	// between two steps of the run. Copies older than a day are left over
	// from runs that ended without teardown.
	script := "set -e\numask 077\nmkdir -p \"$HOME/.cache\"\n" +
		"find \"$HOME/.cache\" -maxdepth 1 -name 'hlab-e2e.*' -mmin +1440 -exec rm -rf -- {} +\n" +
		"d=$(mktemp -d \"$HOME/.cache/hlab-e2e.XXXXXXXX\")\ncat > \"$d/id_ed25519\" <<'HLAB_KEY_EOF'\n" +
		strings.TrimSpace(strings.ReplaceAll(string(key), "\r", "")) +
		"\nHLAB_KEY_EOF\nchmod 600 \"$d/id_ed25519\"\nprintf '%s' \"$d\"\n"
	r, err = w.run(context.Background(), 2*time.Minute, nil, script)
	if err != nil || !strings.HasPrefix(strings.TrimSpace(r.Stdout), "/") {
		return nil, fmt.Errorf("copying the test key into distro %s: %v (exit %d)\nstderr: %s", distro, err, r.ExitCode, r.Stderr)
	}
	w.dir = strings.TrimSpace(r.Stdout)
	w.key = w.dir + "/id_ed25519"
	return w, nil
}

// selectDistro returns HLAB_E2E_WSL_DISTRO or the one registered distro
// whose name starts with Ubuntu; never the default distro implicitly.
func selectDistro() (string, error) {
	if d := strings.TrimSpace(os.Getenv(distroEnv)); d != "" {
		return d, nil
	}
	environ := append(os.Environ(), "WSL_UTF8=1")
	r, err := command(context.Background(), time.Minute, environ, "", "wsl.exe", "--list", "--quiet")
	if err != nil {
		return "", fmt.Errorf("wsl.exe --list --quiet: %v\n%s", err, r)
	}
	// Without WSL_UTF8 support the list comes as UTF-16; dropping the zero
	// bytes leaves the ASCII names.
	out := strings.NewReplacer("\x00", "", "\uFEFF", "", "\r", "").Replace(r.Stdout)
	var found []string
	for _, line := range strings.Split(out, "\n") {
		if name := strings.TrimSpace(line); strings.HasPrefix(name, "Ubuntu") {
			found = append(found, name)
		}
	}
	if len(found) != 1 {
		return "", fmt.Errorf("found %d WSL2 distros starting with Ubuntu (%v); set %s to the one to use", len(found), found, distroEnv)
	}
	return found[0], nil
}

// atLeast compares a VERSION_ID like 24.04 with major.minor.
func atLeast(ver string, major, minor int) bool {
	maj, mnr, _ := strings.Cut(ver, ".")
	a, err1 := strconv.Atoi(maj)
	b, err2 := strconv.Atoi(mnr)
	if err1 != nil || err2 != nil {
		return false
	}
	return a > major || a == major && b >= minor
}

// run feeds script to a login bash in the selected distro through stdin, so
// nothing needs quoting for wsl.exe and the user's PATH (uv tools) applies.
func (w *wslEnv) run(ctx context.Context, timeout time.Duration, environ []string, script string) (result, error) {
	return command(ctx, timeout, environ, script, "wsl.exe", "-d", w.distro, "-e", "bash", "-l", "-s")
}

// removeWSLDir deletes the temporary directory with the key copy.
func removeWSLDir() error {
	if wslVal == nil || wslVal.dir == "" {
		return nil
	}
	r, err := wslVal.run(context.Background(), 2*time.Minute, nil, "rm -rf -- '"+wslVal.dir+"'")
	if err != nil {
		return fmt.Errorf("removing %s in distro %s: %v\n%s", wslVal.dir, wslVal.distro, err, r)
	}
	return nil
}

// ansibleEnviron hands HCLOUD_TOKEN, HCLOUD_ENDPOINT and HETZNER_ENDPOINT
// to the distro through WSLENV; nothing else of the client configuration changes.
func (e *env) ansibleEnviron() []string {
	out := make([]string, 0, len(e.hcloudEnv)+1)
	wslenv := "HCLOUD_TOKEN/u:HCLOUD_ENDPOINT/u:" + hetznerEnvVar + "/u"
	for _, kv := range e.hcloudEnv {
		if k, v, _ := strings.Cut(kv, "="); strings.EqualFold(k, "WSLENV") {
			if v != "" {
				wslenv = v + ":" + wslenv
			}
			continue
		}
		out = append(out, kv)
	}
	return append(out, "WSLENV="+wslenv)
}

// sharedAnsible checks ansible-core, the collection and hcloud-python in the
// selected distro once.
func sharedAnsible(t *testing.T) (*env, *wslEnv) {
	t.Helper()
	e, w := sharedWSL(t)
	ansibleOnce.Do(func() { ansibleErr = checkAnsible(w) })
	if ansibleErr != nil {
		t.Fatalf("Ansible in distro %s: %v", w.distro, ansibleErr)
	}
	return e, w
}

var pythonPathRe = regexp.MustCompile(`(?m)^\s*python version = .*\((/[^)]+)\)\s*$`)

func checkAnsible(w *wslEnv) error {
	ctx := context.Background()
	r, err := w.run(ctx, 2*time.Minute, nil, "ansible --version")
	if err != nil {
		return fmt.Errorf("ansible --version (install ansible-core %s with uv): %v\n%s", ansibleVersion, err, r)
	}
	if !strings.Contains(r.Stdout, "[core "+ansibleVersion+"]") {
		return fmt.Errorf("want ansible-core %s, got:\n%s", ansibleVersion, r.Stdout)
	}
	m := pythonPathRe.FindStringSubmatch(r.Stdout)
	if m == nil {
		return fmt.Errorf("ansible --version names no Python interpreter:\n%s", r.Stdout)
	}
	python := m[1]

	r, err = w.run(ctx, 2*time.Minute, nil, "ansible-galaxy collection list hetzner.hcloud")
	if err != nil || !regexp.MustCompile(`(?m)^hetzner\.hcloud\s+`+regexp.QuoteMeta(collectionVersion)+`\s*$`).MatchString(r.Stdout) {
		return fmt.Errorf("want collection hetzner.hcloud %s: %v\n%s", collectionVersion, err, r)
	}

	r, err = w.run(ctx, 2*time.Minute, nil, "'"+python+"' - <<'HLAB_PY_EOF'\nimport importlib.metadata as m\nprint(m.version('hcloud'))\nHLAB_PY_EOF\n")
	if err != nil || strings.TrimSpace(r.Stdout) != hcloudPyVersion {
		return fmt.Errorf("want hcloud-python %s next to ansible-core (%s): %v\n%s", hcloudPyVersion, python, err, r)
	}
	return nil
}

// playbook runs a playbook for localhost in the selected distro with the
// interpreter of ansible-core itself (the uv environment, not the distro's
// Python) and the hlab endpoint and token from the environment.
func (e *env) playbook(t *testing.T, w *wslEnv, name, content string) result {
	t.Helper()
	return e.playbookWithin(t, w, name, content, playbookTimeout)
}

const (
	// playbookTimeout bounds a playbook that creates no server.
	playbookTimeout = 10 * time.Minute
	// playbookServerTimeout bounds a playbook that creates servers; the
	// first server of an image downloads and converts it (createServer).
	playbookServerTimeout = 45 * time.Minute
)

// playbookWithin is playbook with a deadline the caller sets.
func (e *env) playbookWithin(t *testing.T, w *wslEnv, name, content string, timeout time.Duration) result {
	t.Helper()
	file := w.dir + "/" + name + ".yml"
	script := "set -e\ncat > '" + file + "' <<'HLAB_PLAYBOOK_EOF'\n" + content + "\nHLAB_PLAYBOOK_EOF\n" +
		"ANSIBLE_NOCOLOR=1 ansible-playbook -i localhost, '" + file + "'\n"
	r, err := w.run(context.Background(), timeout, e.ansibleEnviron(), script)
	if err != nil {
		t.Fatalf("ansible-playbook %s: %v\n%s", name, err, r)
	}
	return r
}

// sharedTofu checks the pinned OpenTofu once.
func sharedTofu(t *testing.T) *env {
	t.Helper()
	e := sharedEnv(t)
	tofuOnce.Do(func() { tofuErr = checkTofu() })
	if tofuErr != nil {
		t.Fatalf("OpenTofu: %v", tofuErr)
	}
	return e
}

func checkTofu() error {
	bin, err := exec.LookPath("tofu")
	if err != nil {
		return fmt.Errorf("tofu not found on PATH (install OpenTofu %s): %w", tofuVersion, err)
	}
	r, err := command(context.Background(), time.Minute, nil, "", bin, "version", "-json")
	if err != nil {
		return fmt.Errorf("tofu version: %v\n%s", err, r)
	}
	var v struct {
		Version string `json:"terraform_version"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil || v.Version != tofuVersion {
		return fmt.Errorf("want OpenTofu %s, got %q (%v)", tofuVersion, v.Version, err)
	}
	tofuBin = bin
	return nil
}

// tofuDir is a working directory of OpenTofu for one row.
type tofuDir struct {
	e   *env
	dir string
	// timeout bounds one tofu command; zero is tofuTimeout. A row whose
	// apply creates servers sets tofuServerTimeout.
	timeout time.Duration
}

const (
	// tofuTimeout bounds a tofu command that creates no server.
	tofuTimeout = 15 * time.Minute
	// tofuServerTimeout bounds a tofu command that creates servers; the
	// first server of an image downloads and converts it (createServer).
	tofuServerTimeout = 45 * time.Minute
)

// newTofuDir creates a working directory with the provider pinned and no
// provider arguments: the endpoints and token come from HCLOUD_ENDPOINT,
// HETZNER_ENDPOINT (storage boxes, never endpoint_hetzner) and HCLOUD_TOKEN
// only.
func (e *env) newTofuDir(t *testing.T, name string) *tofuDir {
	t.Helper()
	dir := filepath.Join(harness.tmp, "tofu-"+name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	provider := `terraform {
  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "` + providerVersion + `"
    }
  }
}

provider "hcloud" {}
`
	if err := os.WriteFile(filepath.Join(dir, "provider.tf"), []byte(provider), 0o600); err != nil {
		t.Fatal(err)
	}
	return &tofuDir{e: e, dir: dir}
}

func (d *tofuDir) environ() ([]string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	plugins := filepath.Join(cache, "hlab-e2e", "tofu-plugins")
	if err := os.MkdirAll(plugins, 0o700); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(d.e.hcloudEnv)+3)
	for _, kv := range d.e.hcloudEnv {
		if strings.HasPrefix(strings.ToUpper(kv), "TF_") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "TF_PLUGIN_CACHE_DIR="+plugins, "TF_IN_AUTOMATION=1", "TF_INPUT=0"), nil
}

// writeMain replaces main.tf.
func (d *tofuDir) writeMain(t *testing.T, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(d.dir, "main.tf"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// run runs tofu in the directory; `init` is serialised.
func (d *tofuDir) run(t *testing.T, args ...string) result {
	t.Helper()
	environ, err := d.environ()
	if err != nil {
		t.Fatal(err)
	}
	if len(args) > 0 && args[0] == "init" {
		tofuInitMu.Lock()
		defer tofuInitMu.Unlock()
	}
	timeout := d.timeout
	if timeout == 0 {
		timeout = tofuTimeout
	}
	all := append([]string{"-chdir=" + d.dir}, args...)
	r, err := command(context.Background(), timeout, environ, "", tofuBin, all...)
	if err != nil {
		t.Fatalf("tofu %s: %v\n%s", strings.Join(args, " "), err, r)
	}
	return r
}

// output reads one output value as a string.
func (d *tofuDir) output(t *testing.T, name string) string {
	t.Helper()
	return strings.TrimSpace(d.run(t, "output", "-raw", name).Stdout)
}

// dataValues reads the attributes of a data source from the state. An
// output cannot carry an attribute the provider's schema marks optional and
// not computed (hcloud_primary_ip name, hcloud_volume server_id,
// hcloud_ssh_key name and fingerprint, the rule blocks of hcloud_firewall):
// when the read waits for the apply, OpenTofu plans such an output as the
// configured null and keeps it, while the data source holds what the
// provider read. Numbers come as json.Number.
func (d *tofuDir) dataValues(t *testing.T, address string) map[string]any {
	t.Helper()
	r := d.run(t, "show", "-json", "-no-color")
	var state struct {
		Values struct {
			RootModule struct {
				Resources []struct {
					Address string         `json:"address"`
					Values  map[string]any `json:"values"`
				} `json:"resources"`
			} `json:"root_module"`
		} `json:"values"`
	}
	dec := json.NewDecoder(strings.NewReader(r.Stdout))
	dec.UseNumber()
	if err := dec.Decode(&state); err != nil {
		t.Fatalf("tofu show -json: %v", err)
	}
	for _, res := range state.Values.RootModule.Resources {
		if res.Address == address {
			return res.Values
		}
	}
	t.Fatalf("%s is not in the state", address)
	return nil
}

// apiGet sends GET <path> to the hlab API with the run's token and decodes
// a JSON body into out (nil: body ignored); it returns the status code.
func (e *env) apiGet(ctx context.Context, path string, out any) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiEndpoint+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("GET %s: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}

// apiIDs returns the IDs of a list endpoint over all pages.
func (e *env) apiIDs(ctx context.Context, path, key string) ([]int64, error) {
	items, err := e.apiItems(ctx, path, key)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids, nil
}

// apiItems returns ID and name of every item of a list endpoint over all
// pages.
func (e *env) apiItems(ctx context.Context, path, key string) ([]named, error) {
	var all []named
	for page := 1; ; page++ {
		var body map[string]json.RawMessage
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		status, err := e.apiGet(ctx, fmt.Sprintf("%s%sper_page=50&page=%d", path, sep, page), &body)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("GET %s: status %d", path, status)
		}
		var items []named
		if err := json.Unmarshal(body[key], &items); err != nil {
			return nil, fmt.Errorf("GET %s: no list %q: %w", path, key, err)
		}
		all = append(all, items...)
		var meta struct {
			Pagination struct {
				NextPage *int `json:"next_page"`
			} `json:"pagination"`
		}
		if err := json.Unmarshal(body["meta"], &meta); err != nil {
			return nil, fmt.Errorf("GET %s: no meta.pagination: %w", path, err)
		}
		if meta.Pagination.NextPage == nil {
			return all, nil
		}
		if page > 1000 {
			return nil, errors.New("GET " + path + ": more than 1000 pages")
		}
	}
}
