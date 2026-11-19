//go:build e2e

package e2e

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// lbFixtureUsers is the number of rows using the shared load balancer
// fixture (LB-02, LB-03, LB-04, LB-05, CERT-04).
const lbFixtureUsers = 5

// lbUserData serves the guest's hostname on port 80 and, from a second
// web server LB-05 stops and starts, on port 8081.
const lbUserData = "#cloud-config\nruncmd:\n" +
	"  - [sh, -c, \"mkdir -p /srv/www80 /srv/www8081 && hostname > /srv/www80/index.html && hostname > /srv/www8081/index.html\"]\n" +
	"  - [systemd-run, --unit=hlab-lb-80, python3, -m, http.server, \"80\", --directory, /srv/www80]\n" +
	"  - [systemd-run, --unit=hlab-lb-8081, python3, -m, http.server, \"8081\", --directory, /srv/www8081]\n"

// startWeb8081 starts the second web server again after LB-05 stopped it
// (a stopped transient unit is gone).
const startWeb8081 = "systemctl is-active --quiet hlab-lb-8081 || " +
	"systemd-run --unit=hlab-lb-8081 python3 -m http.server 8081 --directory /srv/www8081"

// lbPorts are the listen ports of the fixture's services.
var lbPorts = []int{8080, 8081, 8000, 443}

// lbFixture is a per-run zone, a network with two web servers and one load
// balancer in it with private targets and the services
//   - tcp 8080 to 80, tcp 8081 to 8081 (LB-05)
//   - http 8000 to 80 with sticky sessions
//   - https 443 to 80 with a managed certificate for www.<zone> and
//     redirect_http
//
// and an A record www.<zone> pointing at the load balancer.
type lbFixture struct {
	once sync.Once
	e    *env
	err  error

	zone    string // the run's zone, "<run>-lb.test"
	zoneID  int64
	network int64
	names   []string // server names
	a, b    *server
	certID  int64
	lbID    int64
	ip      string // the load balancer's public IPv4
	slots   int

	mu       sync.Mutex
	done     int
	released bool
}

var lbFix lbFixture

// sharedLB returns the load balancer fixture, created once; when every row
// using it has ended, it is deleted (or at the end of TestE2E when a filter
// runs only some of them).
func sharedLB(t *testing.T) *lbFixture {
	t.Helper()
	e := sharedEnv(t)
	f := &lbFix
	f.once.Do(func() {
		f.e = e
		f.slots = serverSlots.acquire(2)
		f.err = f.create()
	})
	t.Cleanup(func() {
		f.mu.Lock()
		f.done++
		last := f.done == lbFixtureUsers
		f.mu.Unlock()
		if last {
			releaseLBFixture(t)
		}
	})
	if f.err != nil {
		t.Fatalf("load balancer fixture: %v", f.err)
	}
	return f
}

// postCreated posts body and decodes a 201 answer into out.
func (e *env) postCreated(path string, body, out any) error {
	status, aerr, err := e.apiDo(context.Background(), http.MethodPost, path, body, out)
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("POST %s: status %d %s: %s", path, status, aerr.Code, aerr.Message)
	}
	return nil
}

type actionRef struct {
	Action *struct {
		ID int64 `json:"id"`
	} `json:"action"`
}

