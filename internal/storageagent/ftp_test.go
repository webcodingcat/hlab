package storageagent_test

import (
	"bufio"
	"net/netip"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/storageagent"
)

// vsftpdOptions parses a vsftpd configuration into its options.
func vsftpdOptions(text string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

// listMatch reports whether a pam_access list (fields with an optional
// EXCEPT) matches, item deciding for one field.
func listMatch(fields []string, item func(string) bool) bool {
	for i, f := range fields {
		if f == "EXCEPT" {
			return listMatch(fields[:i], item) && !listMatch(fields[i+1:], item)
		}
	}
	for _, f := range fields {
		if item(f) {
			return true
		}
	}
	return false
}

// pamAccess reports whether access.conf rules admit user from src: the
// first matching line decides; no matching line admits.
func pamAccess(rules, user string, src netip.Addr) bool {
	sc := bufio.NewScanner(strings.NewReader(rules))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		users := listMatch(strings.Fields(parts[1]), func(u string) bool { return u == "ALL" || u == user })
		origins := listMatch(strings.Fields(parts[2]), func(o string) bool {
			if o == "ALL" {
				return true
			}
			if p, err := netip.ParsePrefix(o); err == nil {
				return p.Contains(src)
			}
			a, err := netip.ParseAddr(o)
			return err == nil && a == src
		})
		if users && origins {
			return strings.TrimSpace(parts[0]) == "+"
		}
	}
	return true
}

func testFTPConfig(t *testing.T) {
	files := storageagent.FTPConfig(protocolDoc(), nil, protoTLSDir)
	conf := vsftpdOptions(files.Conf)

	want := func(opts map[string]string, key, value string) func(*testing.T) {
		return func(t *testing.T) {
			if got := opts[key]; !strings.EqualFold(got, value) {
				t.Errorf("%s=%q, want %q", key, got, value)
			}
		}
	}
	inTLSDir := func(key string) func(*testing.T) {
		return func(t *testing.T) {
			if got := conf[key]; !strings.HasPrefix(got, protoTLSDir+"/") {
				t.Errorf("%s=%q, want the wildcard certificate in %s", key, got, protoTLSDir)
			}
		}
	}
	access := func(user, src string, admitted bool) func(*testing.T) {
		return func(t *testing.T) {
			if got := pamAccess(files.Access, user, netip.MustParseAddr(src)); got != admitted {
				t.Errorf("pam_access admits %s from %s = %v, want %v:\n%s", user, src, got, admitted, files.Access)
			}
		}
	}

	cases := []struct {
		name  string
		check func(*testing.T)
	}{
		{"pasv_min_port", want(conf, "pasv_min_port", "50000")},
		{"pasv_max_port", want(conf, "pasv_max_port", "50099")},
		{"ssl_enable", want(conf, "ssl_enable", "YES")},
		{"certificate", inTLSDir("rsa_cert_file")},
		{"private_key", inTLSDir("rsa_private_key_file")},
		{"plain_login_allowed", want(conf, "force_local_logins_ssl", "NO")},
		{"plain_data_allowed", want(conf, "force_local_data_ssl", "NO")},
		{"read_only_no_write", want(vsftpdOptions(files.Users["u100000-sub1"]), "write_enable", "NO")},
		{"writable_may_write", func(t *testing.T) {
			if got := vsftpdOptions(files.Users["u100000-sub3"])["write_enable"]; strings.EqualFold(got, "NO") {
				t.Errorf("write_enable=%q for a writable subaccount", got)
			}
		}},
		{"internal_subaccount_inside_public_range", access("u100000-sub1", "198.19.0.10", true)},
		{"internal_subaccount_outside_public_range", access("u100000-sub1", "203.0.113.5", false)},
		{"internal_subaccount_from_host_net", access("u100000-sub1", "198.19.0.1", false)},
		{"external_subaccount_outside_public_range", access("u100000-sub3", "203.0.113.5", true)},
		{"external_subaccount_from_host_net", access("u100000-sub3", "198.19.0.1", true)},
	}
	for _, c := range cases {
		t.Run(c.name, c.check)
	}
}
