package console

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/webcodingcat/hlab/internal/ca"
	"github.com/webcodingcat/hlab/internal/platform"
)

const consoleHost = "console.hetzner.internal"

// serviceEnv runs a Service on a loopback listener with a certificate of a
// test CA and fake consoles per VM.
type serviceEnv struct {
	svc   *Service
	clk   *fakeClock
	addr  string
	roots *x509.CertPool

	mu      sync.Mutex
	screens map[string]platform.Frame
	opened  map[string][]*fakeConn
}

func newServiceEnv(t *testing.T) *serviceEnv {
	t.Helper()
	authority, err := ca.Open(t.TempDir(), "hlab test CA", nil)
	if err != nil {
		t.Fatalf("open CA: %v", err)
	}
	certPEM, keyPEM, err := authority.Issue([]string{consoleHost})
	if err != nil {
		t.Fatalf("issue certificate: %v", err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("key pair: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(authority.Certificate()) {
		t.Fatalf("CA certificate not loaded")
	}

	e := &serviceEnv{
		clk:   newFakeClock(),
		roots: roots,
		screens: map[string]platform.Frame{
			"vm-1": solidFrame(16, 8, red565),
			"vm-2": solidFrame(16, 8, green565),
			"vm-7": patternFrame(16, 8),
		},
		opened: map[string][]*fakeConn{},
	}
	e.svc = New(Config{
		Host: consoleHost,
		Cert: cert,
		Open: func(_ context.Context, vm string) (platform.ConsoleConn, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			f, ok := e.screens[vm]
			if !ok {
				f = solidFrame(16, 8, gray565)
			}
			c := newFakeConn(f)
			e.opened[vm] = append(e.opened[vm], c)
			return c, nil
		},
		Clock:   e.clk,
		Session: SessionOptions{Interval: 10 * time.Millisecond},
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	e.addr = ln.Addr().String()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = e.svc.Serve(ln)
	}()
	t.Cleanup(func() {
		_ = e.svc.Close()
		select {
		case <-done:
		case <-time.After(stepTimeout):
			t.Errorf("Serve did not return after Close")
		}
	})
	return e
}

// conn returns the last console opened for vm.
func (e *serviceEnv) conn(t *testing.T, vm string) *fakeConn {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.opened[vm]) == 0 {
		t.Fatalf("no console opened for %s", vm)
	}
	return e.opened[vm][len(e.opened[vm])-1]
}

func (e *serviceEnv) openCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, cs := range e.opened {
		n += len(cs)
	}
	return n
}

// dial opens the WebSocket of wssURL over TLS that trusts only the test CA;
// the host name of the URL is kept for TLS, the TCP connection goes to the
// listener.
func (e *serviceEnv) dial(t *testing.T, wssURL string) (net.Conn, *http.Response, error) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: e.roots},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, e.addr)
		},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), stepTimeout)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, wssURL, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		return nil, resp, err
	}
	conn := websocket.NetConn(context.Background(), ws, websocket.MessageBinary)
	t.Cleanup(func() { _ = conn.Close() })
	return conn, resp, nil
}

// login dials wssURL and logs in to the RFB session.
func (e *serviceEnv) login(t *testing.T, wssURL, password string) *rfbClient {
	t.Helper()
	conn, _, err := e.dial(t, wssURL)
	if err != nil {
		t.Fatalf("dial %s: %v", wssURL, err)
	}
	c := newRFBClient(t, conn)
	c.login(password)
	c.setPixelFormat(noVNCFormat)
	c.setEncodings(encRaw, encDesktopSize)
	return c
}

func (e *serviceEnv) issue(t *testing.T, serverID int64, vm string) (string, string) {
	t.Helper()
	wssURL, password, err := e.svc.Issue(serverID, vm)
	if err != nil {
		t.Fatalf("Issue(%d, %s): %v", serverID, vm, err)
	}
	return wssURL, password
}

func TestService(t *testing.T) {
	t.Run("CON-01/connect", func(t *testing.T) {
		e := newServiceEnv(t)
		wssURL, password := e.issue(t, 7, "vm-7")
		urlRE := regexp.MustCompile(`^wss://console\.hetzner\.internal/\?server_id=7&token=[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
		if !urlRE.MatchString(wssURL) {
			t.Errorf("wss_url %q, want wss://console.hetzner.internal/?server_id=7&token=<uuid>", wssURL)
		}
		if !regexp.MustCompile(`^[A-Za-z0-9]{32}$`).MatchString(password) {
			t.Errorf("password %q, want 32 letters and digits", password)
		}

		c := e.login(t, wssURL, password)
		c.waitScreen(e.screens["vm-7"], false, nil)

		// The token's minute passes; the open connection keeps working.
		e.clk.Advance(2 * time.Minute)
		src := e.conn(t, "vm-7")
		c.key(true, 'a')
		eventually(t, "the key at the server's console", func() bool {
			return string(src.scancodes()) == string([]byte{0x1E})
		})
		next := solidFrame(16, 8, white565)
		src.setScreen(next, true)
		c.waitScreen(next, true, nil)
	})

	t.Run("CON-01/close_server", func(t *testing.T) {
		e := newServiceEnv(t)
		url1, pw1 := e.issue(t, 1, "vm-1")
		url2, pw2 := e.issue(t, 2, "vm-2")
		c1 := e.login(t, url1, pw1)
		c2 := e.login(t, url2, pw2)
		c1.waitScreen(e.screens["vm-1"], false, nil)
		c2.waitScreen(e.screens["vm-2"], false, nil)

		e.svc.CloseServer(1)
		c1.expectClosed()

		next := solidFrame(16, 8, white565)
		e.conn(t, "vm-2").setScreen(next, true)
		c2.waitScreen(next, true, nil)
	})

	t.Run("CON-02/token", func(t *testing.T) {
		withQuery := func(t *testing.T, raw, key, value string) string {
			t.Helper()
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("parse %q: %v", raw, err)
			}
			q := u.Query()
			q.Set(key, value)
			u.RawQuery = q.Encode()
			return u.String()
		}
		cases := []struct {
			name string
			url  func(t *testing.T, e *serviceEnv) string
		}{
			{name: "unknown", url: func(t *testing.T, e *serviceEnv) string {
				u, _ := e.issue(t, 3, "vm-3")
				return withQuery(t, u, "token", "0b5c7e2a-3f41-4d8e-9a6b-1c2d3e4f5a6b")
			}},
			{name: "expired", url: func(t *testing.T, e *serviceEnv) string {
				u, _ := e.issue(t, 4, "vm-4")
				e.clk.Advance(61 * time.Second)
				return u
			}},
			{name: "other_server", url: func(t *testing.T, e *serviceEnv) string {
				u, _ := e.issue(t, 5, "vm-5")
				e.issue(t, 6, "vm-6")
				return withQuery(t, u, "server_id", strconv.Itoa(6))
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				e := newServiceEnv(t)
				_, resp, err := e.dial(t, tc.url(t, e))
				if err == nil {
					t.Fatalf("WebSocket opened, want 403 before the upgrade")
				}
				if resp == nil {
					t.Fatalf("no HTTP response: %v", err)
				}
				if resp.StatusCode != http.StatusForbidden {
					t.Errorf("status %d, want 403", resp.StatusCode)
				}
				if n := e.openCount(); n != 0 {
					t.Errorf("%d consoles opened for a refused token", n)
				}
			})
		}
	})
}
