package storageagent_test

import (
	"bufio"
	"slices"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/storageagent"
)

// vhost is one <VirtualHost> block of an Apache site: its address and its
// directive lines with collapsed whitespace.
type vhost struct {
	addr  string
	lines []string
}

// directive returns the arguments of the first line starting with name.
func (v vhost) directive(name string) ([]string, bool) {
	for _, l := range v.lines {
		f := strings.Fields(l)
		if len(f) > 0 && strings.EqualFold(f[0], name) {
			return f[1:], true
		}
	}
	return nil, false
}

// require returns the arguments of every "Require" line.
func (v vhost) require() [][]string {
	var out [][]string
	for _, l := range v.lines {
		f := strings.Fields(l)
		if len(f) > 0 && strings.EqualFold(f[0], "Require") {
			out = append(out, f[1:])
		}
	}
	return out
}

// apacheSite returns the Listen lines and the vhosts of site in order.
func apacheSite(site string) (listen []string, vhosts []vhost) {
	var cur *vhost
	sc := bufio.NewScanner(strings.NewReader(site))
	for sc.Scan() {
		line := strings.Join(strings.Fields(sc.Text()), " ")
		lower := strings.ToLower(line)
		switch {
		case line == "" || line[0] == '#':
		case strings.HasPrefix(lower, "<virtualhost "):
			vhosts = append(vhosts, vhost{addr: strings.TrimSuffix(line[len("<virtualhost "):], ">")})
			cur = &vhosts[len(vhosts)-1]
		case lower == "</virtualhost>":
			cur = nil
		case cur != nil:
			cur.lines = append(cur.lines, line)
		case strings.HasPrefix(lower, "listen "):
			listen = append(listen, strings.TrimSpace(line[len("listen "):]))
		}
	}
	return listen, vhosts
}

func testWebDAVConfig(t *testing.T) {
	files := storageagent.WebDAVConfig(protocolDoc(), nil, protoTLSDir)
	listen, vhosts := apacheSite(files.Site)

	// account finds the vhost of an account by its ServerName.
	account := func(t *testing.T, user string) (vhost, bool) {
		t.Helper()
		for _, v := range vhosts {
			if name, ok := v.directive("ServerName"); ok && len(name) > 0 &&
				strings.EqualFold(name[0], user+".storagebox.hetzner.internal") {
				return v, true
			}
		}
		return vhost{}, false
	}
	hasRequire := func(v vhost, args ...string) bool {
		for _, r := range v.require() {
			if slices.Equal(r, args) {
				return true
			}
		}
		return false
	}
	served := func(user, box, ip string, readOnly, internal bool) func(*testing.T) {
		return func(t *testing.T) {
			v, ok := account(t, user)
			if !ok {
				t.Fatalf("no vhost for %s:\n%s", user, files.Site)
			}
			if v.addr != ip+":443" {
				t.Errorf("vhost of %s at %q, want %s:443", user, v.addr, ip)
			}
			if id, _ := v.directive("AssignUserID"); len(id) == 0 || id[0] != box {
				t.Errorf("AssignUserID = %q, want the box user %s", id, box)
			}
			if root, _ := v.directive("DocumentRoot"); len(root) != 1 ||
				strings.Trim(root[0], `"`) != "/srv/chroot/"+user+"/home" {
				t.Errorf("DocumentRoot = %q, want /srv/chroot/%s/home", root, user)
			}
			if dav, _ := v.directive("Dav"); len(dav) != 1 || !strings.EqualFold(dav[0], "On") {
				t.Errorf("Dav = %q, want On", dav)
			}
			for _, d := range []string{"SSLCertificateFile", "SSLCertificateKeyFile"} {
				if f, _ := v.directive(d); len(f) != 1 || !strings.HasPrefix(strings.Trim(f[0], `"`), protoTLSDir+"/") {
					t.Errorf("%s = %q, want the wildcard certificate in %s", d, f, protoTLSDir)
				}
			}
			if line := files.Passwd[user]; !strings.Contains(line, "$6$salt$crypt-"+user) {
				t.Errorf("passwd file of %s = %q, want its crypt hash", user, line)
			}
			var methods []string
			for _, r := range v.require() {
				if len(r) > 0 && strings.EqualFold(r[0], "method") {
					methods = append(methods, r[1:]...)
				}
			}
			slices.Sort(methods)
			if readOnly {
				if want := []string{"GET", "HEAD", "OPTIONS", "PROPFIND"}; !slices.Equal(methods, want) {
					t.Errorf("Require method %v, want %v", methods, want)
				}
			} else if len(methods) > 0 {
				t.Errorf("Require method %v, want no method limit for a writable account", methods)
			}
			inRange := hasRequire(v, "ip", protoPublicRange)
			notHost := hasRequire(v, "not", "ip", protoHostNet)
			if internal && (!inRange || !notHost) {
				t.Errorf("Require = %v, want ip %s and not ip %s", v.require(), protoPublicRange, protoHostNet)
			}
			if !internal && (inRange || notHost) {
				t.Errorf("Require = %v, want no source limit", v.require())
			}
		}
	}
	notServed := func(user string) func(*testing.T) {
		return func(t *testing.T) {
			if _, ok := account(t, user); ok {
				t.Errorf("vhost for %s, want none", user)
			}
			if _, ok := files.Passwd[user]; ok {
				t.Errorf("passwd file for %s, want none", user)
			}
		}
	}

	cases := []struct {
		name  string
		check func(*testing.T)
	}{
		{"catch_all_first", func(t *testing.T) {
			for _, ip := range []string{"198.19.0.20", "198.19.0.22"} {
				if !slices.Contains(listen, ip+":443") {
					t.Errorf("Listen %v, want %s:443", listen, ip)
				}
				i := slices.IndexFunc(vhosts, func(v vhost) bool { return v.addr == ip+":443" })
				if i < 0 {
					t.Errorf("no vhost at %s:443", ip)
					continue
				}
				first := vhosts[i]
				if id, _ := first.directive("AssignUserID"); !slices.Equal(id, []string{"nobody", "nogroup"}) {
					t.Errorf("first vhost at %s: AssignUserID %q, want nobody nogroup", ip, id)
				}
				if !hasRequire(first, "all", "denied") {
					t.Errorf("first vhost at %s: Require %v, want all denied", ip, first.require())
				}
			}
		}},
		{"box_webdav_on", served("u100000", "u100000", "198.19.0.20", false, false)},
		{"box_webdav_off", notServed("u100001")},
		{"box_not_reachable_externally", served("u100002", "u100002", "198.19.0.22", false, true)},
		{"subaccount_webdav_on", served("u100000-sub3", "u100000", "198.19.0.20", false, false)},
		{"subaccount_webdav_off", notServed("u100000-sub2")},
		{"subaccount_read_only_not_reachable_externally", served("u100000-sub1", "u100000", "198.19.0.20", true, true)},
	}
	for _, c := range cases {
		t.Run(c.name, c.check)
	}
}