func (f *lbFixture) create() error {
	e := f.e
	f.zone = zoneName("lb")
	var zone struct {
		Zone struct {
			ID int64 `json:"id"`
		} `json:"zone"`
		actionRef
	}
	if err := e.postCreated("/zones", map[string]any{"name": f.zone, "mode": "primary"}, &zone); err != nil {
		return err
	}
	f.zoneID = zone.Zone.ID
	if zone.Action != nil {
		if err := e.waitAction(zone.Action.ID, 5*time.Minute); err != nil {
			return fmt.Errorf("create zone %s: %w", f.zone, err)
		}
	}

	var err error
	if f.network, err = e.createNetwork(harness.runID+"-lbnet", "10.70.0.0/16", "10.70.1.0/24", nil); err != nil {
		return err
	}

	file := filepath.Join(harness.tmp, "lb-user-data.yaml")
	if err := os.WriteFile(file, []byte(lbUserData), 0o600); err != nil {
		return err
	}
	srvs := make([]*server, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, suffix := range []string{"lb-a", "lb-b"} {
		name := harness.runID + "-" + suffix
		f.names = append(f.names, name)
		wg.Add(1)
		go func() {
			defer wg.Done()
			srvs[i], errs[i] = e.createServerImage(name, serverImage,
				"--user-data-from-file", file, "--network", strconv.FormatInt(f.network, 10))
			if errs[i] == nil {
				errs[i] = e.waitReady(srvs[i])
			}
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}
	f.a, f.b = srvs[0], srvs[1]

	var cert struct {
		Certificate struct {
			ID int64 `json:"id"`
		} `json:"certificate"`
		actionRef
	}
	if err := e.postCreated("/certificates", map[string]any{
		"name": harness.runID + "-www", "type": "managed", "domain_names": []string{"www." + f.zone},
	}, &cert); err != nil {
		return err
	}
	f.certID = cert.Certificate.ID
	if cert.Action == nil {
		return errors.New("managed certificate create returned no action")
	}
	if err := e.waitAction(cert.Action.ID, 5*time.Minute); err != nil {
		return fmt.Errorf("issue the managed certificate: %w", err)
	}

	target := func(s *server) map[string]any {
		return map[string]any{"type": "server", "server": map[string]any{"id": s.ID}, "use_private_ip": true}
	}
	tcp := func(listen, dest int) map[string]any {
		return map[string]any{"protocol": "tcp", "listen_port": listen, "destination_port": dest, "proxyprotocol": false}
	}
	sticky := tcp(8000, 80)
	sticky["protocol"] = "http"
	sticky["http"] = map[string]any{"sticky_sessions": true}
	https := tcp(443, 80)
	https["protocol"] = "https"
	https["http"] = map[string]any{"certificates": []int64{f.certID}, "redirect_http": true}
	var lb struct {
		LoadBalancer struct {
			ID int64 `json:"id"`
		} `json:"load_balancer"`
		actionRef
	}
	if err := e.postCreated("/load_balancers", map[string]any{
		"name": harness.runID + "-lb", "load_balancer_type": "lb11", "location": serverLocation,
		"network": f.network, "algorithm": map[string]any{"type": "round_robin"},
		"targets":  []any{target(f.a), target(f.b)},
		"services": []any{tcp(8080, 80), tcp(8081, 8081), sticky, https},
	}, &lb); err != nil {
		return err
	}
	f.lbID = lb.LoadBalancer.ID
	if lb.Action == nil {
		return errors.New("load balancer create returned no action")
	}
	if err := e.waitAction(lb.Action.ID, 10*time.Minute); err != nil {
		return fmt.Errorf("create load balancer: %w", err)
	}
	var got struct {
		LoadBalancer struct {
			PublicNet struct {
				IPv4 struct {
					IP string `json:"ip"`
				} `json:"ipv4"`
			} `json:"public_net"`
		} `json:"load_balancer"`
	}
	if status, aerr, err := e.apiDo(context.Background(), http.MethodGet, fmt.Sprintf("/load_balancers/%d", f.lbID), nil, &got); err != nil || status != http.StatusOK {
		return fmt.Errorf("GET /load_balancers/%d: status %d %s: %v", f.lbID, status, aerr.Code, err)
	}
	f.ip = got.LoadBalancer.PublicNet.IPv4.IP

	var rr actionRef
	if err := e.postCreated(fmt.Sprintf("/zones/%d/rrsets", f.zoneID), map[string]any{
		"name": "www", "type": "A", "records": []map[string]any{{"value": f.ip}},
	}, &rr); err != nil {
		return err
	}
	if rr.Action != nil {
		if err := e.waitAction(rr.Action.ID, 5*time.Minute); err != nil {
			return fmt.Errorf("create www record: %w", err)
		}
	}

	// Every target is healthy on every service before the rows start.
	return waitFor(5*time.Minute, 5*time.Second, func() (bool, error) {
		h, err := e.lbHealth(f.lbID)
		if err != nil {
			return false, nil
		}
		for _, s := range []*server{f.a, f.b} {
			for _, p := range lbPorts {
				if h[s.ID][p] != "healthy" {
					return false, nil
				}
			}
		}
		return true, nil
	})
}

// releaseLBFixture deletes the fixture once and gives back its slots.
func releaseLBFixture(t *testing.T) {
	t.Helper()
	f := &lbFix
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.released || f.e == nil {
		return
	}
	f.released = true
	e := f.e
	if f.lbID != 0 {
		var b actionRef
		status, aerr, err := e.apiDo(context.Background(), http.MethodDelete, fmt.Sprintf("/load_balancers/%d", f.lbID), nil, &b)
		switch {
		case err != nil:
			t.Errorf("DELETE load balancer %d: %v", f.lbID, err)
		case status >= 300 && status != http.StatusNotFound:
			t.Errorf("DELETE load balancer %d: status %d %s", f.lbID, status, aerr.Code)
		case b.Action != nil:
			if err := e.waitAction(b.Action.ID, 5*time.Minute); err != nil {
				t.Errorf("delete load balancer %d: %v", f.lbID, err)
			}
		}
	}
	if f.certID != 0 {
		if status, aerr, err := e.apiDo(context.Background(), http.MethodDelete, fmt.Sprintf("/certificates/%d", f.certID), nil, nil); err != nil || (status >= 300 && status != http.StatusNotFound) {
			t.Errorf("DELETE certificate %d: status %d %s: %v", f.certID, status, aerr.Code, err)
		}
	}
	var wg sync.WaitGroup
	for _, name := range f.names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			deleteIfCreated(t, e, name)
		}()
	}
	wg.Wait()
	if f.network != 0 {
		e.deleteNetwork(t, f.network)
	}
	if f.zoneID != 0 {
		if err := e.deleteZone(f.zoneID); err != nil {
			t.Errorf("delete zone %s: %v", f.zone, err)
		}
	}
	serverSlots.release(f.slots)
}

