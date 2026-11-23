//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// clientRow is one row of the client matrix, registered from init() of
// its group file so group plans never touch a shared file.
type clientRow struct {
	Req     string // "CLI-01" (hcloud), "CLI-02" (tofu), "CLI-03" (Ansible), "CLI-04" (example)
	Name    string // subtest name: resource type ("servers", "load_balancers") or client ("hcloud")
	Fixture bool   // runs on the client's fixture server, one row after another
	Slots   int    // server slots taken in one step before Run; 0 for Fixture rows
	Run     func(t *testing.T, c *rowCtx)
}

// rowCtx is what a row gets: the run's env (CLI-04 makes its own) and, for
// Fixture rows, the client's fixture server. The fixture server carries the
// label fixtureLabel=<client>.
type rowCtx struct {
	E       *env
	Fixture *server // nil unless the row is a Fixture row
}

// fixtureLabel is the label key of every fixture server; its value is the
// client (clientOf).
const fixtureLabel = "client"

var (
	clientRowsMu sync.Mutex
	clientRows   []clientRow
)

func registerRow(r clientRow) {
	clientRowsMu.Lock()
	defer clientRowsMu.Unlock()
	clientRows = append(clientRows, r)
}

// clientOf names the client of a requirement in resource names.
func clientOf(req string) string {
	switch req {
	case "CLI-01", "CLI-05":
		return "hcloud"
	case "CLI-02", "CLI-06":
		return "tofu"
	case "CLI-03", "CLI-07":
		return "ansible"
	default:
		return "example"
	}
}

// resName is a resource name of the run: <runID>-<parts joined by ->.
func resName(parts ...string) string {
	return harness.runID + "-" + strings.Join(parts, "-")
}

// testClients runs the rows of req as subtests named r.Name: first the
// Fixture rows one after another (the fixture server is created lazily by
// the first one that runs, holding one slot, and deleted after the last),
// then the other rows in parallel, each taking r.Slots in one step. Every
// row is wrapped in logged.
func testClients(t *testing.T, req string) {
	clientRowsMu.Lock()
	var rows []clientRow
	for _, r := range clientRows {
		if r.Req == req {
			rows = append(rows, r)
		}
	}
	clientRowsMu.Unlock()
	slices.SortFunc(rows, func(a, b clientRow) int { return strings.Compare(a.Name, b.Name) })

	fx := &clientFixture{name: resName(clientOf(req), "fixture"), client: clientOf(req)}
	t.Cleanup(func() { fx.release(t) })
	for _, r := range rows {
		if !r.Fixture {
			continue
		}
		t.Run(r.Name, logged(func(t *testing.T) {
			srv := fx.get(t)
			r.Run(t, &rowCtx{E: sharedEnv(t), Fixture: srv})
		}))
	}
	fx.release(t)

	for _, r := range rows {
		if r.Fixture {
			continue
		}
		t.Run(r.Name, func(t *testing.T) {
			t.Parallel()
			logged(func(t *testing.T) {
				if r.Slots > 0 {
					takeServerSlots(t, r.Slots)
				}
				r.Run(t, &rowCtx{E: sharedEnv(t)})
			})(t)
		})
	}
}

// clientFixture is the fixture server of one client; it holds one slot
// from its creation to its deletion.
type clientFixture struct {
	name   string
	client string
	mu     sync.Mutex
	tried  bool
	srv    *server
	err    error
	slots  int
}

func (f *clientFixture) get(t *testing.T) *server {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.tried {
		f.tried = true
		e := sharedEnv(t)
		f.slots = serverSlots.acquire(1)
		f.srv, f.err = e.createServerImage(f.name, serverImage, "--label", fixtureLabel+"="+f.client)
		if f.err == nil {
			f.err = e.waitReady(f.srv)
		}
	}
	if f.err != nil {
		t.Fatalf("fixture server %s: %v", f.name, f.err)
	}
	return f.srv
}

