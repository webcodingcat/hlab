//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"
)

// testHOST03: from the selected Ubuntu distro in NAT mode with dnsTunneling,
// `ssh -i <temp key> root@<shared server IP> hostname` prints the server
// name.
func testHOST03(t *testing.T) {
	_, srv := sharedServer(t)
	_, w := sharedWSL(t)

	script := "ssh -i '" + w.key + "'" +
		" -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=no" +
		" -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10 -o LogLevel=ERROR" +
		" root@" + srv.IPv4 + " hostname\n"
	r, err := w.run(context.Background(), 2*time.Minute, nil, script)
	if err != nil {
		t.Fatalf("ssh root@%s from distro %s: %v\n%s", srv.IPv4, w.distro, err, r)
	}
	if got := strings.TrimSpace(r.Stdout); got != srv.Name {
		t.Errorf("hostname %q, want %q", got, srv.Name)
	}
}
