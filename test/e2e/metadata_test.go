//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const metadataURL = "http://169.254.169.254"

// metadataGet fetches a path of the metadata service from the server itself.
func (e *env) metadataGet(srv *server, path string) (string, error) {
	r, err := e.ssh(srv, 2*time.Minute, "", "curl -sS -f --max-time 10 "+metadataURL+path)
	if err != nil || r.ExitCode != 0 {
		return "", fmt.Errorf("curl %s from %s: %v\n%s", path, srv.Name, err, r)
	}
	return r.Stdout, nil
}

// yamlTopLevel reads the top-level scalar keys of a YAML mapping; quotes
// around a value are removed.
func yamlTopLevel(doc string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(doc, "\n") {
		if line == "" || line[0] == ' ' || line[0] == '-' || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		m[k] = v
	}
	return m
}

// testMETA01: the shared server reads its own hostname, ID, public IPv4,
// region, availability zone, keys and user_data from the metadata service.
func testMETA01(t *testing.T) {
	t.Run("spoof", parallel(testMETA01Spoof))

	e, srv := sharedServer(t)

	// The server object has no datacenter since Hetzner removed them; the
	// availability zone is the location's datacenter name.
	var info struct {
		Server struct {
			Location struct {
				Name        string `json:"name"`
				NetworkZone string `json:"network_zone"`
			} `json:"location"`
		} `json:"server"`
	}
	status, aerr, err := e.apiDo(context.Background(), http.MethodGet, fmt.Sprintf("/servers/%d", srv.ID), nil, &info)
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET /servers/%d: %v (status %d %s)", srv.ID, err, status, aerr.Code)
	}
	pub, err := os.ReadFile(e.pubKey)
	if err != nil {
		t.Fatal(err)
	}
	keyFields := strings.Fields(string(pub))
	if len(keyFields) < 2 {
		t.Fatalf("public key file %s has no key", e.pubKey)
	}
	keyBlob := keyFields[1]
	userData, err := os.ReadFile(e.userData)
	if err != nil {
		t.Fatal(err)
	}

	doc, err := e.metadataGet(srv, "/hetzner/v1/metadata")
	if err != nil {
		t.Fatal(err)
	}
	top := yamlTopLevel(doc)
	zone := top["availability-zone"]
	if !regexp.MustCompile(`^` + regexp.QuoteMeta(info.Server.Location.Name) + `-dc[0-9]+$`).MatchString(zone) {
		t.Errorf("metadata availability-zone %q, want %s-dc<n>\n%s", zone, info.Server.Location.Name, doc)
	}
	want := []struct{ key, value string }{
		{"hostname", srv.Name},
		{"instance-id", strconv.FormatInt(srv.ID, 10)},
		{"public-ipv4", srv.IPv4},
		{"region", info.Server.Location.NetworkZone},
		{"availability-zone", zone},
	}
	for _, w := range want {
		if got := top[w.key]; got != w.value {
			t.Errorf("metadata %s: %q, want %q\n%s", w.key, got, w.value, doc)
		}
		got, err := e.metadataGet(srv, "/hetzner/v1/metadata/"+w.key)
		if err != nil {
			t.Error(err)
			continue
		}
		if strings.TrimSpace(got) != w.value {
			t.Errorf("/hetzner/v1/metadata/%s: %q, want %q", w.key, strings.TrimSpace(got), w.value)
		}
	}

	if _, ok := top["public-keys"]; !ok || !strings.Contains(doc, keyBlob) {
		t.Errorf("metadata has no public-keys with the run's key:\n%s", doc)
	}
	if got, err := e.metadataGet(srv, "/hetzner/v1/metadata/public-keys"); err != nil {
		t.Error(err)
	} else if !strings.Contains(got, keyBlob) {
		t.Errorf("/hetzner/v1/metadata/public-keys lacks the run's key:\n%s", got)
	}

	if got, err := e.metadataGet(srv, "/hetzner/v1/userdata"); err != nil {
		t.Error(err)
	} else if got != string(userData) {
		t.Errorf("/hetzner/v1/userdata:\n%q\nwant\n%q", got, userData)
	}
}

// testMETA01Spoof: a second server that takes the shared server's IPv4 as an
// extra source address gets no answer from the metadata service with it.
func testMETA01Spoof(t *testing.T) {
	e, shared := sharedServer(t)
	takeServerSlots(t, 1)
	other := ownServer(t, e, "meta01")

	// The service answers the second server under its own address, so a
	// missing answer below is the binding and not a dead service.
	got, err := e.metadataGet(other, "/hetzner/v1/metadata/hostname")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got) != other.Name {
		t.Fatalf("own hostname %q, want %q", strings.TrimSpace(got), other.Name)
	}

	// The address is held only for the two requests; arp_announce=2 keeps
	// the borrowed address out of the second server's ARP requests.
	script := fmt.Sprintf(`set -u
sysctl -q -w net.ipv4.conf.eth0.arp_announce=2 || exit 90
ip addr add %[1]s/32 dev eth0 || exit 91
trap 'ip addr del %[1]s/32 dev eth0' EXIT
for p in metadata/hostname userdata; do
  code=$(curl -s -o /tmp/hlab-meta01-spoof -w '%%{http_code}' --max-time 10 --interface %[1]s %[2]s/hetzner/v1/$p)
  echo "$p $code"
  cat /tmp/hlab-meta01-spoof 2>/dev/null
  rm -f /tmp/hlab-meta01-spoof
  echo
done
`, shared.IPv4, metadataURL)
	r, err := e.ssh(other, 3*time.Minute, "", script)
	if err != nil {
		t.Fatalf("spoofed requests from %s: %v\n%s", other.Name, err, r)
	}
	for _, p := range []string{"metadata/hostname", "userdata"} {
		if !strings.Contains(r.Stdout, p+" 000\n") {
			t.Errorf("request for %s from the borrowed address %s got an answer:\n%s", p, shared.IPv4, r.Stdout)
		}
	}
	if strings.Contains(r.Stdout, shared.Name) {
		t.Errorf("the shared server's metadata leaked:\n%s", r.Stdout)
	}
}
