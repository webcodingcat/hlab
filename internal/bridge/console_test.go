package bridge_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/webcodingcat/hlab/internal/bridge"
)

const consoleHost = "console.hetzner.internal"

// testCA is a certificate authority that no system store trusts.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T, name string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue returns a server certificate for host signed by the CA.
func (ca *testCA) issue(t *testing.T, host string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// consoleUpstream is a wss console server: it sends greeting, then echoes
// every binary message it receives and records what it got.
type consoleUpstream struct {
	addr     string
	greeting []byte
	// upgrades counts the WebSocket requests that reached the handler.
	upgrades atomic.Int32
	mu       sync.Mutex
	query    string
	received []byte
	types    []websocket.MessageType
}

func newConsoleUpstream(t *testing.T, cert tls.Certificate) *consoleUpstream {
	t.Helper()
	u := &consoleUpstream{greeting: []byte("RFB 003.008\n")}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(u.handle))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	u.addr = srv.Listener.Addr().String()
	return u
}

func (u *consoleUpstream) handle(w http.ResponseWriter, r *http.Request) {
	u.upgrades.Add(1)
	u.mu.Lock()
	u.query = r.URL.RawQuery
	u.mu.Unlock()
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	ctx, cancel := context.WithTimeout(r.Context(), waitTimeout)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageBinary, u.greeting); err != nil {
		return
	}
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		u.mu.Lock()
		u.received = append(u.received, data...)
		u.types = append(u.types, typ)
		u.mu.Unlock()
		if err := c.Write(ctx, websocket.MessageBinary, data); err != nil {
			return
		}
	}
}

// dialTo returns a ConsoleDeps.Dial that connects every address to addr
// and records the addresses asked for.
func dialTo(addr string, asked *[]string, mu *sync.Mutex) func(ctx context.Context, network, a string) (net.Conn, error) {
	return func(ctx context.Context, network, a string) (net.Conn, error) {
		mu.Lock()
		*asked = append(*asked, a)
		mu.Unlock()
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
}

// readBytes reads binary messages from c until n bytes arrived.
func readBytes(ctx context.Context, t *testing.T, c *websocket.Conn, n int) []byte {
	t.Helper()
	var got []byte
	for len(got) < n {
		typ, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read through the proxy after %d of %d bytes: %v", len(got), n, err)
		}
		if typ != websocket.MessageBinary {
			t.Fatalf("message type %v through the proxy, want binary", typ)
		}
		got = append(got, data...)
	}
	return got
}

func originHeader(origin string) http.Header {
	h := http.Header{}
	h.Set("Origin", origin)
	return h
}

func TestConsoleProxy(t *testing.T) {
	t.Run("UI-15", func(t *testing.T) {
		ca := newTestCA(t, "hlab test CA")
		up := newConsoleUpstream(t, ca.issue(t, consoleHost))
		var mu sync.Mutex
		var asked []string
		proxy := bridge.NewConsole(bridge.ConsoleDeps{
			CA:   func(context.Context) ([]byte, error) { return ca.pem, nil },
			Dial: dialTo(up.addr, &asked, &mu),
		})
		const query = "server_id=7&token=0b1e7c9a-4f3d-4c2b-9a8e-1d2c3b4a5f60"
		wssURL := "wss://" + consoleHost + "/?" + query

		t.Run("relay", func(t *testing.T) {
			local, err := proxy.Open(wssURL)
			if err != nil {
				t.Fatalf("Open(%s): %v", wssURL, err)
			}
			if !strings.HasPrefix(local, "ws://127.0.0.1:") {
				t.Fatalf("Open: local URL %q, want ws://127.0.0.1:<port>/<path>", local)
			}
			ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
			defer cancel()
			c, _, err := websocket.Dial(ctx, local, &websocket.DialOptions{HTTPHeader: originHeader("http://wails.localhost")})
			if err != nil {
				t.Fatalf("dial %s from origin wails.localhost: %v", local, err)
			}
			defer c.CloseNow()
			c.SetReadLimit(-1)

			if got := readBytes(ctx, t, c, len(up.greeting)); !bytes.Equal(got, up.greeting) {
				t.Errorf("server to client: got %q, want %q", got, up.greeting)
			}
			payload := make([]byte, 64*1024)
			for i := range payload {
				payload[i] = byte(i * 7)
			}
			if err := c.Write(ctx, websocket.MessageBinary, payload); err != nil {
				t.Fatalf("write through the proxy: %v", err)
			}
			reply := readBytes(ctx, t, c, len(payload))
			// The proxy may split or join messages; compare the bytes.
			up.mu.Lock()
			received := append([]byte(nil), up.received...)
			types := append([]websocket.MessageType(nil), up.types...)
			gotQuery := up.query
			up.mu.Unlock()
			if !bytes.Equal(received, payload) {
				t.Errorf("client to server: upstream got %d bytes, want the %d bytes sent", len(received), len(payload))
			}
			for _, typ := range types {
				if typ != websocket.MessageBinary {
					t.Errorf("upstream got message type %v, want binary", typ)
				}
			}
			if !bytes.Equal(reply, payload) {
				t.Errorf("server to client: echo of %d bytes differs from the %d bytes sent", len(reply), len(payload))
			}
			if gotQuery != query {
				t.Errorf("upstream query %q, want %q", gotQuery, query)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, a := range asked {
				if host, _, _ := net.SplitHostPort(a); host != consoleHost {
					t.Errorf("proxy dialed %q, want the host of the wss URL %s", a, consoleHost)
				}
			}
		})

		t.Run("foreign_ca", func(t *testing.T) {
			other := newTestCA(t, "other CA")
			foreign := newConsoleUpstream(t, other.issue(t, consoleHost))
			var fmu sync.Mutex
			var fasked []string
			p := bridge.NewConsole(bridge.ConsoleDeps{
				CA:   func(context.Context) ([]byte, error) { return ca.pem, nil },
				Dial: dialTo(foreign.addr, &fasked, &fmu),
			})
			local, err := p.Open(wssURL)
			if err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
				defer cancel()
				c, _, err := websocket.Dial(ctx, local, &websocket.DialOptions{HTTPHeader: originHeader("http://wails.localhost")})
				if err == nil {
					defer c.CloseNow()
					if _, data, err := c.Read(ctx); err == nil {
						t.Errorf("proxy relayed %q from a server certificate of another CA", data)
					}
				}
			}
			if n := foreign.upgrades.Load(); n != 0 {
				t.Errorf("server with a certificate of another CA got %d WebSocket requests, want 0", n)
			}
		})

		t.Run("foreign_origin", func(t *testing.T) {
			local, err := proxy.Open(wssURL)
			if err != nil {
				t.Fatalf("Open(%s): %v", wssURL, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
			defer cancel()
			c, resp, err := websocket.Dial(ctx, local, &websocket.DialOptions{HTTPHeader: originHeader("http://evil.example")})
			if err == nil {
				defer c.CloseNow()
				t.Fatalf("dial %s from origin evil.example: accepted, want refused", local)
			}
			if resp != nil && resp.StatusCode == http.StatusSwitchingProtocols {
				t.Errorf("dial from origin evil.example: status 101, want refused")
			}
		})
	})
}
