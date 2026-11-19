//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// primaryIPv4 is the ID and address of the server's primary IPv4 as the
// API reports them.
func (e *env) primaryIPv4(t *testing.T, id int64) (int64, string) {
	t.Helper()
	var body struct {
		Server struct {
			PublicNet struct {
				IPv4 *struct {
					ID int64  `json:"id"`
					IP string `json:"ip"`
				} `json:"ipv4"`
			} `json:"public_net"`
		} `json:"server"`
	}
	status, aerr, err := e.apiDo(context.Background(), http.MethodGet, fmt.Sprintf("/servers/%d", id), nil, &body)
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET /servers/%d: %v (status %d %s)", id, err, status, aerr.Code)
	}
	p := body.Server.PublicNet.IPv4
	if p == nil || p.ID == 0 || p.IP == "" {
		t.Fatalf("server %d has no primary IPv4", id)
	}
	return p.ID, p.IP
}

// waitHostname waits until SSH on addr answers with the hostname want.
func (e *env) waitHostname(t *testing.T, addr, want string, timeout time.Duration) {
	t.Helper()
	target := &server{Name: addr, IPv4: addr}
	var last result
	if err := waitFor(timeout, 5*time.Second, func() (bool, error) {
		r, err := e.ssh(target, 30*time.Second, "", "hostname")
		last = r
		return err == nil && strings.TrimSpace(r.Stdout) == want, nil
	}); err != nil {
		t.Errorf("ssh root@%s did not answer with hostname %q: %v\n%s", addr, want, err, last)
	}
}

// powerOff powers the server off and waits for status off.
func (e *env) powerOff(t *testing.T, srv *server) {
	t.Helper()
	e.hcloudAction(t, 5*time.Minute, "server", "poweroff", srv.Name)
	if err := e.waitStatus(srv.ID, "off", 2*time.Minute); err != nil {
		t.Fatal(err)
	}
}

// testPIP02: with both servers off, X's IPv4 unassigned and assigned to Y
// (whose own IPv4 was unassigned), Y answers SSH on that address with its
// hostname once powered on.
func testPIP02(t *testing.T) {
	e := sharedEnv(t)
	takeServerSlots(t, 2)
	x := ownServer(t, e, "pip02-x")
	y := ownServer(t, e, "pip02-y")
	e.powerOff(t, x)
	e.powerOff(t, y)

	xIP, xAddr := e.primaryIPv4(t, x.ID)
	yIP, _ := e.primaryIPv4(t, y.ID)

	// Y's own IPv4 is left unassigned; it goes before the servers do.
	t.Cleanup(func() {
		var body struct {
			PrimaryIP struct {
				AssigneeID *int64 `json:"assignee_id"`
			} `json:"primary_ip"`
		}
		path := fmt.Sprintf("/primary_ips/%d", yIP)
		status, _, err := e.apiDo(context.Background(), http.MethodGet, path, nil, &body)
		if err != nil || status != http.StatusOK || body.PrimaryIP.AssigneeID != nil {
			return // gone, or still on Y and deleted with it
		}
		if status, aerr, err := e.apiDo(context.Background(), http.MethodDelete, path, nil, nil); err != nil ||
			(status != http.StatusNoContent && status != http.StatusNotFound) {
			t.Errorf("DELETE %s: %v (status %d %s)", path, err, status, aerr.Code)
		}
	})

	e.runAPIAction(t, fmt.Sprintf("/primary_ips/%d/actions/unassign", yIP), nil, 5*time.Minute)
	e.runAPIAction(t, fmt.Sprintf("/primary_ips/%d/actions/unassign", xIP), nil, 5*time.Minute)
	e.runAPIAction(t, fmt.Sprintf("/primary_ips/%d/actions/assign", xIP),
		map[string]any{"assignee_id": y.ID, "assignee_type": "server"}, 5*time.Minute)

	e.hcloudAction(t, 5*time.Minute, "server", "poweron", y.Name)
	if err := e.waitStatus(y.ID, "running", 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	e.waitHostname(t, xAddr, y.Name, 10*time.Minute)
}

// testFIP02: a floating IP configured on A and B answers SSH with A's
// hostname while assigned to A and with B's after reassignment to B,
// without a change on A.
func testFIP02(t *testing.T) {
	f := sharedNetwork(t)
	e := f.e
	a, b := f.a, f.b

	var body struct {
		FloatingIP struct {
			ID int64  `json:"id"`
			IP string `json:"ip"`
		} `json:"floating_ip"`
		Action *struct {
			ID int64 `json:"id"`
		} `json:"action"`
	}
	e.post(t, "/floating_ips", map[string]any{
		"type": "ipv4", "server": a.ID, "name": harness.runID + "-fip02",
	}, &body)
	id, fip := body.FloatingIP.ID, body.FloatingIP.IP
	t.Cleanup(func() { e.deleteFloatingIP(t, id) })
	if body.Action != nil {
		if err := e.waitAction(body.Action.ID, 5*time.Minute); err != nil {
			t.Fatalf("create floating IP: %v", err)
		}
	}
	if fip == "" {
		t.Fatal("POST /floating_ips returned no ip")
	}

	for _, m := range []*member{a, b} {
		t.Cleanup(func() { _, _ = e.ssh(m.server, time.Minute, "", "ip addr del "+fip+"/32 dev "+m.pubIf) })
		e.guest(t, m.server, "ip addr replace "+fip+"/32 dev "+m.pubIf)
	}

	e.waitHostname(t, fip, a.Name, 3*time.Minute)
	e.runLockedAction(t, fmt.Sprintf("/floating_ips/%d/actions/assign", id), map[string]any{"server": b.ID})
	e.waitHostname(t, fip, b.Name, 3*time.Minute)
}

// deleteFloatingIP unassigns the floating IP if needed and deletes it,
// trying again while an action of it runs.
func (e *env) deleteFloatingIP(t *testing.T, id int64) {
	t.Helper()
	path := fmt.Sprintf("/floating_ips/%d", id)
	err := waitFor(5*time.Minute, 3*time.Second, func() (bool, error) {
		var body struct {
			FloatingIP struct {
				Server *int64 `json:"server"`
			} `json:"floating_ip"`
		}
		status, _, err := e.apiDo(context.Background(), http.MethodGet, path, nil, &body)
		if err != nil {
			return false, nil
		}
		if status == http.StatusNotFound {
			return true, nil
		}
		if body.FloatingIP.Server != nil {
			_, _, _ = e.apiDo(context.Background(), http.MethodPost, path+"/actions/unassign", nil, nil)
			return false, nil
		}
		status, _, err = e.apiDo(context.Background(), http.MethodDelete, path, nil, nil)
		return err == nil && (status == http.StatusNoContent || status == http.StatusNotFound), nil
	})
	if err != nil {
		t.Errorf("DELETE %s: %v", path, err)
	}
}
