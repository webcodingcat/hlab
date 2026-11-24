//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// imageNames are the system images of CAT-06 and CAT-09.
var imageNames = []string{
	"ubuntu-22.04", "ubuntu-24.04", "ubuntu-26.04", "debian-12", "debian-13",
	"rocky-8", "rocky-9", "rocky-10", "alma-8", "alma-9", "alma-10",
	"centos-stream-9", "centos-stream-10", "fedora-43", "fedora-44", "opensuse-16",
}

const (
	// extraCase is the keyless rocky-8 server with a user from user_data
	// that SRV-23 and SRV-24 share besides the per-image servers.
	extraCase  = "extra"
	extraImage = "rocky-8"
	extraUser  = "tester"

	// imageWriteMarker is written by the write_files part of the image
	// servers' user_data, userDataMarker by its runcmd part.
	imageWriteMarker = "/var/lib/hlab-e2e-write-files"
	imageWriteValue  = "hlab-e2e-write-files"

	imageProbeURL = "https://example.com/"
)

// defaultUsers are the default users of the images' cloud configuration.
var defaultUsers = []string{"ubuntu", "debian", "rocky", "almalinux", "cloud-user", "fedora", "opensuse"}

// imageFix is the server of one image (or of extraCase) that SRV-23 and
// SRV-24 share; it holds one server slot from its creation to its release.
type imageFix struct {
	key      string
	name     string
	mu       sync.Mutex
	tried    bool
	slot     bool
	srv      *server
	password string // create-time root password (extraCase only)
	err      error

	by       map[string]bool // rows that are done with this fixture
	released bool
}

// imageSet holds the image fixtures, the rows the filter selected
// (SRV-23, SRV-24), the rows started and finished, networks A and B and
// hcloud's image list.
type imageSet struct {
	mu       sync.Mutex
	e        *env
	fixes    map[string]*imageFix
	selected map[string]bool
	started  map[string]bool
	finished map[string]bool

	netOnce    sync.Once
	netA, netB int64
	networks   []int64
	netErr     error

	listOnce sync.Once
	list     []hcloudImage
	listErr  error
}

var imageFixtures = imageSet{
	fixes:    map[string]*imageFix{},
	selected: map[string]bool{},
	started:  map[string]bool{},
	finished: map[string]bool{},
}

// imageRow registers a row sharing the image fixtures as selected (this
// runs in TestE2E's body, before the parallel rows start) and runs it in
// parallel.
func imageRow(f func(t *testing.T)) func(t *testing.T) {
	return func(t *testing.T) {
		row := path.Base(t.Name())
		imageFixtures.mu.Lock()
		imageFixtures.selected[row] = true
		imageFixtures.mu.Unlock()
		parallel(f)(t)
	}
}

// startImageRow waits until every selected image row has started, so no
// fixture holds a slot for a row still waiting for its turn to run, and
// marks the row finished when it ends.
func startImageRow(t *testing.T, e *env) {
	t.Helper()
	row := path.Base(t.Name())
	r := &imageFixtures
	r.mu.Lock()
	r.e = e
	r.started[row] = true
	r.mu.Unlock()
	t.Cleanup(func() { finishImageRow(t, row) })
	if err := waitFor(60*time.Minute, 5*time.Second, func() (bool, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		for s := range r.selected {
			if !r.started[s] {
				return false, nil
			}
		}
		return true, nil
	}); err != nil {
		t.Fatalf("the other image rows did not start: %v", err)
	}
}

// finishImageRow marks the row done with every fixture, including those
// created later by another row.
func finishImageRow(t *testing.T, row string) {
	t.Helper()
	r := &imageFixtures
	r.mu.Lock()
	r.finished[row] = true
	var free []*imageFix
	for _, f := range r.fixes {
		f.by[row] = true
		if r.unusedLocked(f) {
			free = append(free, f)
		}
	}
	r.mu.Unlock()
	for _, f := range free {
		releaseImageFix(t, f)
	}
}

// fixtureFor returns the fixture of the key, registered but not created.
func fixtureFor(key string) *imageFix {
	r := &imageFixtures
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.fixes[key]
	if !ok {
		f = &imageFix{key: key, name: harness.runID + "-img-" + strings.ReplaceAll(key, ".", "-"), by: map[string]bool{}}
		for row := range r.finished {
			f.by[row] = true
		}
		r.fixes[key] = f
	}
	return f
}

