package loadbalancers_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/apitest"
	"github.com/webcodingcat/hlab/internal/catalog"
	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/platform/platformtest"
	resactions "github.com/webcodingcat/hlab/internal/resources/actions"
	"github.com/webcodingcat/hlab/internal/resources/certificates"
	"github.com/webcodingcat/hlab/internal/resources/loadbalancers"
	"github.com/webcodingcat/hlab/internal/resources/networks"
	"github.com/webcodingcat/hlab/internal/resources/servers"
	"github.com/webcodingcat/hlab/internal/store"
)

// plenty is a host with room for every server of the tests.
var plenty = platform.Resources{MemoryMB: 64 << 10, FreeMemoryMB: 64 << 10, LogicalCPUs: 16, FreeDiskBytes: 1 << 50}

// unknownID is an ID no resource of the tests has.
const unknownID = 999999

// fakePIPs is the primary IPs module as the load balancers see it: it
// hands out the addresses of known IDs and records the calls.
type fakePIPs struct {
	mu       sync.Mutex
	ipv4     map[int64]netip.Addr
	ipv6     map[int64]netip.Prefix
	assigned []pipCall
	deleted  []int64
}

type pipCall struct {
	lbID       int64
	location   string
	ipv4, ipv6 int64 // 0: none
}

func (f *fakePIPs) AssignLoadBalancer(_ store.Tx, _, lbID int64, location string, ipv4, ipv6 *int64) (netip.Addr, netip.Prefix, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := pipCall{lbID: lbID, location: location}
	var a netip.Addr
	var p netip.Prefix
	if ipv4 != nil {
		c.ipv4, a = *ipv4, f.ipv4[*ipv4]
	}
	if ipv6 != nil {
		c.ipv6, p = *ipv6, f.ipv6[*ipv6]
	}
	f.assigned = append(f.assigned, c)
	return a, p, nil
}

func (f *fakePIPs) LoadBalancerDeleted(_ store.Tx, lbID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, lbID)
	return nil
}

// fakeClock is the module's clock; jobs are run by the tests, so After
// never fires.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(time.Duration) <-chan time.Time { return make(chan time.Time) }

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

type lbEnv struct {
	*apitest.Env
	cat   *catalog.Catalog
	srv   *servers.Module
	nets  *networks.Module
	lb    *loadbalancers.Module
	pips  *fakePIPs
	clock *fakeClock

	status atomic.Pointer[agentapi.Status]
	mu     sync.Mutex
	pushes []agentapi.Config
}

// newEnv mounts the servers, networks, certificates and load balancers
// modules on a fake platform, wired like the cloud does, with a fake
// primary IPs module, a fake clock and a fake agent status. tune may
// change the load balancers' dependencies. Every agent push records the
// document of servers, networks and load balancers.
func newEnv(t *testing.T, tune ...func(d *loadbalancers.Deps)) *lbEnv {
	t.Helper()
	cat, err := catalog.Load("../../../catalog")
	if err != nil {
		t.Fatalf("load shipped catalog: %v", err)
	}
	fake := platformtest.NewFake()
	fake.SetResources(plenty)
	e := &lbEnv{
		cat:   cat,
		pips:  &fakePIPs{ipv4: map[int64]netip.Addr{}, ipv6: map[int64]netip.Prefix{}},
		clock: &fakeClock{now: time.Now().UTC().Truncate(time.Second)},
	}
	e.status.Store(&agentapi.Status{})
	d := apitest.ServersDeps(t, cat, fake)
	d.PushAgent = e.push
	e.srv = servers.New(d)
	e.nets = networks.New(networks.Deps{Store: d.Store, Catalog: cat, Servers: e.srv, PushAgent: e.push})
	e.srv.SetNetworks(e.nets)
	ld := loadbalancers.Deps{
		Store:       d.Store,
		Catalog:     cat,
		PublicRange: d.PublicRange,
		PushAgent:   e.push,
		AgentStatus: func(context.Context) (agentapi.Status, error) { return *e.status.Load(), nil },
		PublicIPs:   e.pips,
		Networks:    e.nets,
		Clock:       e.clock,
	}
	for _, f := range tune {
		f(&ld)
	}
	e.lb = loadbalancers.New(ld)
	e.nets.SetLoadBalancers(e.lb)
	e.srv.OnDelete(e.lb.ServerDeleted)
	certs := certificates.New(certificates.Deps{Store: d.Store, PushAgent: e.push, UsedBy: e.lb.UsedBy})
	e.Env = apitest.New(t, e.srv, e.nets, certs, e.lb, resactions.New())
	return e
}