// release deletes the fixture server, if one was made, and gives its slot
// back; a second call does nothing.
func (f *clientFixture) release(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.tried || f.slots == 0 {
		return
	}
	deleteIfCreated(t, sharedEnv(t), f.name)
	serverSlots.release(f.slots)
	f.slots = 0
}

// logged logs the start, the end and the elapsed time of a row.
func logged(f func(t *testing.T)) func(t *testing.T) {
	return func(t *testing.T) {
		start := time.Now()
		t.Logf("%s start %s", t.Name(), start.Format(time.RFC3339))
		t.Cleanup(func() {
			end := time.Now()
			t.Logf("%s end %s after %s", t.Name(), end.Format(time.RFC3339), end.Sub(start).Round(time.Second))
		})
		f(t)
	}
}

// newProjectEnv creates a project of its own (name <runID>-<suffix>) with a
// read/write token, an hcloud environment (hlab's Cloud and Hetzner
// endpoints) and the run's SSH key registered under the run's key name; the
// project is deleted at cleanup.
func newProjectEnv(t *testing.T, suffix string) *env {
	t.Helper()
	base := sharedEnv(t)
	ctx := context.Background()
	name := harness.runID + "-" + suffix
	if r, err := runHlab(ctx, 2*time.Minute, "project", "create", name); err != nil || r.ExitCode != 0 {
		t.Fatalf("hlab project create %s: %v\n%s", name, err, r)
	}
	t.Cleanup(func() {
		if err := deleteProject(name); err != nil {
			t.Error(err)
		}
	})
	r, err := runHlab(ctx, 2*time.Minute, "token", "create", "--project", name, "--name", "e2e", "--output", "json")
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("hlab token create --project %s: %v (exit %d)\nstderr: %s", name, err, r.ExitCode, r.Stderr)
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &tok); err != nil || tok.Token == "" {
		t.Fatalf("hlab token create --project %s --output json printed no token", name)
	}

	cfg := filepath.Join(harness.tmp, "hcloud-"+suffix+".toml")
	if err := os.WriteFile(cfg, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	e := &env{
		project: name, token: tok.Token,
		key: base.key, pubKey: base.pubKey, sshKey: base.sshKey, known: base.known, userData: base.userData,
	}
	for _, kv := range base.hcloudEnv {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "HCLOUD_TOKEN", "HCLOUD_CONFIG":
			continue
		}
		e.hcloudEnv = append(e.hcloudEnv, kv)
	}
	e.hcloudEnv = append(e.hcloudEnv, "HCLOUD_TOKEN="+e.token, "HCLOUD_CONFIG="+cfg)
	e.hcloudOK(t, "ssh-key", "create", "--name", e.sshKey, "--public-key-from-file", e.pubKey)
	return e
}

// hcloudTimeout bounds one hcloud command of a row.
const hcloudTimeout = 20 * time.Minute

// hcloudOK runs hcloud with the env's endpoint and token and fails the
// test on a non-zero exit; hcloudJSON adds "-o json" and decodes stdout.
func (e *env) hcloudOK(t *testing.T, args ...string) result {
	t.Helper()
	r, err := e.hcloudRun(context.Background(), hcloudTimeout, args...)
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("hcloud %s: %v\n%s", strings.Join(args, " "), err, r)
	}
	return r
}

func (e *env) hcloudJSON(t *testing.T, out any, args ...string) {
	t.Helper()
	r := e.hcloudOK(t, append(slices.Clone(args), "-o", "json")...)
	if err := json.Unmarshal([]byte(r.Stdout), out); err != nil {
		t.Fatalf("hcloud %s -o json: %v\n%s", strings.Join(args, " "), err, r.Stdout)
	}
}

// ansibleOK runs a playbook in WSL2 and fails unless it ends with failed=0.
func (e *env) ansibleOK(t *testing.T, w *wslEnv, name, playbook string) result {
	t.Helper()
	r := e.playbook(t, w, name, playbook)
	if !strings.Contains(r.Stdout, "failed=0") {
		t.Fatalf("playbook %s did not finish without failures:\n%s", name, r)
	}
	return r
}