// unusedLocked reports whether every selected row is done with f.
func (r *imageSet) unusedLocked(f *imageFix) bool {
	for row := range r.selected {
		if !f.by[row] {
			return false
		}
	}
	return true
}

// doneWithImage marks the row done with the key's fixture and releases the
// fixture when no selected row needs it any more.
func doneWithImage(t *testing.T, row, key string) {
	t.Helper()
	f := fixtureFor(key)
	r := &imageFixtures
	r.mu.Lock()
	f.by[row] = true
	free := r.unusedLocked(f)
	r.mu.Unlock()
	if free {
		releaseImageFix(t, f)
	}
}

// releaseImageFix deletes the fixture's server, if one was made, and gives
// its slot back; a second call does nothing.
func releaseImageFix(t *testing.T, f *imageFix) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.released {
		return
	}
	f.released = true
	if f.tried {
		deleteIfCreated(t, imageFixtures.e, f.name)
	}
	if f.slot {
		serverSlots.release(1)
	}
}

// releaseImageFixtures deletes every image fixture and networks A and B at
// the end of the group, if a filter left out a row using them.
func releaseImageFixtures(t *testing.T) {
	t.Helper()
	r := &imageFixtures
	r.mu.Lock()
	fixes := make([]*imageFix, 0, len(r.fixes))
	for _, f := range r.fixes {
		fixes = append(fixes, f)
	}
	networks := r.networks
	r.networks = nil
	e := r.e
	r.mu.Unlock()
	var wg sync.WaitGroup
	for _, f := range fixes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			releaseImageFix(t, f)
		}()
	}
	wg.Wait()
	for _, id := range networks {
		e.deleteNetwork(t, id)
	}
}

// imageNetworks creates networks A and B once.
func imageNetworks(e *env) (a, b int64, err error) {
	r := &imageFixtures
	r.netOnce.Do(func() {
		var id int64
		if id, r.netErr = e.createNetwork(harness.runID+"-img-a", "10.90.0.0/16", "10.90.1.0/24", nil); r.netErr != nil {
			return
		}
		r.mu.Lock()
		r.networks = append(r.networks, id)
		r.mu.Unlock()
		r.netA = id
		if id, r.netErr = e.createNetwork(harness.runID+"-img-b", "10.91.0.0/16", "10.91.1.0/24", nil); r.netErr != nil {
			return
		}
		r.mu.Lock()
		r.networks = append(r.networks, id)
		r.mu.Unlock()
		r.netB = id
	})
	return r.netA, r.netB, r.netErr
}

// imageUserData is the image servers' user_data: a write_files and a
// runcmd marker, plus extra lines (the users of extraCase).
func imageUserData(extra string) string {
	return "#cloud-config\n" +
		"write_files:\n  - path: " + imageWriteMarker + "\n    content: \"" + imageWriteValue + "\\n\"\n" +
		"runcmd:\n  - [sh, -c, \"echo " + userDataValue + " > " + userDataMarker + "\"]\n" + extra
}

// imageFixture returns the server of the image (or of extraCase), created
// the first time a row asks for it: a cx23 in fsn1 with the run's SSH key,
// network A attached at create and the markers' user_data, ready when SSH
// answers and cloud-init is done. The extraCase server is a rocky-8 without
// SSH key whose user_data adds the user tester with the run's key.
func imageFixture(t *testing.T, key string) *imageFix {
	t.Helper()
	e := sharedEnv(t)
	f := fixtureFor(key)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.released {
		t.Fatalf("image fixture %s already released", key)
	}
	if !f.tried {
		f.tried = true
		serverSlots.acquire(1)
		f.slot = true
		if key == extraCase {
			f.err = f.createExtra(e)
		} else {
			f.err = f.create(e)
		}
	}
	if f.err != nil {
		t.Fatalf("image fixture %s: %v", key, f.err)
	}
	return f
}