// lbHealth returns the health status per server target and listen port.
func (e *env) lbHealth(id int64) (map[int64]map[int]string, error) {
	var body struct {
		LoadBalancer struct {
			Targets []struct {
				Server *struct {
					ID int64 `json:"id"`
				} `json:"server"`
				HealthStatus []struct {
					ListenPort int    `json:"listen_port"`
					Status     string `json:"status"`
				} `json:"health_status"`
			} `json:"targets"`
		} `json:"load_balancer"`
	}
	status, aerr, err := e.apiDo(context.Background(), http.MethodGet, fmt.Sprintf("/load_balancers/%d", id), nil, &body)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("GET /load_balancers/%d: status %d %s", id, status, aerr.Code)
	}
	out := map[int64]map[int]string{}
	for _, tg := range body.LoadBalancer.Targets {
		if tg.Server == nil {
			continue
		}
		m := map[int]string{}
		for _, h := range tg.HealthStatus {
			m[h.ListenPort] = h.Status
		}
		out[tg.Server.ID] = m
	}
	return out, nil
}

// curlHost runs curl.exe on the host with args.
func curlHost(args ...string) (result, error) {
	return command(context.Background(), time.Minute, nil, "", "curl.exe", append([]string{"-sS", "--max-time", "20"}, args...)...)
}

// answers fetches url n times from the host, one connection each, and
// returns the bodies (the answering server's hostname).
func answers(t *testing.T, n int, url string, extra ...string) []string {
	t.Helper()
	var out []string
	for i := range n {
		r, err := curlHost(append(extra, url)...)
		if err != nil {
			t.Fatalf("request %d to %s: %v\n%s", i+1, url, err, r)
		}
		out = append(out, strings.TrimSpace(r.Stdout))
	}
	return out
}

// testLB02: 20 requests to the tcp service are answered by both private
// targets in turn.
func testLB02(t *testing.T) {
	f := sharedLB(t)
	got := answers(t, 20, "http://"+f.ip+":8080/")
	seen := map[string]int{}
	for i, name := range got {
		seen[name]++
		if i > 0 && got[i-1] == name {
			t.Errorf("requests %d and %d both answered by %q, want the targets in turn: %v", i, i+1, name, got)
			break
		}
	}
	if seen[f.a.Name] == 0 || seen[f.b.Name] == 0 || len(seen) != 2 {
		t.Errorf("answers %v, want both %s and %s", seen, f.a.Name, f.b.Name)
	}
}

// testLB03: sticky sessions on the http service, and the redirect of
// http://www.<zone>/ to https.
func testLB03(t *testing.T) {
	f := sharedLB(t)
	jar := filepath.Join(t.TempDir(), "cookies.txt")
	got := answers(t, 10, "http://"+f.ip+":8000/", "-c", jar, "-b", jar)
	for _, name := range got {
		if name != got[0] {
			t.Errorf("answers with the cookie kept %v, want the same target every time", got)
			break
		}
	}
	if data, err := os.ReadFile(jar); err != nil || !strings.Contains(string(data), "HCLBSTICKY") {
		t.Errorf("cookie jar %q (%v), want the HCLBSTICKY cookie", data, err)
	}

	url := "http://www." + f.zone + "/"
	want := "301 https://www." + f.zone + "/"
	var last string
	if err := waitFor(dnsWait, 3*time.Second, func() (bool, error) {
		r, _ := curlHost("-o", "NUL", "-w", "%{http_code} %{redirect_url}", url)
		last = strings.TrimSpace(r.Stdout)
		return last == want, nil
	}); err != nil {
		t.Errorf("GET %s answered %q, want %q: %v", url, last, want, err)
	}
}