func (e *lbEnv) push(context.Context) error {
	var doc agentapi.Config
	err := e.Store.Tx(func(tx store.Tx) error {
		if err := e.srv.Contribute(tx, &doc); err != nil {
			return err
		}
		if err := e.nets.Contribute(tx, &doc); err != nil {
			return err
		}
		return e.lb.Contribute(tx, &doc)
	})
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.pushes = append(e.pushes, doc)
	e.mu.Unlock()
	return nil
}

// waitDoc waits until the last pushed document satisfies ok and returns
// it.
func (e *lbEnv) waitDoc(t *testing.T, what string, ok func(doc agentapi.Config) bool) agentapi.Config {
	t.Helper()
	deadline := time.Now().Add(apitest.ActionTimeout)
	for {
		e.mu.Lock()
		var last *agentapi.Config
		if n := len(e.pushes); n > 0 {
			last = &e.pushes[n-1]
		}
		e.mu.Unlock()
		if last != nil && ok(*last) {
			return *last
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pushed agent document with %s within %s", what, apitest.ActionTimeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// runJob runs the load balancers' job of that name once.
func (e *lbEnv) runJob(t *testing.T, name string) {
	t.Helper()
	for _, j := range e.lb.Jobs() {
		if j.Name == name {
			ctx, cancel := context.WithTimeout(context.Background(), apitest.ActionTimeout)
			defer cancel()
			j.Run(ctx)
			return
		}
	}
	t.Fatalf("load balancers module has no job %q", name)
}

type ipView struct {
	ID     *int64  `json:"id"`
	IP     *string `json:"ip"`
	DNSPtr *string `json:"dns_ptr"`
}

type healthView struct {
	ListenPort int    `json:"listen_port"`
	Status     string `json:"status"`
}

type targetView struct {
	Type   string `json:"type"`
	Server *struct {
		ID int64 `json:"id"`
	} `json:"server"`
	LabelSelector *struct {
		Selector string `json:"selector"`
	} `json:"label_selector"`
	IP *struct {
		IP string `json:"ip"`
	} `json:"ip"`
	UsePrivateIP *bool        `json:"use_private_ip"`
	HealthStatus []healthView `json:"health_status"`
	Targets      []targetView `json:"targets"`
}

type healthCheckView struct {
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
	Interval int    `json:"interval"`
	Timeout  int    `json:"timeout"`
	Retries  int    `json:"retries"`
	HTTP     *struct {
		Path        string   `json:"path"`
		StatusCodes []string `json:"status_codes"`
		TLS         *bool    `json:"tls"`
	} `json:"http"`
}

type serviceView struct {
	Protocol        string          `json:"protocol"`
	ListenPort      int             `json:"listen_port"`
	DestinationPort int             `json:"destination_port"`
	Proxyprotocol   bool            `json:"proxyprotocol"`
	HealthCheck     healthCheckView `json:"health_check"`
	HTTP            *struct {
		CookieName     string  `json:"cookie_name"`
		CookieLifetime int     `json:"cookie_lifetime"`
		Certificates   []int64 `json:"certificates"`
		RedirectHTTP   *bool   `json:"redirect_http"`
		StickySessions *bool   `json:"sticky_sessions"`
	} `json:"http"`
}

type lbView struct {
	ID        int64             `json:"id"`
	Name      string            `json:"name"`
	Labels    map[string]string `json:"labels"`
	PublicNet struct {
		Enabled bool   `json:"enabled"`
		IPv4    ipView `json:"ipv4"`
		IPv6    ipView `json:"ipv6"`
	} `json:"public_net"`
	PrivateNet []struct {
		Network int64  `json:"network"`
		IP      string `json:"ip"`
	} `json:"private_net"`
	Location struct {
		Name        string `json:"name"`
		NetworkZone string `json:"network_zone"`
	} `json:"location"`
	LoadBalancerType struct {
		Name string `json:"name"`
	} `json:"load_balancer_type"`
	Protection struct {
		Delete bool `json:"delete"`
	} `json:"protection"`
	Algorithm struct {
		Type string `json:"type"`
	} `json:"algorithm"`
	Services []serviceView `json:"services"`
	Targets  []targetView  `json:"targets"`
}

func (l lbView) ipv4() string {
	if l.PublicNet.IPv4.IP == nil {
		return ""
	}
	return *l.PublicNet.IPv4.IP
}

func (l lbView) ipv6() string {
	if l.PublicNet.IPv6.IP == nil {
		return ""
	}
	return *l.PublicNet.IPv6.IP
}

// service returns the service on listen port, if any.
func (l lbView) service(port int) (serviceView, bool) {
	for _, s := range l.Services {
		if s.ListenPort == port {
			return s, true
		}
	}
	return serviceView{}, false
}

// serverTarget returns the server target of server id, if any.
func (l lbView) serverTarget(id int64) (targetView, bool) {
	for _, tg := range l.Targets {
		if tg.Type == "server" && tg.Server != nil && tg.Server.ID == id {
			return tg, true
		}
	}
	return targetView{}, false
}

// lbBody is the create body of an lb11 load balancer in fsn1.
func lbBody(name string) map[string]any {
	return map[string]any{"name": name, "load_balancer_type": "lb11", "location": "fsn1"}
}

// postLB posts a load balancer create and returns the response.
func postLB(e *lbEnv, body map[string]any) *http.Response {
	return e.Do(http.MethodPost, "/load_balancers", body)
}

// createLB creates a load balancer, expects 201 with a successful
// create_load_balancer action and returns the load balancer as GET shows it.
func createLB(t *testing.T, e *lbEnv, body map[string]any) lbView {
	t.Helper()
	resp := postLB(e, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /load_balancers %v: status %d (%s), want 201", body, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		LoadBalancer lbView         `json:"load_balancer"`
		Action       apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	if b.Action.Command != "create_load_balancer" {
		t.Errorf("create action command %q, want create_load_balancer", b.Action.Command)
	}
	if a := e.For(t).WaitAction(b.Action.ID); a.Status != "success" {
		t.Fatalf("create load balancer: action %q %+v, want success", a.Status, a.Error)
	}
	return mustLB(t, e, b.LoadBalancer.ID)
}

// getLB returns the load balancer and whether it exists.
func getLB(t *testing.T, e *lbEnv, id int64) (lbView, bool) {
	t.Helper()
	resp := e.Do(http.MethodGet, fmt.Sprintf("/load_balancers/%d", id), nil)
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return lbView{}, false
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /load_balancers/%d: status %d, want 200", id, resp.StatusCode)
	}
	var b struct {
		LoadBalancer lbView `json:"load_balancer"`
	}
	apitest.Decode(t, resp, &b)
	return b.LoadBalancer, true
}

func mustLB(t *testing.T, e *lbEnv, id int64) lbView {
	t.Helper()
	l, ok := getLB(t, e, id)
	if !ok {
		t.Fatalf("GET /load_balancers/%d: 404, want the load balancer", id)
	}
	return l
}

func lbAction(e *lbEnv, id int64, name string, body any) *http.Response {
	return e.Do(http.MethodPost, fmt.Sprintf("/load_balancers/%d/actions/%s", id, name), body)
}

// runAction expects 201 with an action named like the request and waits
// for its success.
func runAction(t *testing.T, e *lbEnv, resp *http.Response, command, what string) apitest.Action {
	t.Helper()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("%s: status %d (%s), want 201", what, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	if b.Action.Command != command {
		t.Errorf("%s: action command %q, want %s", what, b.Action.Command, command)
	}
	a := e.For(t).WaitAction(b.Action.ID)
	if a.Status != "success" {
		t.Fatalf("%s: action %q %+v, want success", what, a.Status, a.Error)
	}
	return a
}

// runLBAction posts action name and waits for its success.
func runLBAction(t *testing.T, e *lbEnv, id int64, name string, body any) apitest.Action {
	t.Helper()
	return runAction(t, e, lbAction(e, id, name, body), name, fmt.Sprintf("%s load balancer %d %v", name, id, body))
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Details struct {
			Fields []struct {
				Name string `json:"name"`
			} `json:"fields"`
		} `json:"details"`
	} `json:"error"`
}

// wantError checks status and error.code of resp.
func wantError(t *testing.T, resp *http.Response, status int, code, what string) errorBody {
	t.Helper()
	var b errorBody
	apitest.Decode(t, resp, &b)
	if resp.StatusCode != status || b.Error.Code != code {
		t.Errorf("%s: status %d code %q, want %d %s", what, resp.StatusCode, b.Error.Code, status, code)
	}
	return b
}

// wantInvalid checks a 422 invalid_input.
func wantInvalid(t *testing.T, resp *http.Response, what string) {
	t.Helper()
	wantError(t, resp, http.StatusUnprocessableEntity, "invalid_input", what)
}

// wantFieldError checks a 422 invalid_input naming field.
func wantFieldError(t *testing.T, resp *http.Response, field, what string) {
	t.Helper()
	b := wantError(t, resp, http.StatusUnprocessableEntity, "invalid_input", what)
	var names []string
	for _, f := range b.Error.Details.Fields {
		names = append(names, f.Name)
	}
	if !slices.Contains(names, field) {
		t.Errorf("%s: error on fields %v, want %s", what, names, field)
	}
}

type serverView struct {
	ID        int64 `json:"id"`
	PublicNet struct {
		IPv4 *struct {
			IP string `json:"ip"`
		} `json:"ipv4"`
	} `json:"public_net"`
	PrivateNet []struct {
		Network int64  `json:"network"`
		IP      string `json:"ip"`
	} `json:"private_net"`
}

func (s serverView) privateIP(network int64) string {
	for _, p := range s.PrivateNet {
		if p.Network == network {
			return p.IP
		}
	}
	return ""
}

// createServer creates a running server in location with the extra create
// fields and returns it as GET shows it.
func createServer(t *testing.T, e *lbEnv, name, location string, extra map[string]any) serverView {
	t.Helper()
	typ := "cx23"
	if location == "ash" || location == "hil" || location == "sin" {
		typ = "cpx11"
	}
	req := map[string]any{"name": name, "server_type": typ, "image": "ubuntu-24.04", "location": location}
	for k, v := range extra {
		req[k] = v
	}
	resp := e.Do(http.MethodPost, "/servers", req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /servers %s: status %d (%s), want 201", name, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Server struct {
			ID int64 `json:"id"`
		} `json:"server"`
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	if a := e.For(t).WaitAction(b.Action.ID); a.Status != "success" {
		t.Fatalf("create server %s: action %q %+v, want success", name, a.Status, a.Error)
	}
	return getServer(t, e, b.Server.ID)
}

func getServer(t *testing.T, e *lbEnv, id int64) serverView {
	t.Helper()
	resp := e.Do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /servers/%d: status %d, want 200", id, resp.StatusCode)
	}
	var b struct {
		Server serverView `json:"server"`
	}
	apitest.Decode(t, resp, &b)
	return b.Server
}

// noIPv4 is the create field for a server without public IPv4.
var noIPv4 = map[string]any{"public_net": map[string]any{"enable_ipv4": false, "enable_ipv6": true}}

// createNetwork creates a network with ipRange and one eu-central cloud
// subnet and returns its ID.
func createNetwork(t *testing.T, e *lbEnv, name, ipRange, subnet string) int64 {
	t.Helper()
	resp := e.Do(http.MethodPost, "/networks", map[string]any{
		"name": name, "ip_range": ipRange,
		"subnets": []any{map[string]any{"type": "cloud", "ip_range": subnet, "network_zone": "eu-central"}},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /networks %s: status %d (%s), want 201", name, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Network struct {
			ID int64 `json:"id"`
		} `json:"network"`
	}
	apitest.Decode(t, resp, &b)
	return b.Network.ID
}

var serial atomic.Int64

// newCert returns a self-signed leaf for domain and its PKCS#8 key as PEM.
func newCert(t *testing.T, domain string) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial.Add(1)),
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(30 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))
}