func (f *imageFix) create(e *env) error {
	netA, _, err := imageNetworks(e)
	if err != nil {
		return err
	}
	file := filepath.Join(harness.tmp, "image-user-data.yaml")
	if err := os.WriteFile(file, []byte(imageUserData("")), 0o600); err != nil {
		return err
	}
	// The first server of an image downloads and converts it.
	r, err := e.hcloudRun(context.Background(), 45*time.Minute, "server", "create",
		"--name", f.name, "--type", serverType, "--location", serverLocation, "--image", f.key,
		"--ssh-key", e.sshKey, "--user-data-from-file", file, "--network", strconv.FormatInt(netA, 10))
	if err != nil {
		return fmt.Errorf("hcloud server create %s from %s: %v\n%s", f.name, f.key, err, r)
	}
	if f.srv, err = e.describeServer(f.name); err != nil {
		return err
	}
	return e.waitReady(f.srv)
}

func (f *imageFix) createExtra(e *env) error {
	pub, err := os.ReadFile(e.pubKey)
	if err != nil {
		return err
	}
	users := "users:\n  - name: " + extraUser + "\n    ssh_authorized_keys:\n      - " + strings.TrimSpace(string(pub)) + "\n"
	var body struct {
		Server struct {
			ID        int64 `json:"id"`
			PublicNet struct {
				IPv4 struct {
					IP string `json:"ip"`
				} `json:"ipv4"`
			} `json:"public_net"`
		} `json:"server"`
		Action struct {
			ID int64 `json:"id"`
		} `json:"action"`
		RootPassword *string `json:"root_password"`
	}
	status, aerr, err := e.apiDo(context.Background(), http.MethodPost, "/servers", map[string]any{
		"name": f.name, "server_type": serverType, "location": serverLocation, "image": extraImage,
		"user_data": imageUserData(users),
	}, &body)
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("POST /servers %s: status %d %s: %s", f.name, status, aerr.Code, aerr.Message)
	}
	if body.RootPassword == nil || *body.RootPassword == "" {
		return fmt.Errorf("create of %s without SSH keys returned no root_password", f.name)
	}
	f.password = *body.RootPassword
	f.srv = &server{ID: body.Server.ID, Name: f.name, IPv4: body.Server.PublicNet.IPv4.IP}
	if err := e.waitAction(body.Action.ID, 45*time.Minute); err != nil {
		return err
	}
	var last result
	if err := waitFor(15*time.Minute, 5*time.Second, func() (bool, error) {
		r, err := e.sshAs(extraUser, f.srv, 30*time.Second, "true")
		last = r
		return err == nil && r.ExitCode == 0, nil
	}); err != nil {
		return fmt.Errorf("ssh %s@%s with the run's key: %w\n%s", extraUser, f.srv.IPv4, err, last)
	}
	if r, err := e.sshAs(extraUser, f.srv, 15*time.Minute, "cloud-init status --wait >/dev/null; true"); err != nil {
		return fmt.Errorf("cloud-init status --wait on %s: %v\n%s", f.name, err, r)
	}
	return nil
}

// sshAs runs a command as user on the server with the run's key.
func (e *env) sshAs(user string, s *server, timeout time.Duration, remote string) (result, error) {
	return command(context.Background(), timeout, nil, "", harness.ssh,
		"-i", e.key,
		"-o", "IdentitiesOnly=yes",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile="+e.known,
		"-o", "ConnectTimeout=10",
		"-o", "LogLevel=ERROR",
		user+"@"+s.IPv4, remote)
}

// hcloudImage is a system image as `hcloud image list -o json` prints it.
type hcloudImage struct {
	Name string `json:"name"`
}

// systemImages are the system images hcloud lists, fetched once.
func systemImages(t *testing.T, e *env) []hcloudImage {
	t.Helper()
	r := &imageFixtures
	r.listOnce.Do(func() {
		res, err := e.hcloudRun(context.Background(), 2*time.Minute, "image", "list", "--type", "system", "-o", "json")
		if err != nil {
			r.listErr = fmt.Errorf("hcloud image list --type system -o json: %v\n%s", err, res)
			return
		}
		if err := json.Unmarshal([]byte(res.Stdout), &r.list); err != nil {
			r.listErr = fmt.Errorf("hcloud image list --type system -o json: %w\n%s", err, res.Stdout)
		}
	})
	if r.listErr != nil {
		t.Fatal(r.listErr)
	}
	return r.list
}

// imageCases are the names of the listed system images plus extraCase.
func imageCases(t *testing.T, e *env) []string {
	t.Helper()
	var keys []string
	for _, img := range systemImages(t, e) {
		if img.Name != "" {
			keys = append(keys, img.Name)
		}
	}
	return append(keys, extraCase)
}