// testLB04: https://www.<zone>/ answers through the load balancer with the
// managed certificate, issued by the hlab CA; curl with the exported CA
// succeeds in WSL2.
func testLB04(t *testing.T) {
	f := sharedLB(t)
	_, w := sharedWSL(t)
	host := "www." + f.zone

	file := filepath.Join(t.TempDir(), "hlab-ca.pem")
	if r, err := runHlab(context.Background(), time.Minute, "ca", "export", file); err != nil {
		t.Fatalf("hlab ca export: %v\n%s", err, r)
	}
	caPEM, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read exported CA: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatalf("exported CA is no PEM certificate: %q", caPEM)
	}

	var leaf *x509.Certificate
	var lastErr error
	if err := waitFor(dnsWait, 3*time.Second, func() (bool, error) {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", net.JoinHostPort(host, "443"),
			&tls.Config{ServerName: host, RootCAs: pool, MinVersion: tls.VersionTLS12})
		if err != nil {
			lastErr = err
			return false, nil
		}
		defer func() { _ = conn.Close() }()
		leaf = conn.ConnectionState().PeerCertificates[0]
		return true, nil
	}); err != nil {
		t.Fatalf("TLS to %s:443 verified against the hlab CA: %v (last: %v)", host, err, lastErr)
	}
	if leaf.Subject.CommonName != host {
		t.Errorf("certificate subject %q, want %s", leaf.Subject.CommonName, host)
	}

	r, err := curlHost("-k", "https://"+host+"/")
	if err != nil {
		t.Errorf("GET https://%s/: %v\n%s", host, err, r)
	} else if name := strings.TrimSpace(r.Stdout); name != f.a.Name && name != f.b.Name {
		t.Errorf("GET https://%s/ answered %q, want one of the targets", host, name)
	}

	script := "set -e\nd=$(mktemp -d)\ncat > \"$d/ca.pem\" <<'HLAB_CA_EOF'\n" +
		strings.TrimSpace(strings.ReplaceAll(string(caPEM), "\r", "")) +
		"\nHLAB_CA_EOF\ncurl -sS --max-time 20 --cacert \"$d/ca.pem\" https://" + host + "/; rc=$?\nrm -rf \"$d\"\nexit $rc\n"
	if r, err := w.run(context.Background(), 2*time.Minute, nil, script); err != nil {
		t.Errorf("curl --cacert https://%s/ in %s: %v\n%s", host, w.distro, err, r)
	} else if name := strings.TrimSpace(r.Stdout); name != f.a.Name && name != f.b.Name {
		t.Errorf("curl in %s answered %q, want one of the targets", w.distro, name)
	}
}

// testLB05: a target whose web server stops turns unhealthy and gets no
// requests; started again it turns healthy.
func testLB05(t *testing.T) {
	f := sharedLB(t)
	e := f.e
	t.Cleanup(func() {
		if r, err := e.ssh(f.a, time.Minute, "", startWeb8081); err != nil {
			t.Errorf("cleanup: start the web server on %s: %v\n%s", f.a.Name, err, r)
		}
	})
	waitHealth := func(want string) {
		t.Helper()
		var last string
		if err := waitFor(3*time.Minute, 3*time.Second, func() (bool, error) {
			h, err := e.lbHealth(f.lbID)
			if err != nil {
				return false, nil
			}
			last = h[f.a.ID][8081]
			return last == want, nil
		}); err != nil {
			t.Fatalf("health of %s on 8081 is %q, want %s: %v", f.a.Name, last, want, err)
		}
	}

	if r, err := e.ssh(f.a, time.Minute, "", "systemctl stop hlab-lb-8081"); err != nil {
		t.Fatalf("stop the web server on %s: %v\n%s", f.a.Name, err, r)
	}
	waitHealth("unhealthy")
	for i, name := range answers(t, 10, "http://"+f.ip+":8081/") {
		if name != f.b.Name {
			t.Errorf("request %d answered by %q, want %s while %s is unhealthy", i+1, name, f.b.Name, f.a.Name)
		}
	}

	if r, err := e.ssh(f.a, time.Minute, "", startWeb8081); err != nil {
		t.Fatalf("start the web server on %s: %v\n%s", f.a.Name, err, r)
	}
	waitHealth("healthy")
}