// uploadCert uploads a new certificate named name with the env's token and
// returns its ID and PEMs.
func uploadCert(t *testing.T, e *apitest.Env, name string) (id int64, certPEM, keyPEM string) {
	t.Helper()
	certPEM, keyPEM = newCert(t, name+".example.test")
	resp := e.Do(http.MethodPost, "/certificates", map[string]any{
		"name": name, "type": "uploaded", "certificate": certPEM, "private_key": keyPEM,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /certificates %s: status %d (%s), want 201", name, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Certificate struct {
			ID int64 `json:"id"`
		} `json:"certificate"`
	}
	apitest.Decode(t, resp, &b)
	return b.Certificate.ID, certPEM, keyPEM
}

func ptr[T any](v T) *T { return &v }

func TestLoadBalancers(t *testing.T) {
	t.Run("LB-01/create", testCreate)
	t.Run("LB-01/change", testChange)
	t.Run("LB-01/network", testNetwork)
	t.Run("LB-02", testServicesTCP)
	t.Run("LB-03", testServicesHTTP)
	t.Run("LB-04", testServicesHTTPS)
	t.Run("LB-06", testTargets)
	t.Run("LB-05", testHealthStatus)
	t.Run("LB-08", testMetrics)
	t.Run("LB-09", testDNSPtr)
	t.Run("LB-06/document", testDocument)
	t.Run("LB-09/document", testDocumentPTR)
}

