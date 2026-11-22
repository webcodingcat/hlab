package loadbalancers_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
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
	"github.com/webcodingcat/hlab/internal/rdns"
	"github.com/webcodingcat/hlab/internal/resource"
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
	gate   *apitest.PushGate
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
		gate:  apitest.NewPushGate(t),
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

func (e *lbEnv) push(ctx context.Context) error {
	if err := e.gate.Wait(ctx); err != nil {
		return err
	}
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
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		NetworkZone string `json:"network_zone"`
	} `json:"location"`
	LoadBalancerType struct {
		ID   int64  `json:"id"`
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
	t.Run("LB-09/recover", testRecover)
	t.Run("CLI-02/queue", testQueue)
	t.Run("CLI-02/effect", testEffect)
	t.Run("CLI-02/deleted", testDeletedWhileQueued)
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
	want6 := netip.MustParseAddr(fmt.Sprintf("2001:db8:a000:%x::1", lb.ID))
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

	byID := lbBody("by-type-id")
	byID["load_balancer_type"] = lb.LoadBalancerType.ID
	if got := createLB(t, e, byID); got.LoadBalancerType.Name != "lb11" {
		t.Errorf("create with type ID %d: type %s, want lb11", lb.LoadBalancerType.ID, got.LoadBalancerType.Name)
	}

	locByID := lbBody("by-location-id")
	locByID["location"] = lb.Location.ID
	if got := createLB(t, e, locByID); got.Location.Name != "fsn1" {
		t.Errorf("create with location ID %d: location %s, want fsn1", lb.Location.ID, got.Location.Name)
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

// testRecover: Q14 moves the formal IPv6 of load balancers to the prefix
// 0xA0000000; Recover moves PTR entries stored on the old /64. It also
// drops the pending state of an action a stop interrupted.
func testRecover(t *testing.T) {
	e := newEnv(t)
	var mod any = e.lb
	rec, ok := mod.(resource.Recoverer)
	if !ok {
		t.Fatal("load balancers module does not implement resource.Recoverer")
	}

	lb := createLB(t, e, lbBody("formal"))
	new6 := netip.MustParseAddr(lb.ipv6())
	newOther := withInterfaceID(new6, 0x5)
	old6 := oldFormal(new6, lb.ID)
	oldOther := oldFormal(newOther, lb.ID)

	e.pips.mu.Lock()
	e.pips.ipv4[601] = netip.MustParseAddr("198.19.77.8")
	e.pips.ipv6[602] = netip.MustParsePrefix("2001:db8:9000:2::/64")
	e.pips.mu.Unlock()
	withPIPs := lbBody("held")
	withPIPs["public_net"] = map[string]any{"ipv4": 601, "ipv6": 602}
	held := createLB(t, e, withPIPs)
	// The held LB's entries lie in the /64 that would be its old formal
	// one: a primary IP with the same ID as the LB.
	heldEntries := []rdns.Entry{
		{IP: oldFormal(withInterfaceID(new6, 1), held.ID).String(), DNSPtr: "held.example.com"},
		{IP: held.ipv6(), DNSPtr: "held6.example.com"},
	}

	// The state of phase 08: entries stored on the old formal IPv6.
	setPTRs(t, e, lb.ID, []rdns.Entry{
		{IP: old6.String(), DNSPtr: "lb6.example.com"},
		{IP: oldOther.String(), DNSPtr: "other.example.com"},
		{IP: lb.ipv4(), DNSPtr: "lb.example.com"},
	})
	setPTRs(t, e, held.ID, heldEntries)
	// The pending state of an action a stop interrupted.
	setPending(t, e, held.ID, &loadbalancers.Pending{Action: 99, Services: []loadbalancers.Service{}, Targets: []loadbalancers.Target{}})

	if err := rec.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if err := e.Store.Tx(func(tx store.Tx) error {
		r, err := store.Get[loadbalancers.Record](tx, loadbalancers.Kind, e.Project.ID, held.ID)
		if err == nil && r.Hlab.Pending != nil {
			t.Errorf("pending state after Recover: %+v, want none", r.Hlab.Pending)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	want := []rdns.Entry{
		{IP: new6.String(), DNSPtr: "lb6.example.com"},
		{IP: newOther.String(), DNSPtr: "other.example.com"},
		{IP: lb.ipv4(), DNSPtr: "lb.example.com"},
	}
	if got := ptrsOf(t, e, lb.ID); !sameEntries(got, want) {
		t.Errorf("dns_ptr after Recover: %+v, want %+v", got, want)
	}
	if got := mustLB(t, e, lb.ID).PublicNet.IPv6.DNSPtr; strOf(got) != "lb6.example.com" {
		t.Errorf("ipv6 dns_ptr after Recover %s, want lb6.example.com", strOf(got))
	}
	if got := ptrsOf(t, e, held.ID); !sameEntries(got, heldEntries) {
		t.Errorf("dns_ptr of the LB with an IPv6 primary IP after Recover: %+v, want unchanged %+v", got, heldEntries)
	}

	if err := rec.Recover(context.Background()); err != nil {
		t.Fatalf("second Recover: %v", err)
	}
	if got := ptrsOf(t, e, lb.ID); !sameEntries(got, want) {
		t.Errorf("dns_ptr after a second Recover: %+v, want unchanged %+v", got, want)
	}
	if got := ptrsOf(t, e, held.ID); !sameEntries(got, heldEntries) {
		t.Errorf("dns_ptr of the LB with an IPv6 primary IP after a second Recover: %+v, want unchanged %+v", got, heldEntries)
	}
}

// oldFormal is a in the formal /64 of phase 08, 2001:db8:<0x80000000|id>.
func oldFormal(a netip.Addr, id int64) netip.Addr {
	b := a.As16()
	binary.BigEndian.PutUint32(b[4:8], 0x80000000|uint32(id))
	return netip.AddrFrom16(b)
}

// withInterfaceID is a with its last 64 bits set to iid.
func withInterfaceID(a netip.Addr, iid uint64) netip.Addr {
	b := a.As16()
	binary.BigEndian.PutUint64(b[8:], iid)
	return netip.AddrFrom16(b)
}

// setPTRs replaces the stored dns_ptr entries of load balancer id.
func setPTRs(t *testing.T, e *lbEnv, id int64, entries []rdns.Entry) {
	t.Helper()
	err := e.Store.Tx(func(tx store.Tx) error {
		r, err := store.Get[loadbalancers.Record](tx, loadbalancers.Kind, e.Project.ID, id)
		if err != nil {
			return err
		}
		r.DNSPtr = entries
		return store.Put(tx, store.Meta{Kind: loadbalancers.Kind, ID: id, ProjectID: e.Project.ID, Name: r.Name, Labels: r.Labels, Created: r.Created}, r)
	})
	if err != nil {
		t.Fatalf("store dns_ptr of load balancer %d: %v", id, err)
	}
}

func setPending(t *testing.T, e *lbEnv, id int64, p *loadbalancers.Pending) {
	t.Helper()
	err := e.Store.Tx(func(tx store.Tx) error {
		r, err := store.Get[loadbalancers.Record](tx, loadbalancers.Kind, e.Project.ID, id)
		if err != nil {
			return err
		}
		r.Hlab.Pending = p
		return store.Put(tx, store.Meta{Kind: loadbalancers.Kind, ID: id, ProjectID: e.Project.ID, Name: r.Name, Labels: r.Labels, Created: r.Created}, r)
	})
	if err != nil {
		t.Fatalf("store pending state of load balancer %d: %v", id, err)
	}
}

func ptrsOf(t *testing.T, e *lbEnv, id int64) []rdns.Entry {
	t.Helper()
	var r loadbalancers.Record
	err := e.Store.Tx(func(tx store.Tx) error {
		var err error
		r, err = store.Get[loadbalancers.Record](tx, loadbalancers.Kind, e.Project.ID, id)
		return err
	})
	if err != nil {
		t.Fatalf("read load balancer %d: %v", id, err)
	}
	return r.DNSPtr
}

// sameEntries compares entries regardless of order and of the textual form
// of the addresses.
func sameEntries(got, want []rdns.Entry) bool {
	norm := func(es []rdns.Entry) []string {
		out := make([]string, 0, len(es))
		for _, e := range es {
			ip := e.IP
			if a, err := netip.ParseAddr(ip); err == nil {
				ip = a.String()
			}
			out = append(out, ip+" "+e.DNSPtr)
		}
		slices.Sort(out)
		return out
	}
	return slices.Equal(norm(got), norm(want))
}

// startedAction expects 201 for the action request and returns the
// action, which is still running.
func startedAction(t *testing.T, resp *http.Response, what string) apitest.Action {
	t.Helper()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("%s: status %d (%s), want 201", what, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var b struct {
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &b)
	if b.Action.Status != "running" {
		t.Errorf("%s: action status %q, want running", what, b.Action.Status)
	}
	return b.Action
}

// inOrder lets the held push of first through and checks that the work of
// second starts only after first succeeded; it returns the release of the
// push of second.
func inOrder(t *testing.T, e *lbEnv, releaseFirst func(), first, second apitest.Action) func() {
	t.Helper()
	releaseFirst()
	releaseSecond := e.gate.Next(t, second.Command+" queued behind "+first.Command)
	if got := e.For(t).GetAction(first.ID); got.Status != "success" {
		t.Errorf("%s %d when the queued %s %d worked: %q, want success", first.Command, first.ID, second.Command, second.ID, got.Status)
	}
	return releaseSecond
}

func waitSuccess(t *testing.T, e *lbEnv, a apitest.Action, what string) {
	t.Helper()
	if got := e.For(t).WaitAction(a.ID); got.Status != "success" {
		t.Fatalf("%s: action %q %+v, want success", what, got.Status, got.Error)
	}
}

// testQueue: target and service actions on one load balancer queue;
// change_type and a second attach still refuse.
func testQueue(t *testing.T) {
	e := newEnv(t)
	n := createNetwork(t, e, "n", "10.0.0.0/16", "10.0.1.0/24")
	n2 := createNetwork(t, e, "n2", "10.1.0.0/16", "10.1.1.0/24")
	lb := createLB(t, e, lbBody("queue"))
	s1 := createServer(t, e, "s1", "fsn1", nil)
	s2 := createServer(t, e, "s2", "fsn1", nil)
	e.gate.Close()

	t1 := startedAction(t, lbAction(e, lb.ID, "add_target", serverTargetBody(s1.ID, false)), "add_target s1")
	release := e.gate.Next(t, "add_target s1")
	t2 := startedAction(t, lbAction(e, lb.ID, "add_target", serverTargetBody(s2.ID, false)), "add_target s2 while s1 is added")
	wantError(t, lbAction(e, lb.ID, "change_type", map[string]any{"load_balancer_type": "lb21"}),
		http.StatusLocked, "locked", "change_type while a target action runs")
	inOrder(t, e, release, t1, t2)()
	waitSuccess(t, e, t2, "add_target s2")

	sv1 := startedAction(t, lbAction(e, lb.ID, "add_service", tcpService(80, 80)), "add_service 80")
	release = e.gate.Next(t, "add_service 80")
	sv2 := startedAction(t, lbAction(e, lb.ID, "add_service", tcpService(443, 443)), "add_service 443 while 80 is added")
	inOrder(t, e, release, sv1, sv2)()
	waitSuccess(t, e, sv2, "add_service 443")

	a := startedAction(t, lbAction(e, lb.ID, "attach_to_network", map[string]any{"network": n}), "attach_to_network")
	release = e.gate.Next(t, "attach_to_network")
	wantError(t, lbAction(e, lb.ID, "attach_to_network", map[string]any{"network": n2}),
		http.StatusLocked, "locked", "second attach_to_network of the load balancer")
	release()
	waitSuccess(t, e, a, "attach_to_network")
	e.gate.Open()

	got := mustLB(t, e, lb.ID)
	for _, id := range []int64{s1.ID, s2.ID} {
		if _, ok := got.serverTarget(id); !ok {
			t.Errorf("targets %+v, want server %d", got.Targets, id)
		}
	}
	for _, port := range []int{80, 443} {
		if _, ok := got.service(port); !ok {
			t.Errorf("services %+v, want listen port %d", got.Services, port)
		}
	}
}

// testEffect: targets, services and private_net appear on GET at success;
// a private target checks the committed network state.
func testEffect(t *testing.T) {
	e := newEnv(t)
	n := createNetwork(t, e, "n", "10.0.0.0/16", "10.0.1.0/24")
	lb := createLB(t, e, lbBody("effect"))
	s1 := createServer(t, e, "s1", "fsn1", nil)
	s2 := createServer(t, e, "s2", "fsn1", nil)
	runAction(t, e, e.Do(http.MethodPost, fmt.Sprintf("/servers/%d/actions/attach_to_network", s2.ID), map[string]any{"network": n}),
		"attach_to_network", "attach s2 to the network")
	e.gate.Close()

	a := startedAction(t, lbAction(e, lb.ID, "add_target", serverTargetBody(s1.ID, false)), "add_target")
	release := e.gate.Next(t, "add_target")
	if _, ok := mustLB(t, e, lb.ID).serverTarget(s1.ID); ok {
		t.Errorf("while add_target runs: target server %d shown, want not yet", s1.ID)
	}
	release()
	waitSuccess(t, e, a, "add_target")
	if _, ok := mustLB(t, e, lb.ID).serverTarget(s1.ID); !ok {
		t.Errorf("after add_target: no target server %d", s1.ID)
	}

	a = startedAction(t, lbAction(e, lb.ID, "add_service", tcpService(80, 80)), "add_service")
	release = e.gate.Next(t, "add_service")
	if _, ok := mustLB(t, e, lb.ID).service(80); ok {
		t.Errorf("while add_service runs: service 80 shown, want not yet")
	}
	release()
	waitSuccess(t, e, a, "add_service")
	if _, ok := mustLB(t, e, lb.ID).service(80); !ok {
		t.Errorf("after add_service: no service 80")
	}

	a = startedAction(t, lbAction(e, lb.ID, "attach_to_network", map[string]any{"network": n}), "attach_to_network")
	release = e.gate.Next(t, "attach_to_network")
	if got := mustLB(t, e, lb.ID).PrivateNet; len(got) != 0 {
		t.Errorf("while attach_to_network runs: private_net %+v, want none yet", got)
	}
	wantError(t, lbAction(e, lb.ID, "add_target", serverTargetBody(s2.ID, true)),
		http.StatusUnprocessableEntity, "load_balancer_not_attached_to_network", "private target while attach_to_network runs")
	release()
	waitSuccess(t, e, a, "attach_to_network")
	if got := mustLB(t, e, lb.ID).PrivateNet; len(got) != 1 || got[0].Network != n {
		t.Errorf("after attach_to_network: private_net %+v, want network %d", got, n)
	}
}

// testDeletedWhileQueued: a server target and the network deleted while a
// service action runs stay gone when that action commits, on GET; the
// network also in the agent document.
func testDeletedWhileQueued(t *testing.T) {
	e := newEnv(t)
	n := createNetwork(t, e, "n", "10.0.0.0/16", "10.0.1.0/24")
	lb := createLB(t, e, lbBody("deleted"))
	s1 := createServer(t, e, "s1", "fsn1", nil)
	runLBAction(t, e, lb.ID, "attach_to_network", map[string]any{"network": n})
	runLBAction(t, e, lb.ID, "add_target", serverTargetBody(s1.ID, false))
	e.gate.Close()

	a := startedAction(t, lbAction(e, lb.ID, "add_service", tcpService(80, 80)), "add_service")
	release := e.gate.Next(t, "add_service")
	resp := e.Do(http.MethodDelete, fmt.Sprintf("/servers/%d", s1.ID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /servers/%d: status %d (%s), want 200", s1.ID, resp.StatusCode, apitest.ErrorCode(t, resp))
	}
	var del struct {
		Action apitest.Action `json:"action"`
	}
	apitest.Decode(t, resp, &del)
	done := make(chan int, 1)
	go func() {
		resp := e.Do(http.MethodDelete, fmt.Sprintf("/networks/%d", n), nil)
		_ = resp.Body.Close()
		done <- resp.StatusCode
	}()
	deadline := time.Now().Add(apitest.ActionTimeout)
	for {
		resp := e.Do(http.MethodGet, fmt.Sprintf("/networks/%d", n), nil)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("network %d still there after %s", n, apitest.ActionTimeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.gate.Open()
	release()
	if code := <-done; code != http.StatusNoContent {
		t.Fatalf("DELETE /networks/%d: status %d, want 204", n, code)
	}
	waitSuccess(t, e, a, "add_service")
	waitSuccess(t, e, del.Action, "delete server")

	got := mustLB(t, e, lb.ID)
	if _, ok := got.serverTarget(s1.ID); ok {
		t.Errorf("after add_service: target of deleted server %d shown", s1.ID)
	}
	if len(got.PrivateNet) != 0 {
		t.Errorf("after add_service: private_net %+v of deleted network %d", got.PrivateNet, n)
	}
	if _, ok := got.service(80); !ok {
		t.Errorf("after add_service: no service 80")
	}
	e.waitDoc(t, "service 80 without the deleted network", func(doc agentapi.Config) bool {
		for _, l := range doc.LoadBalancers {
			if l.ID == lb.ID {
				return len(l.Services) == 1 && len(l.Private) == 0
			}
		}
		return false
	})
}
