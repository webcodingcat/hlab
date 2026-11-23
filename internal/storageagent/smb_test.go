package storageagent_test

import (
	"bufio"
	"net/netip"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/storageagent"
)

// The addresses of protocolDoc.
const (
	protoPublicRange = "198.19.0.0/16"
	protoHostNet     = "198.19.0.0/30"
	protoTLSDir      = "/test/tls"
)

// account returns an account's hashes, distinct per user.
func account(user string) agentapi.Hashes {
	return agentapi.Hashes{Crypt: "$6$salt$crypt-" + user, NT: "nt-" + user}
}

// protocolDoc is the document the protocol renderers are tested with:
//
//   - u100000 (198.19.0.20): Samba and WebDAV on, reachable externally;
//     subaccounts sub1 (Samba, WebDAV, read-only, internal), sub2 (both
//     off) and sub3 (Samba, WebDAV, writable, reachable externally).
//   - u100001 (198.19.0.21): Samba and WebDAV off, internal.
//   - u100002 (198.19.0.22): Samba and WebDAV on, internal.
func protocolDoc() agentapi.Storage {
	b0 := box("u100000", "198.19.0.20")
	b0.Hashes = account(b0.User)
	b0.Access = agentapi.Access{SSH: true, Samba: true, WebDAV: true, ReachableExternally: true}
	b0.Subaccounts = []agentapi.Subaccount{
		{User: "u100000-sub1", HomeDir: "ro", Hashes: account("u100000-sub1"),
			Access: agentapi.Access{Samba: true, WebDAV: true, ReadOnly: true}},
		{User: "u100000-sub2", HomeDir: "off", Hashes: account("u100000-sub2"),
			Access: agentapi.Access{ReachableExternally: true}},
		{User: "u100000-sub3", HomeDir: "rw", Hashes: account("u100000-sub3"),
			Access: agentapi.Access{Samba: true, WebDAV: true, ReachableExternally: true}},
	}
	b1 := box("u100001", "198.19.0.21")
	b1.Hashes = account(b1.User)
	b2 := box("u100002", "198.19.0.22")
	b2.Hashes = account(b2.User)
	b2.Access = agentapi.Access{Samba: true, WebDAV: true}
	return agentapi.Storage{
		Schema:      agentapi.StorageSchema,
		Generation:  1,
		PublicRange: netip.MustParsePrefix(protoPublicRange),
		HostNet:     netip.MustParsePrefix(protoHostNet),
		TLS:         agentapi.TLS{CertPEM: "CERT", KeyPEM: "KEY"},
		Boxes:       []agentapi.Box{b0, b1, b2},
	}
}

// smbSections parses an smb.conf text into its sections, each a map of
// lower-case parameter names to values with collapsed whitespace;
// parameters before the first section go to "".
func smbSections(text string) map[string]map[string]string {
	out := map[string]map[string]string{"": {}}
	cur := ""
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			cur = strings.TrimSpace(line[1 : len(line)-1])
			out[cur] = map[string]string{}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[cur][strings.ToLower(strings.Join(strings.Fields(k), " "))] = strings.Join(strings.Fields(v), " ")
	}
	return out
}

func testSambaConfig(t *testing.T) {
	files := storageagent.SambaConfig(protocolDoc(), nil)
	internal := protoPublicRange + " EXCEPT " + protoHostNet

	cases := []struct {
		name, ip, share, account string
		served                   bool
		readOnly                 bool
		hostsAllow               string // "" when the share has none
	}{
		{name: "box_samba_on", ip: "198.19.0.20", share: "backup", account: "u100000", served: true},
		{name: "box_samba_off", ip: "198.19.0.21", share: "backup", account: "u100001"},
		{name: "box_not_reachable_externally", ip: "198.19.0.22", share: "backup", account: "u100002",
			served: true, hostsAllow: internal},
		{name: "subaccount_samba_on", ip: "198.19.0.20", share: "u100000-sub3", account: "u100000-sub3", served: true},
		{name: "subaccount_samba_off", ip: "198.19.0.20", share: "u100000-sub2", account: "u100000-sub2"},
		{name: "subaccount_read_only_not_reachable_externally", ip: "198.19.0.20", share: "u100000-sub1",
			account: "u100000-sub1", served: true, readOnly: true, hostsAllow: internal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(files.Global, "/etc/samba/ip-%i.conf") {
				t.Fatalf("global configuration does not include the per-IP files:\n%s", files.Global)
			}
			perIP, ok := files.PerIP[netip.MustParseAddr(c.ip)]
			if !ok && c.served {
				t.Fatalf("no configuration for box IP %s", c.ip)
			}
			share, found := smbSections(perIP)[c.share]
			tdbsam := false
			for _, a := range files.Accounts {
				tdbsam = tdbsam || a == c.account
			}
			if !c.served {
				if found {
					t.Errorf("share %q served at %s, want none:\n%s", c.share, c.ip, perIP)
				}
				if tdbsam {
					t.Errorf("Accounts = %v, want no tdbsam entry for %s", files.Accounts, c.account)
				}
				return
			}
			if !found {
				t.Fatalf("share %q missing at %s:\n%s", c.share, c.ip, perIP)
			}
			if !tdbsam {
				t.Errorf("Accounts = %v, want a tdbsam entry for %s", files.Accounts, c.account)
			}
			if got := share["valid users"]; got != c.account {
				t.Errorf("valid users = %q, want %q", got, c.account)
			}
			ro := strings.EqualFold(share["read only"], "yes") || strings.EqualFold(share["writable"], "no") ||
				strings.EqualFold(share["writeable"], "no")
			if ro != c.readOnly {
				t.Errorf("read-only = %v, want %v: %v", ro, c.readOnly, share)
			}
			if got := share["hosts allow"]; got != c.hostsAllow {
				t.Errorf("hosts allow = %q, want %q", got, c.hostsAllow)
			}
		})
	}
}