// testCreate: create, location and type rules, primary IPs and names.
func testCreate(t *testing.T) {
	e := newEnv(t)
	public := netip.MustParsePrefix("198.19.0.0/16")

	body := lbBody("lb1")
	body["algorithm"] = map[string]any{"type": "round_robin"}
	lb := createLB(t, e, body)
	if lb.Name != "lb1" || lb.LoadBalancerType.Name != "lb11" || lb.Location.Name != "fsn1" || lb.Algorithm.Type != "round_robin" {
		t.Errorf("created %+v, want lb1, lb11, fsn1, round_robin", lb)
	}
	if a, err := netip.ParseAddr(lb.ipv4()); err != nil || !public.Contains(a) {
		t.Errorf("public IPv4 %q, want an address of %s", lb.ipv4(), public)
	}
	if lb.PublicNet.IPv4.ID != nil {
		t.Errorf("system IPv4 shows primary IP ID %d, want null", *lb.PublicNet.IPv4.ID)
	}
	want6 := netip.MustParseAddr(fmt.Sprintf("2001:db8:8000:%x::1", lb.ID))
	if got := loadbalancers.IPv6(lb.ID); got != want6 {
		t.Errorf("IPv6(%d) = %s, want %s", lb.ID, got, want6)
	}
	if lb.ipv6() != want6.String() {
		t.Errorf("public IPv6 %q, want %s", lb.ipv6(), want6)
	}

	var firstInZone string
	for _, l := range e.cat.Locations {
		if l.NetworkZone == "eu-central" {
			firstInZone = l.Name
			break
		}
	}
	zoned := createLB(t, e, map[string]any{"name": "zoned", "load_balancer_type": "lb11", "network_zone": "eu-central"})
	if zoned.Location.Name != firstInZone || zoned.Location.NetworkZone != "eu-central" {
		t.Errorf("network_zone eu-central alone: location %s (%s), want %s", zoned.Location.Name, zoned.Location.NetworkZone, firstInZone)
	}

	badType := lbBody("bad-type")
	badType["load_balancer_type"] = "lb99"
	wantInvalid(t, postLB(e, badType), "create with an unknown type")
	badLoc := lbBody("bad-location")
	badLoc["location"] = "nowhere1"
	wantInvalid(t, postLB(e, badLoc), "create with an unknown location")

	e.pips.mu.Lock()
	e.pips.ipv4[501] = netip.MustParseAddr("198.19.77.7")
	e.pips.ipv6[502] = netip.MustParsePrefix("2001:db8:9000:1::/64")
	e.pips.mu.Unlock()
	withPIPs := lbBody("with-pips")
	withPIPs["public_net"] = map[string]any{"ipv4": 501, "ipv6": 502}
	held := createLB(t, e, withPIPs)
	e.pips.mu.Lock()
	calls := slices.Clone(e.pips.assigned)
	e.pips.mu.Unlock()
	if !slices.Contains(calls, pipCall{lbID: held.ID, location: "fsn1", ipv4: 501, ipv6: 502}) {
		t.Errorf("PublicIPs calls %+v, want one for LB %d in fsn1 with 501 and 502", calls, held.ID)
	}
	if held.ipv4() != "198.19.77.7" || held.ipv6() != "2001:db8:9000:1::1" {
		t.Errorf("LB with primary IPs shows %q and %q, want 198.19.77.7 and 2001:db8:9000:1::1", held.ipv4(), held.ipv6())
	}

	private := lbBody("private-pips")
	private["public_net"] = map[string]any{"ipv4": 501}
	private["public_interface"] = false
	wantInvalid(t, postLB(e, private), "create with public_net IDs and public_interface false")

	wantError(t, postLB(e, lbBody("lb1")), http.StatusConflict, "uniqueness_error", "create with a used name")
}