// runImageCases runs one subtest per key at once; each waits for its
// fixture's slot. The row is done with a key's fixture when its subtest
// ends, or at once if the filter left the subtest out.
func runImageCases(t *testing.T, keys []string, f func(t *testing.T, key string)) {
	t.Helper()
	row := path.Base(t.Name())
	var wg sync.WaitGroup
	for _, key := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer doneWithImage(t, row, key)
			t.Run(key, logged(func(t *testing.T) { f(t, key) }))
		}()
	}
	wg.Wait()
}

// dateRe finds the year of a date in any of hcloud's date layouts.
var dateRe = regexp.MustCompile(`\b20\d{2}\b`)

// testSRV23: hcloud lists all sixteen system images, fedora-43 with a date
// in its deprecated column; a server from each boots with hostname, key,
// user_data, internet, private networks at create and later, an automounted
// volume, a password reset and a clean cloud-init; the keyless extra server
// takes its create-time password.
func testSRV23(t *testing.T) {
	e := sharedEnv(t)
	startImageRow(t, e)

	listed := map[string]bool{}
	for _, img := range systemImages(t, e) {
		listed[img.Name] = true
	}
	for _, name := range imageNames {
		if !listed[name] {
			t.Errorf("hcloud image list --type system does not list %s", name)
		}
	}
	r, err := e.hcloudRun(context.Background(), 2*time.Minute, "image", "list", "--type", "system", "-o", "noheader", "-o", "columns=name,deprecated")
	if err != nil {
		t.Errorf("hcloud image list --type system -o columns=name,deprecated: %v\n%s", err, r)
	} else {
		var deprecated string
		found := false
		for _, line := range strings.Split(r.Stdout, "\n") {
			name, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
			if name == "fedora-43" {
				found, deprecated = true, strings.TrimSpace(rest)
			}
		}
		if !found || !dateRe.MatchString(deprecated) {
			t.Errorf("fedora-43's deprecated column %q (listed %v), want a date\n%s", deprecated, found, r.Stdout)
		}
	}

	runImageCases(t, imageCases(t, e), func(t *testing.T, key string) {
		f := imageFixture(t, key)
		if key == extraCase {
			var last result
			if err := waitFor(15*time.Minute, 5*time.Second, func() (bool, error) {
				r, err := sshPassword(f.srv, f.password, 30*time.Second, "id -un")
				last = r
				return err == nil && strings.TrimSpace(r.Stdout) == "root", nil
			}); err != nil {
				t.Errorf("ssh root@%s with the create-time password: %v\n%s", f.srv.IPv4, err, last)
			}
			return
		}
		srv23Image(t, e, f.srv)
	})
}

