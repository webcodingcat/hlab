package agent_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/webcodingcat/hlab/internal/agent"
	"github.com/webcodingcat/hlab/internal/agentapi"
)

// lbDir is the folder the rendered configuration refers to.
const lbDir = "/run/hlab/lb7"

// haSections splits a rendered haproxy.cfg into its sections, keyed by the
// section line ("global", "frontend fe8080", "backend be8080"), each with
// its directives with whitespace collapsed to single spaces.
func haSections(cfg string) map[string][]string {
	out := map[string][]string{}
	cur := ""
	for _, line := range strings.Split(cfg, "\n") {
		if i := strings.Index(line, "#"); i >= 0 && strings.TrimSpace(line[:i]) == "" {
			continue
		}
		text := strings.Join(strings.Fields(line), " ")
		if text == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			cur = text
			out[cur] = []string{}
			continue
		}
		out[cur] = append(out[cur], text)
	}
	return out
}

// section returns the directives of section name and fails without it.
func section(t *testing.T, cfg, name string) []string {
	t.Helper()
	s, ok := haSections(cfg)[name]
	if !ok {
		t.Fatalf("no section %q in\n%s", name, cfg)
	}
	return s
}

// withPrefix returns the directives starting with prefix.
func withPrefix(lines []string, prefix string) []string {
	var out []string
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

// hasToken reports whether the directive has the space-separated token.
func hasToken(line, token string) bool {
	return slices.Contains(strings.Fields(line), token)
}

// tokenAfter returns the token following key in line, or "".
func tokenAfter(line, key string) string {
	f := strings.Fields(line)
	for i := 0; i+1 < len(f); i++ {
		if f[i] == key {
			return f[i+1]
		}
	}
	return ""
}

var lbTargets = []agentapi.LBTarget{
	{Key: "server:1", IP: addr("10.0.0.2"), ViaNetworkID: 3},
	{Key: "server:2", IP: addr("10.0.0.3"), ViaNetworkID: 3},
	{Key: "ip:203.0.113.9", IP: addr("203.0.113.9")},
}

func tcpService(listen, dest int) agentapi.LBService {
	return agentapi.LBService{
		Protocol: "tcp", ListenPort: listen, DestinationPort: dest,
		HealthCheck: agentapi.LBHealthCheck{Protocol: "tcp", Port: dest, Interval: 15, Timeout: 10, Retries: 3},
	}
}

func httpService(listen, dest int, h agentapi.LBHTTP) agentapi.LBService {
	s := tcpService(listen, dest)
	s.Protocol = "http"
	s.HTTP = &h
	return s
}

func renderLB(t *testing.T, lb agentapi.LoadBalancer) (string, map[string][]byte) {
	t.Helper()
	if lb.ID == 0 {
		lb.ID = 7
	}
	if lb.Algorithm == "" {
		lb.Algorithm = "round_robin"
	}
	if lb.Targets == nil {
		lb.Targets = lbTargets
	}
	cfg, files := agent.HAProxyConfig(lb, lbDir)
	if cfg == "" {
		t.Fatal("HAProxyConfig rendered an empty configuration")
	}
	return cfg, files
}

// testHAProxyBalance: the algorithm, one server line per target with its IP
// and the destination port, and maxconn.
func testHAProxyBalance(t *testing.T) {
	for _, tc := range []struct{ algorithm, balance string }{
		{"round_robin", "balance roundrobin"},
		{"least_connections", "balance leastconn"},
	} {
		cfg, _ := renderLB(t, agentapi.LoadBalancer{
			Algorithm: tc.algorithm, MaxConn: 10000,
			Services: []agentapi.LBService{tcpService(8080, 80)},
		})
		be := section(t, cfg, "backend be8080")
		if !slices.Contains(be, tc.balance) {
			t.Errorf("%s: backend be8080 %q, want %q", tc.algorithm, be, tc.balance)
		}
		servers := withPrefix(be, "server ")
		if len(servers) != len(lbTargets) {
			t.Fatalf("%s: server lines %q, want one per target (%d)", tc.algorithm, servers, len(lbTargets))
		}
		for _, tg := range lbTargets {
			want := "server " + tg.Key + " " + tg.IP.String() + ":80"
			if len(withPrefix(servers, want+" ")) != 1 && !slices.Contains(servers, want) {
				t.Errorf("%s: no server line %q in %q", tc.algorithm, want, servers)
			}
		}
		fe := section(t, cfg, "frontend fe8080")
		if len(withPrefix(fe, "mode tcp")) != 1 || len(withPrefix(be, "mode tcp")) != 1 {
			t.Errorf("%s: tcp service not in mode tcp: frontend %q backend %q", tc.algorithm, fe, be)
		}
		if !strings.Contains(" "+strings.Join(strings.Fields(cfg), " ")+" ", " maxconn 10000 ") {
			t.Errorf("%s: no maxconn 10000 in\n%s", tc.algorithm, cfg)
		}
	}
}

// testHAProxyHTTP: sticky cookie, redirect frontend and forwarded headers.
func testHAProxyHTTP(t *testing.T) {
	sticky := httpService(8000, 80, agentapi.LBHTTP{CookieName: "MYCOOKIE", CookieLifetime: 300, StickySessions: true, TimeoutIdle: 50})
	plain := httpService(8001, 80, agentapi.LBHTTP{CookieName: "HCLBSTICKY", CookieLifetime: 300, TimeoutIdle: 50})
	https := httpService(443, 80, agentapi.LBHTTP{
		CookieName: "HCLBSTICKY", CookieLifetime: 300, RedirectHTTP: true, TimeoutIdle: 50,
		CertPEM: []string{fakePEM("CERTIFICATE", "leaf-a") + fakePEM("CERTIFICATE", "ca")},
		KeyPEM:  []string{fakePEM("PRIVATE KEY", "key-a")},
	})
	https.Protocol = "https"
	cfg, _ := renderLB(t, agentapi.LoadBalancer{Services: []agentapi.LBService{sticky, plain, https}})

	cookies := withPrefix(section(t, cfg, "backend be8000"), "cookie ")
	if len(cookies) != 1 || !strings.HasPrefix(cookies[0], "cookie MYCOOKIE ") || !hasToken(cookies[0], "insert") || tokenAfter(cookies[0], "maxlife") != "300s" {
		t.Errorf("sticky backend cookie lines %q, want one 'cookie MYCOOKIE insert ... maxlife 300s'", cookies)
	}
	if c := withPrefix(section(t, cfg, "backend be8001"), "cookie "); len(c) != 0 {
		t.Errorf("backend without sticky sessions has cookie lines %q", c)
	}

	redirect := section(t, cfg, "frontend fe80redirect")
	binds := withPrefix(redirect, "bind ")
	if len(binds) != 1 || !strings.HasSuffix(strings.Fields(binds[0])[1], ":80") {
		t.Errorf("redirect frontend binds %q, want one bind on port 80", binds)
	}
	var redirects []string
	for _, l := range redirect {
		if strings.Contains(l, "redirect scheme https") && hasToken(l, "301") {
			redirects = append(redirects, l)
		}
	}
	if len(redirects) != 1 {
		t.Errorf("redirect frontend %q, want one 'redirect scheme https code 301'", redirect)
	}
	plainOnly, _ := renderLB(t, agentapi.LoadBalancer{Services: []agentapi.LBService{plain}})
	if _, ok := haSections(plainOnly)["frontend fe80redirect"]; ok {
		t.Errorf("redirect frontend without an https service with redirect_http:\n%s", plainOnly)
	}

	for _, port := range []string{"8000", "8001", "443"} {
		all := strings.Join(append(section(t, cfg, "frontend fe"+port), section(t, cfg, "backend be"+port)...), "\n")
		for _, want := range []string{"option forwardfor", "X-Forwarded-Proto", "X-Forwarded-Port"} {
			if !strings.Contains(all, want) {
				t.Errorf("http service %s lacks %q in\n%s", port, want, all)
			}
		}
		if !strings.Contains(all, "mode http") {
			t.Errorf("http service %s not in mode http:\n%s", port, all)
		}
	}
}

func fakePEM(kind, body string) string {
	return "-----BEGIN " + kind + "-----\n" + body + "\n-----END " + kind + "-----\n"
}

// testHAProxyTLS: the https bind names one file per certificate, the first
// being the default, each file holding leaf, chain and key.
func testHAProxyTLS(t *testing.T) {
	certs := []string{
		fakePEM("CERTIFICATE", "leaf-a") + fakePEM("CERTIFICATE", "ca"),
		fakePEM("CERTIFICATE", "leaf-b") + fakePEM("CERTIFICATE", "ca"),
	}
	keys := []string{fakePEM("PRIVATE KEY", "key-a"), fakePEM("PRIVATE KEY", "key-b")}
	s := httpService(443, 80, agentapi.LBHTTP{CookieName: "HCLBSTICKY", CookieLifetime: 300, TimeoutIdle: 50, CertPEM: certs, KeyPEM: keys})
	s.Protocol = "https"
	cfg, files := renderLB(t, agentapi.LoadBalancer{Services: []agentapi.LBService{s}})

	binds := withPrefix(section(t, cfg, "frontend fe443"), "bind ")
	if len(binds) != 1 {
		t.Fatalf("https frontend binds %q, want one", binds)
	}
	f := strings.Fields(binds[0])
	if !strings.HasSuffix(f[1], ":443") || !slices.Contains(f, "ssl") {
		t.Errorf("https bind %q, want port 443 with ssl", binds[0])
	}
	var crts []string
	for i := 0; i+1 < len(f); i++ {
		if f[i] == "crt" {
			crts = append(crts, f[i+1])
		}
	}
	want := []string{"443-0.pem", "443-1.pem"}
	if len(crts) != len(want) {
		t.Fatalf("https bind %q names certificates %q, want %q in that order", binds[0], crts, want)
	}
	for i, name := range want {
		if !strings.HasPrefix(crts[i], lbDir) || !strings.HasSuffix(crts[i], name) {
			t.Errorf("certificate %d is %q, want %s below %s", i, crts[i], name, lbDir)
		}
		content, ok := files[name]
		if !ok {
			t.Errorf("no file %q among %d files", name, len(files))
			continue
		}
		for _, part := range []string{certs[i], keys[i]} {
			if !strings.Contains(string(content), part) {
				t.Errorf("file %s lacks\n%s", name, part)
			}
		}
	}
}

// testHAProxyChecks: the health check directives of tcp and http checks.
func testHAProxyChecks(t *testing.T) {
	tcp := tcpService(8080, 80)
	tcp.HealthCheck = agentapi.LBHealthCheck{Protocol: "tcp", Port: 80, Interval: 15, Timeout: 10, Retries: 3}
	hc := httpService(8000, 80, agentapi.LBHTTP{CookieName: "HCLBSTICKY", CookieLifetime: 300, TimeoutIdle: 50})
	hc.HealthCheck = agentapi.LBHealthCheck{
		Protocol: "http", Port: 81, Interval: 5, Timeout: 4, Retries: 2,
		Path: "/health", Domain: "example.com", Response: "alive", StatusCodes: []string{"2??", "3??"},
	}
	tlsc := httpService(8001, 443, agentapi.LBHTTP{CookieName: "HCLBSTICKY", CookieLifetime: 300, TimeoutIdle: 50})
	tlsc.HealthCheck = agentapi.LBHealthCheck{
		Protocol: "http", Port: 443, Interval: 15, Timeout: 10, Retries: 3,
		Path: "/", StatusCodes: []string{"2??", "3??"}, TLS: true,
	}
	cfg, _ := renderLB(t, agentapi.LoadBalancer{Services: []agentapi.LBService{tcp, hc, tlsc}})

	for _, c := range []struct {
		backend                      string
		inter, fall, rise, tmo, port string
	}{
		{"backend be8080", "15s", "3", "3", "10s", "80"},
		{"backend be8000", "5s", "2", "2", "4s", "81"},
	} {
		be := section(t, cfg, c.backend)
		servers := withPrefix(be, "server ")
		if len(servers) == 0 {
			t.Fatalf("%s has no server lines", c.backend)
		}
		for _, s := range servers {
			if !hasToken(s, "check") || tokenAfter(s, "inter") != c.inter || tokenAfter(s, "fall") != c.fall || tokenAfter(s, "rise") != c.rise {
				t.Errorf("%s: server line %q, want check inter %s fall %s rise %s", c.backend, s, c.inter, c.fall, c.rise)
			}
			if p := tokenAfter(s, "port"); p != "" && p != c.port {
				t.Errorf("%s: server line %q checks port %s, want %s", c.backend, s, p, c.port)
			}
		}
		if !slices.Contains(be, "timeout check "+c.tmo) {
			t.Errorf("%s: %q, want 'timeout check %s'", c.backend, be, c.tmo)
		}
	}

	be := section(t, cfg, "backend be8000")
	if len(withPrefix(be, "option httpchk")) != 1 {
		t.Errorf("http check backend %q, want option httpchk", be)
	}
	sends := withPrefix(be, "http-check send ")
	if len(sends) != 1 || tokenAfter(sends[0], "meth") != "GET" || tokenAfter(sends[0], "uri") != "/health" ||
		!strings.Contains(sends[0], "hdr Host example.com") {
		t.Errorf("http-check send lines %q, want meth GET uri /health hdr Host example.com", sends)
	}
	var statusRegex, strExpect []string
	for _, l := range withPrefix(be, "http-check expect ") {
		switch f := strings.Fields(l); {
		case slices.Contains(f, "rstatus"):
			statusRegex = append(statusRegex, tokenAfter(l, "rstatus"))
		case slices.Contains(f, "string") || slices.Contains(f, "rstring"):
			strExpect = append(strExpect, l)
		}
	}
	if len(statusRegex) != 1 {
		t.Fatalf("http-check expect rstatus lines %q, want one regex for both patterns", statusRegex)
	}
	re, err := regexp.Compile(strings.Trim(statusRegex[0], `"'`))
	if err != nil {
		t.Fatalf("status regex %q: %v", statusRegex[0], err)
	}
	for code, want := range map[string]bool{"200": true, "204": true, "302": true, "404": false, "500": false, "1200": false} {
		if re.MatchString(code) != want {
			t.Errorf("status regex %q matches %s = %v, want %v", statusRegex[0], code, !want, want)
		}
	}
	if len(strExpect) != 1 || !strings.Contains(strExpect[0], "alive") {
		t.Errorf("response expect lines %q, want one expecting 'alive'", strExpect)
	}
	if strings.Contains(strings.Join(be, "\n"), "ssl") {
		t.Errorf("check without tls uses ssl: %q", be)
	}
	tlsBe := strings.Join(section(t, cfg, "backend be8001"), "\n")
	if !strings.Contains(tlsBe, "ssl") {
		t.Errorf("check with tls does not use ssl:\n%s", tlsBe)
	}
	if strings.Contains(strings.Join(withPrefix(section(t, cfg, "backend be8001"), "http-check expect "), "\n"), "string") {
		t.Errorf("check without response expects a string: %s", tlsBe)
	}
}

// statFields are the columns of `show stat` CSV in HAProxy's order.
var statFields = strings.Split("pxname,svname,qcur,qmax,scur,smax,slim,stot,bin,bout,dreq,dresp,ereq,econ,eresp,wretr,wredis,status,weight,act,bck,chkfail,chkdown,lastchg,downtime,qlimit,pid,iid,sid,throttle,lbtot,tracked,type,rate,rate_lim,rate_max,check_status,check_code,check_duration,hrsp_1xx,hrsp_2xx,hrsp_3xx,hrsp_4xx,hrsp_5xx,hrsp_other,hanafail,req_rate,req_rate_max,req_tot,cli_abrt,srv_abrt,comp_in,comp_out,comp_byp,comp_rsp,lastsess,last_chk,last_agt,qtime,ctime,rtime,ttime,agent_status,agent_code,agent_duration,check_desc,agent_desc,check_rise,check_fall,check_health,agent_rise,agent_fall,agent_health,addr", ",")

// statCSV renders rows given as field=value pairs as `show stat` output.
func statCSV(rows ...map[string]string) []byte {
	var b strings.Builder
	b.WriteString("# " + strings.Join(statFields, ",") + ",\n")
	for _, r := range rows {
		vals := make([]string, len(statFields))
		for i, f := range statFields {
			vals[i] = r[f]
		}
		b.WriteString(strings.Join(vals, ",") + ",\n")
	}
	b.WriteString("\n")
	return []byte(b.String())
}

func frontendRow(px, scur, stot, req, bin, bout string) map[string]string {
	return map[string]string{"pxname": px, "svname": "FRONTEND", "scur": scur, "stot": stot, "req_tot": req, "bin": bin, "bout": bout, "status": "OPEN", "type": "0"}
}

func serverRow(px, key, status, check string) map[string]string {
	return map[string]string{"pxname": px, "svname": key, "scur": "1", "stot": "1000", "req_tot": "500", "bin": "99999", "bout": "99999", "status": status, "type": "2", "check_status": check, "addr": "10.0.0.2:80"}
}

// testParseStat: health per target and port, and the frontend sums.
func testParseStat(t *testing.T) {
	csv := statCSV(
		frontendRow("fe8080", "3", "100", "0", "1000", "2000"),
		serverRow("be8080", "server:1", "UP", "L4OK"),
		serverRow("be8080", "server:2", "UP 1/3", "L4OK"),
		map[string]string{"pxname": "be8080", "svname": "BACKEND", "scur": "50", "stot": "5000", "req_tot": "7", "bin": "7777", "bout": "7777", "status": "UP", "type": "1"},
		frontendRow("fe8000", "4", "40", "60", "10", "20"),
		serverRow("be8000", "server:1", "DOWN", "L4CON"),
		serverRow("be8000", "ip:203.0.113.9", "UP", "INI"),
		map[string]string{"pxname": "be8000", "svname": "BACKEND", "scur": "50", "stot": "5000", "req_tot": "7", "bin": "7777", "bout": "7777", "status": "DOWN", "type": "1"},
	)
	health, counters, err := agent.ParseStat(csv)
	if err != nil {
		t.Fatalf("ParseStat: %v", err)
	}
	want := []agentapi.TargetHealth{
		{Key: "server:1", ListenPort: 8080, Status: "healthy"},
		{Key: "server:2", ListenPort: 8080, Status: "healthy"},
		{Key: "server:1", ListenPort: 8000, Status: "unhealthy"},
		{Key: "ip:203.0.113.9", ListenPort: 8000, Status: "unknown"},
	}
	if len(health) != len(want) {
		t.Errorf("health %+v, want %+v", health, want)
	}
	for _, w := range want {
		if !slices.Contains(health, w) {
			t.Errorf("health %+v lacks %+v", health, w)
		}
	}
	wantC := agentapi.LBCounters{OpenConnections: 7, ConnectionsTotal: 140, RequestsTotal: 60, BytesIn: 1010, BytesOut: 2020}
	if counters != wantC {
		t.Errorf("counters %+v, want %+v (sums of the frontends)", counters, wantC)
	}
}

// testHAProxyProxyProtocol: send-proxy-v2 on every server line of a
// service with proxyprotocol, on none of a service without.
func testHAProxyProxyProtocol(t *testing.T) {
	with := tcpService(8080, 80)
	with.ProxyProtocol = true
	without := tcpService(8081, 81)
	cfg, _ := renderLB(t, agentapi.LoadBalancer{Services: []agentapi.LBService{with, without}})
	servers := withPrefix(section(t, cfg, "backend be8080"), "server ")
	if len(servers) != len(lbTargets) {
		t.Fatalf("server lines %q, want %d", servers, len(lbTargets))
	}
	for _, s := range servers {
		if !hasToken(s, "send-proxy-v2") {
			t.Errorf("server line %q of a proxyprotocol service lacks send-proxy-v2", s)
		}
	}
	for _, s := range withPrefix(section(t, cfg, "backend be8081"), "server ") {
		if strings.Contains(s, "send-proxy") {
			t.Errorf("server line %q of a service without proxyprotocol sends PROXY", s)
		}
	}
}