// deleteLB deletes the load balancer and waits for the delete action.
func deleteLB(t *testing.T, e *lbEnv, id int64) {
	t.Helper()
	resp := e.Do(http.MethodDelete, fmt.Sprintf("/load_balancers/%d", id), nil)
	if resp.StatusCode >= 300 {
		t.Fatalf("DELETE /load_balancers/%d: status %d (%s), want success", id, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	if resp.StatusCode == http.StatusCreated {
		var b struct {
			Action apitest.Action `json:"action"`
		}
		apitest.Decode(t, resp, &b)
		if a := e.For(t).WaitAction(b.Action.ID); a.Status != "success" {
			t.Fatalf("delete load balancer %d: action %q %+v, want success", id, a.Status, a.Error)
		}
	} else {
		_ = resp.Body.Close()
	}
	if _, ok := getLB(t, e, id); ok {
		t.Errorf("load balancer %d still there after DELETE", id)
	}
}

func tcpService(listen, dest int) map[string]any {
	return map[string]any{"protocol": "tcp", "listen_port": listen, "destination_port": dest, "proxyprotocol": false}
}

// testChange: algorithm, type, name and labels, protection and deletion.
func testChange(t *testing.T) {
	// A public range with three addresses, so a released one is the only
	// one left to allocate.
	small := netip.MustParsePrefix("198.18.0.0/29")
	e := newEnv(t, func(d *loadbalancers.Deps) { d.PublicRange = small })

	lb := createLB(t, e, lbBody("change"))
	runLBAction(t, e, lb.ID, "change_algorithm", map[string]any{"type": "least_connections"})
	if got := mustLB(t, e, lb.ID).Algorithm.Type; got != "least_connections" {
		t.Errorf("after change_algorithm: algorithm %q, want least_connections", got)
	}

	big := lbBody("big")
	big["load_balancer_type"] = "lb21"
	var services []any
	for i := range 6 {
		services = append(services, tcpService(1000+i, 80))
	}
	big["services"] = services
	bigLB := createLB(t, e, big)
	wantError(t, lbAction(e, bigLB.ID, "change_type", map[string]any{"load_balancer_type": "lb11"}),
		http.StatusUnprocessableEntity, "invalid_load_balancer_type", "change_type below the used services")
	if got := mustLB(t, e, bigLB.ID).LoadBalancerType.Name; got != "lb21" {
		t.Errorf("after refused change_type: type %s, want lb21", got)
	}
	runLBAction(t, e, bigLB.ID, "change_type", map[string]any{"load_balancer_type": "lb31"})
	if got := mustLB(t, e, bigLB.ID).LoadBalancerType.Name; got != "lb31" {
		t.Errorf("after change_type to lb31: type %s", got)
	}

	resp := e.Do(http.MethodPut, fmt.Sprintf("/load_balancers/%d", lb.ID),
		map[string]any{"name": "renamed", "labels": map[string]string{"env": "test"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /load_balancers/%d: status %d (%s), want 200", lb.ID, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	_ = resp.Body.Close()
	if got := mustLB(t, e, lb.ID); got.Name != "renamed" || len(got.Labels) != 1 || got.Labels["env"] != "test" {
		t.Errorf("after PUT: name %q labels %v, want renamed, env=test", got.Name, got.Labels)
	}

	runLBAction(t, e, lb.ID, "change_protection", map[string]any{"delete": true})
	wantError(t, e.Do(http.MethodDelete, fmt.Sprintf("/load_balancers/%d", lb.ID), nil),
		http.StatusLocked, "protected", "DELETE of a protected load balancer")
	mustLB(t, e, lb.ID)
	runLBAction(t, e, lb.ID, "change_protection", map[string]any{"delete": false})

	// lb and bigLB hold two of the three addresses; the third LB takes the
	// last one, so the next create only succeeds with a released address.
	third := createLB(t, e, lbBody("third"))
	freed := third.ipv4()
	deleteLB(t, e, third.ID)
	again := createLB(t, e, lbBody("again"))
	if again.ipv4() != freed {
		t.Errorf("LB created after the delete got %q, want the released %s", again.ipv4(), freed)
	}

	e.pips.mu.Lock()
	e.pips.ipv4[601] = netip.MustParseAddr("198.19.66.6")
	e.pips.mu.Unlock()
	withPIP := lbBody("with-pip")
	withPIP["public_net"] = map[string]any{"ipv4": 601}
	held := createLB(t, e, withPIP)
	deleteLB(t, e, held.ID)
	e.pips.mu.Lock()
	deleted := slices.Clone(e.pips.deleted)
	e.pips.mu.Unlock()
	if !slices.Contains(deleted, held.ID) {
		t.Errorf("LoadBalancerDeleted calls %v, want one for %d", deleted, held.ID)
	}
}