// localPlay is a playbook for localhost with the interpreter of
// ansible-core, the variable row (a JSON object is a YAML flow mapping)
// and tasks, which are indented by four spaces.
func localPlay(t *testing.T, row map[string]any, tasks string) string {
	t.Helper()
	if row == nil {
		row = map[string]any{}
	}
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	return `- hosts: localhost
  connection: local
  gather_facts: false
  vars:
    ansible_python_interpreter: "{{ ansible_playbook_python }}"
    row: ` + string(b) + `
  tasks:
` + tasks
}

// getOK sends GET path and fails the test unless it answers 200; the body
// goes into out.
func (e *env) getOK(t *testing.T, path string, out any) {
	t.Helper()
	status, aerr, err := e.apiDo(context.Background(), http.MethodGet, path, nil, out)
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET %s: %v (status %d %s: %s)", path, err, status, aerr.Code, aerr.Message)
	}
}

// named is an item of a list endpoint with an ID and a name.
type named struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// apiNamed returns ID and name of every item of a list endpoint over all
// pages.
func (e *env) apiNamed(t *testing.T, path, key string) map[int64]string {
	t.Helper()
	items, err := e.apiItems(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	return namedOf(items)
}

// namedOf turns a decoded client list into the map apiNamed returns.
func namedOf(items []named) map[int64]string {
	out := make(map[int64]string, len(items))
	for _, it := range items {
		out[it.ID] = it.Name
	}
	return out
}

// assertActionDone fails unless GET <resource>/actions lists a successful
// action with the command.
func (e *env) assertActionDone(t *testing.T, resource, command string) {
	t.Helper()
	var body struct {
		Actions []struct {
			Command string `json:"command"`
			Status  string `json:"status"`
		} `json:"actions"`
	}
	e.getOK(t, resource+"/actions?per_page=50&sort=id:desc", &body)
	for _, a := range body.Actions {
		if a.Command == command && a.Status == "success" {
			return
		}
	}
	t.Errorf("GET %s/actions shows no successful %s action: %+v", resource, command, body.Actions)
}

// assertGoneIDs fails if a list endpoint still lists one of ids.
func assertGoneIDs(t *testing.T, e *env, path, key string, ids ...int64) {
	t.Helper()
	listed, err := e.apiIDs(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if slices.Contains(listed, id) {
			t.Errorf("GET %s still lists %d", path, id)
		}
	}
}

// parseID parses an ID a client printed.
func parseID(t *testing.T, s string) int64 {
	t.Helper()
	id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		t.Fatalf("ID %q: %v", s, err)
	}
	return id
}

// projectLists are the Cloud API list endpoints of a project's own
// resources and the key of their list.
var projectLists = [][2]string{
	{"/servers", "servers"},
	{"/images?type=snapshot", "images"},
	{"/images?type=backup", "images"},
	{"/placement_groups", "placement_groups"},
	{"/primary_ips", "primary_ips"},
	{"/floating_ips", "floating_ips"},
	{"/volumes", "volumes"},
	{"/networks", "networks"},
	{"/firewalls", "firewalls"},
	{"/load_balancers", "load_balancers"},
	{"/certificates", "certificates"},
	{"/ssh_keys", "ssh_keys"},
	{"/zones", "zones"},
}

