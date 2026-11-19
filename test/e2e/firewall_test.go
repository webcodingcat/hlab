//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// httpUserData starts a web server on port 80 of the guest.
const httpUserData = "#cloud-config\nruncmd:\n" +
	"  - [systemd-run, --unit=hlab-e2e-http, python3, -m, http.server, \"80\"]\n"

// fwProbeHost is the internet host FW-03 fetches from; it serves both http
// and https.
const fwProbeHost = "example.com"

// firewallServer creates the row's server with a web server on port 80 and
// waits until port 80 answers from the host.
func firewallServer(t *testing.T, e *env, suffix string) *server {
	t.Helper()
	file := filepath.Join(t.TempDir(), "http-user-data.yaml")
	if err := os.WriteFile(file, []byte(httpUserData), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := ownServerImage(t, e, suffix, serverImage, "--user-data-from-file", file)
	if err := waitFor(3*time.Minute, 3*time.Second, func() (bool, error) {
		return dialOK(srv.IPv4, 80), nil
	}); err != nil {
		t.Fatalf("port 80 on %s does not answer before any firewall: %v", srv.IPv4, err)
	}
	return srv
}

// dialOK reports whether a TCP connect from the host to ip:port succeeds
// within a short deadline.
func dialOK(ip string, port int) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort(ip, fmt.Sprint(port)), 5*time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// createFirewall creates a firewall with rules, waits for its actions and
// deletes it when the row ends. Created before the row's server, it is
// deleted after the server, which takes it out of applied_to.
func (e *env) createFirewall(t *testing.T, suffix string, rules []map[string]any) int64 {
	t.Helper()
	var body struct {
		Firewall struct {
			ID int64 `json:"id"`
		} `json:"firewall"`
		Actions []struct {
			ID int64 `json:"id"`
		} `json:"actions"`
	}
	e.post(t, "/firewalls", map[string]any{"name": harness.runID + "-" + suffix, "rules": rules}, &body)
	id := body.Firewall.ID
	t.Cleanup(func() {
		path := fmt.Sprintf("/firewalls/%d", id)
		err := waitFor(5*time.Minute, 3*time.Second, func() (bool, error) {
			status, _, err := e.apiDo(context.Background(), http.MethodDelete, path, nil, nil)
			return err == nil && (status == http.StatusNoContent || status == http.StatusNotFound), nil
		})
		if err != nil {
			t.Errorf("DELETE %s: %v", path, err)
		}
	})
	for _, a := range body.Actions {
		if err := e.waitAction(a.ID, 5*time.Minute); err != nil {
			t.Fatalf("POST /firewalls: %v", err)
		}
	}
	return id
}

// firewallAction posts a firewall action answering {actions} and waits
// until all of them end in success.
func (e *env) firewallAction(t *testing.T, id int64, action string, body any) {
	t.Helper()
	path := fmt.Sprintf("/firewalls/%d/actions/%s", id, action)
	var b struct {
		Actions []struct {
			ID int64 `json:"id"`
		} `json:"actions"`
	}
	e.post(t, path, body, &b)
	if len(b.Actions) == 0 {
		t.Fatalf("POST %s returned no actions", path)
	}
	for _, a := range b.Actions {
		if err := e.waitAction(a.ID, 5*time.Minute); err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
	}
}

func serverResource(srv *server) []map[string]any {
	return []map[string]any{{"type": "server", "server": map[string]any{"id": srv.ID}}}
}

func sshInRule() map[string]any {
	return map[string]any{
		"direction": "in", "protocol": "tcp", "port": "22", "source_ips": []string{"0.0.0.0/0"},
	}
}

// testFW02: with a firewall allowing only tcp 22 in, SSH from the host
// works and port 80 does not connect; after remove_from_resources port 80
// answers again.
func testFW02(t *testing.T) {
	e := sharedEnv(t)
	takeServerSlots(t, 1)
	fw := e.createFirewall(t, "fw02", []map[string]any{sshInRule()})
	srv := firewallServer(t, e, "fw02")

	e.firewallAction(t, fw, "apply_to_resources", map[string]any{"apply_to": serverResource(srv)})
	e.waitSSH(t, srv, time.Minute)
	if dialOK(srv.IPv4, 80) {
		t.Errorf("port 80 on %s connects with a firewall allowing only tcp 22 in", srv.IPv4)
	}

	e.firewallAction(t, fw, "remove_from_resources", map[string]any{"remove_from": serverResource(srv)})
	if err := waitFor(time.Minute, 2*time.Second, func() (bool, error) {
		return dialOK(srv.IPv4, 80), nil
	}); err != nil {
		t.Errorf("port 80 on %s does not answer after the firewall was removed: %v", srv.IPv4, err)
	}
}

// testFW03: with a firewall allowing tcp 22 in and only tcp 443 out, the
// server fetches an https URL from the internet, a plain http URL fails,
// and SSH from the host still works.
func testFW03(t *testing.T) {
	e := sharedEnv(t)
	addrs, err := net.DefaultResolver.LookupIP(context.Background(), "ip4", fwProbeHost)
	if err != nil || len(addrs) == 0 {
		t.Fatalf("resolve %s on the host: %v", fwProbeHost, err)
	}
	ip := addrs[0].String()
	// The address is resolved on the host, so the guest needs no DNS, which
	// the firewall does not allow out.
	fetch := func(scheme string, port int) string {
		return fmt.Sprintf("curl -sS -o /dev/null -m 15 --resolve %s:%d:%s %s://%s/", fwProbeHost, port, ip, scheme, fwProbeHost)
	}
	httpsCmd, httpCmd := fetch("https", 443), fetch("http", 80)

	takeServerSlots(t, 1)
	fw := e.createFirewall(t, "fw03", []map[string]any{
		sshInRule(),
		{"direction": "out", "protocol": "tcp", "port": "443", "destination_ips": []string{"0.0.0.0/0"}},
	})
	srv := firewallServer(t, e, "fw03")
	var last result
	if err := waitFor(2*time.Minute, 5*time.Second, func() (bool, error) {
		r, err := e.ssh(srv, time.Minute, "", httpCmd)
		last = r
		return err == nil && r.ExitCode == 0, nil
	}); err != nil {
		t.Fatalf("%s cannot fetch http://%s before any firewall: %v\n%s", srv.Name, fwProbeHost, err, last)
	}

	e.firewallAction(t, fw, "apply_to_resources", map[string]any{"apply_to": serverResource(srv)})
	e.waitSSH(t, srv, time.Minute)
	if r, err := e.ssh(srv, time.Minute, "", httpsCmd); err != nil || r.ExitCode != 0 {
		t.Errorf("%s cannot fetch https://%s with tcp 443 out allowed: %v\n%s", srv.Name, fwProbeHost, err, r)
	}
	// curl fails with its own code; 255 or -1 is ssh itself failing.
	if r, _ := e.ssh(srv, time.Minute, "", httpCmd); r.ExitCode == 0 || r.ExitCode == 255 || r.ExitCode == -1 {
		t.Errorf("http://%s from %s: want a failed fetch with only tcp 443 out allowed\n%s", fwProbeHost, srv.Name, r)
	}
	if r, err := e.ssh(srv, time.Minute, "", "true"); err != nil || r.ExitCode != 0 {
		t.Errorf("ssh root@%s with outbound restricted: %v\n%s", srv.IPv4, err, r)
	}
}
