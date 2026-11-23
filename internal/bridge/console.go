package bridge

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// consoleUnused is how long a console proxy waits for its one connection.
const consoleUnused = time.Minute

// consoleDialTimeout bounds fetching the CA and dialing the console.
const consoleDialTimeout = 30 * time.Second

// consoleOrigin is the origin of the tray's pages on Windows (E22).
const consoleOrigin = "wails.localhost"

// ConsoleDeps are the parts Console uses.
type ConsoleDeps struct {
	CA func(ctx context.Context) ([]byte, error) // hlab CA PEM, GET /mgmt/v1/ca.pem
	// Dial overrides the TCP dial in tests; nil = net.Dialer.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// Console opens loopback WebSocket proxies from the console window to the
// console's wss URL, so the window works while Windows does not trust the
// hlab CA.
type Console struct {
	d ConsoleDeps
}

// NewConsole returns a Console.
func NewConsole(d ConsoleDeps) *Console { return &Console{d: d} }

// Open starts a one-connection loopback proxy and returns
// ws://127.0.0.1:<port>/<random>; it accepts only origin wails.localhost
// and dials wssURL trusting only the hlab CA, ServerName from the URL.
// The listener closes after its connection or after a minute unused.
func (c *Console) Open(wssURL string) (string, error) {
	u, err := url.Parse(wssURL)
	if err != nil || u.Scheme != "wss" || u.Hostname() == "" {
		return "", fmt.Errorf("console URL %q is not a wss URL", wssURL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), consoleDialTimeout)
	defer cancel()
	caPEM, err := c.d.CA(ctx)
	if err != nil {
		return "", fmt.Errorf("load the hlab CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return "", errors.New("the hlab CA holds no certificate")
	}
	var secret [16]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", err
	}
	path := "/" + hex.EncodeToString(secret[:])

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	p := &consoleProxy{
		ln:     ln,
		path:   path,
		wssURL: wssURL,
		tls:    &tls.Config{RootCAs: pool, ServerName: u.Hostname(), MinVersion: tls.VersionTLS12},
		dial:   c.d.Dial,
	}
	p.timer = time.AfterFunc(consoleUnused, p.closeListener)
	srv := &http.Server{Handler: p, ReadHeaderTimeout: consoleDialTimeout}
	go func() { _ = srv.Serve(ln) }()
	return "ws://" + ln.Addr().String() + path, nil
}

// consoleProxy serves one WebSocket connection and relays it to wssURL.
type consoleProxy struct {
	ln     net.Listener
	path   string
	wssURL string
	tls    *tls.Config
	dial   func(ctx context.Context, network, addr string) (net.Conn, error)
	timer  *time.Timer

	mu   sync.Mutex
	used bool
}

func (p *consoleProxy) closeListener() { _ = p.ln.Close() }

func (p *consoleProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != p.path {
		http.NotFound(w, r)
		return
	}
	p.mu.Lock()
	used := p.used
	p.mu.Unlock()
	if used {
		http.Error(w, "used", http.StatusGone)
		return
	}
	client, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{consoleOrigin}})
	if err != nil {
		return // Accept has answered, a foreign origin with 403
	}
	defer func() { _ = client.CloseNow() }()
	p.mu.Lock()
	used, p.used = p.used, true
	p.mu.Unlock()
	if used {
		_ = client.Close(websocket.StatusPolicyViolation, "the console connection is in use")
		return
	}
	p.timer.Stop()
	p.closeListener()

	ctx, cancel := context.WithTimeout(context.Background(), consoleDialTimeout)
	upstream, err := p.dialUpstream(ctx)
	cancel()
	if err != nil {
		_ = client.Close(websocket.StatusInternalError, truncateReason("console: "+err.Error()))
		return
	}
	defer func() { _ = upstream.CloseNow() }()
	relay(client, upstream)
}

// dialUpstream connects to the console trusting only the hlab CA.
func (p *consoleProxy) dialUpstream(ctx context.Context) (*websocket.Conn, error) {
	dial := p.dial
	if dial == nil {
		var d net.Dialer
		dial = d.DialContext
	}
	hc := &http.Client{Transport: &http.Transport{
		DialContext:     dial,
		TLSClientConfig: p.tls,
	}}
	c, _, err := websocket.Dial(ctx, p.wssURL, &websocket.DialOptions{HTTPClient: hc})
	return c, err
}

// relayChunk is the largest message the proxy sends: RFB is a byte
// stream, so a message is split to stay within the peer's default read
// limit of 32 KiB.
const relayChunk = 32 * 1024

// relay copies messages both ways until one side ends, then closes both.
func relay(a, b *websocket.Conn) {
	a.SetReadLimit(-1)
	b.SetReadLimit(-1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{}, 2)
	copyMessages := func(dst, src *websocket.Conn) {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, relayChunk)
		for {
			typ, rd, err := src.Reader(ctx)
			if err != nil {
				return
			}
			for {
				n, err := io.ReadFull(rd, buf)
				if n > 0 {
					if werr := dst.Write(ctx, typ, buf[:n]); werr != nil {
						return
					}
				}
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					break
				}
				if err != nil {
					return
				}
			}
		}
	}
	go copyMessages(b, a)
	go copyMessages(a, b)
	<-done
	cancel()
	_ = a.CloseNow()
	_ = b.CloseNow()
	<-done
}

// truncateReason keeps a close reason within the 123 bytes WebSocket
// allows.
func truncateReason(s string) string {
	if len(s) > 123 {
		return s[:123]
	}
	return s
}