// srv23Image checks one image's server.
func srv23Image(t *testing.T, e *env, srv *server) {
	t.Helper()
	netA, netB, err := imageNetworks(e)
	if err != nil {
		t.Fatal(err)
	}

	r, err := e.ssh(srv, time.Minute, "", "id -un")
	if err != nil || strings.TrimSpace(r.Stdout) != "root" {
		t.Fatalf("ssh root@%s with the key: %v\n%s", srv.IPv4, err, r)
	}
	r, err = e.ssh(srv, time.Minute, "", "cat /proc/sys/kernel/hostname")
	if err != nil || strings.TrimSpace(r.Stdout) != srv.Name {
		t.Errorf("hostname: %v\n%s\nwant %q", err, r, srv.Name)
	}
	for marker, want := range map[string]string{userDataMarker: userDataValue, imageWriteMarker: imageWriteValue} {
		r, err := e.ssh(srv, time.Minute, "", "cat "+marker)
		if err != nil || strings.TrimSpace(r.Stdout) != want {
			t.Errorf("user_data marker %s: %v\n%s\nwant %q", marker, err, r, want)
		}
	}

	fetch := "if command -v curl >/dev/null; then curl -sS -o /dev/null -m 15 " + imageProbeURL +
		"; else wget -q -O /dev/null -T 15 " + imageProbeURL + "; fi"
	var last result
	if err := waitFor(3*time.Minute, 5*time.Second, func() (bool, error) {
		r, err := e.ssh(srv, time.Minute, "", fetch)
		last = r
		return err == nil && r.ExitCode == 0, nil
	}); err != nil {
		t.Errorf("%s cannot fetch %s: %v\n%s", srv.Name, imageProbeURL, err, last)
	}

	if _, err := e.loadMember(srv, netA); err != nil {
		t.Errorf("network A attached at create: %v", err)
	}

	r, err = e.ssh(srv, 15*time.Minute, "", "cloud-init status --wait")
	if err != nil || r.ExitCode != 0 {
		t.Errorf("cloud-init status --wait: %v\n%s", err, r)
	}

	vol := e.createVolume(t, map[string]any{
		"name": srv.Name + "-vol", "size": 10, "location": serverLocation, "format": "ext4",
	})
	e.attachVolume(t, vol, srv, true)
	mount := fmt.Sprintf("/mnt/HC_Volume_%d", vol)
	if err := waitFor(5*time.Minute, 5*time.Second, func() (bool, error) {
		r, err := e.ssh(srv, 30*time.Second, "", "findmnt -n -o FSTYPE "+mount)
		last = r
		return err == nil && strings.TrimSpace(r.Stdout) != "", nil
	}); err != nil {
		t.Errorf("%s not mounted: %v\n%s", mount, err, last)
	}

	if err := e.postAction(fmt.Sprintf("/servers/%d/actions/attach_to_network", srv.ID),
		map[string]any{"network": netB}, 10*time.Minute); err != nil {
		t.Errorf("attach to network B: %v", err)
	} else if _, err := e.loadMember(srv, netB); err != nil {
		t.Errorf("network B attached while running: %v", err)
	}

	var body struct {
		RootPassword string `json:"root_password"`
	}
	if err := e.postActionInto(fmt.Sprintf("/servers/%d/actions/reset_password", srv.ID), nil, &body, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if body.RootPassword == "" {
		t.Fatal("reset_password returned no root_password")
	}
	r, err = sshPassword(srv, body.RootPassword, time.Minute, "id -un")
	if err != nil || strings.TrimSpace(r.Stdout) != "root" {
		t.Errorf("ssh root@%s with the new password: %v\n%s", srv.IPv4, err, r)
	}
}

// passwdEntry is a line of `getent passwd`.
type passwdEntry struct {
	name string
	uid  int
}

func getentPasswd(out string) []passwdEntry {
	var entries []passwdEntry
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimSpace(line), ":")
		if len(fields) < 3 {
			continue
		}
		uid, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		entries = append(entries, passwdEntry{name: fields[0], uid: uid})
	}
	return entries
}

// testSRV24: no image's server has a default user or any account with a
// UID of 1000 to 59999; on the extra server the user from user_data exists
// and logs in with the run's key, and no default user does.
func testSRV24(t *testing.T) {
	e := sharedEnv(t)
	startImageRow(t, e)
	runImageCases(t, imageCases(t, e), func(t *testing.T, key string) {
		f := imageFixture(t, key)
		var r result
		var err error
		if key == extraCase {
			r, err = e.sshAs(extraUser, f.srv, time.Minute, "id -un; getent passwd")
			if err != nil {
				t.Fatalf("ssh %s@%s with the run's key: %v\n%s", extraUser, f.srv.IPv4, err, r)
			}
			if first, _, _ := strings.Cut(r.Stdout, "\n"); strings.TrimSpace(first) != extraUser {
				t.Errorf("login as %s: id -un %q", extraUser, strings.TrimSpace(first))
			}
		} else {
			r, err = e.ssh(f.srv, time.Minute, "", "getent passwd")
			if err != nil {
				t.Fatalf("getent passwd on %s: %v\n%s", f.srv.Name, err, r)
			}
		}
		entries := getentPasswd(r.Stdout)
		hasExtra := false
		for _, u := range entries {
			if key == extraCase && u.name == extraUser {
				hasExtra = true
				continue
			}
			if slices.Contains(defaultUsers, u.name) {
				t.Errorf("default user %s exists (uid %d)", u.name, u.uid)
			} else if u.uid >= 1000 && u.uid <= 59999 {
				t.Errorf("account %s with uid %d exists", u.name, u.uid)
			}
		}
		if key == extraCase && !hasExtra {
			t.Errorf("user %s from user_data is missing\n%s", extraUser, r.Stdout)
		}
	})
}