// assertProjectEmpty fails unless every Cloud API list endpoint of e's
// project is empty and no Hyper-V VM of serverIDs exists. The SSH key
// newProjectEnv registered is the env's, not the row's, and is left out.
func assertProjectEmpty(t *testing.T, e *env, serverIDs []int64) {
	t.Helper()
	for _, l := range projectLists {
		var left []string
		for id, name := range e.apiNamed(t, l[0], l[1]) {
			if l[1] == "ssh_keys" && name == e.sshKey {
				continue
			}
			left = append(left, fmt.Sprintf("%d %s", id, name))
		}
		if len(left) > 0 {
			t.Errorf("GET %s still lists %v", l[0], left)
		}
	}
	for _, id := range serverIDs {
		var exists bool
		err := waitFor(5*time.Minute, 5*time.Second, func() (bool, error) {
			var err error
			exists, err = vmExists(vmName(id))
			return err == nil && !exists, nil
		})
		if err != nil {
			t.Errorf("Hyper-V VM %s still exists (%v)", vmName(id), err)
		}
	}
}

// sshOptions are the options of every key login of the harness except the
// known_hosts file.
var sshOptions = []string{
	"-o", "IdentitiesOnly=yes",
	"-o", "BatchMode=yes",
	"-o", "StrictHostKeyChecking=no",
	"-o", "ConnectTimeout=10",
	"-o", "LogLevel=ERROR",
}

// sshAddr runs remote as root at addr:port with the run's key, from the
// host; (w *wslEnv).sshAddr does the same from the WSL2 distro.
func (e *env) sshAddr(addr string, port int, timeout time.Duration, remote string) (result, error) {
	args := append([]string{"-i", e.key, "-p", strconv.Itoa(port), "-o", "UserKnownHostsFile=" + e.known}, sshOptions...)
	args = append(args, "root@"+addr, remote)
	return command(context.Background(), timeout, nil, "", harness.ssh, args...)
}

func (w *wslEnv) sshAddr(addr string, port int, timeout time.Duration, remote string) (result, error) {
	if strings.Contains(remote, "HLAB_REMOTE_EOF") {
		return result{}, errors.New("remote command contains the heredoc marker")
	}
	script := "ssh -i '" + w.key + "' -p " + strconv.Itoa(port) + " -o UserKnownHostsFile=/dev/null " +
		strings.Join(sshOptions, " ") + " root@" + addr + " \"$(cat <<'HLAB_REMOTE_EOF'\n" + remote + "\nHLAB_REMOTE_EOF\n)\"\n"
	return w.run(context.Background(), timeout, nil, script)
}

// serverDetail is what the client rows check of GET /servers/{id}.
type serverDetail struct {
	ID         int64             `json:"id"`
	Name       string            `json:"name"`
	Status     string            `json:"status"`
	Labels     map[string]string `json:"labels"`
	ServerType struct {
		Name string `json:"name"`
	} `json:"server_type"`
	Image *struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"image"`
	ISO *struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"iso"`
	RescueEnabled bool    `json:"rescue_enabled"`
	BackupWindow  *string `json:"backup_window"`
	Protection    struct {
		Delete  bool `json:"delete"`
		Rebuild bool `json:"rebuild"`
	} `json:"protection"`
	PlacementGroup *struct {
		ID int64 `json:"id"`
	} `json:"placement_group"`
	PublicNet struct {
		IPv4 *struct {
			IP     string `json:"ip"`
			DNSPtr string `json:"dns_ptr"`
		} `json:"ipv4"`
	} `json:"public_net"`
}

// serverInfo is GET /servers/{id}.
func (e *env) serverInfo(t *testing.T, id int64) serverDetail {
	t.Helper()
	var body struct {
		Server serverDetail `json:"server"`
	}
	e.getOK(t, fmt.Sprintf("/servers/%d", id), &body)
	return body.Server
}

// firstISO is the first catalog ISO GET /isos lists for x86 servers.
func (e *env) firstISO(t *testing.T) named {
	t.Helper()
	var body struct {
		ISOs []struct {
			named
			Architecture *string `json:"architecture"`
		} `json:"isos"`
	}
	e.getOK(t, "/isos?per_page=50", &body)
	for _, iso := range body.ISOs {
		if iso.Architecture == nil || *iso.Architecture == "x86" {
			return iso.named
		}
	}
	t.Fatal("GET /isos lists no ISO for x86")
	return named{}
}
