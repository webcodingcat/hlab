package console

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/webcodingcat/hlab/internal/platform"
	"github.com/webcodingcat/hlab/internal/scheduler"
)

// tokenTTL is how long an issued URL may be used to start a connection.
const tokenTTL = time.Minute

const (
	passwordLength   = 32
	passwordAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

// Config configures a Service.
type Config struct {
	Host    string          // "console.hetzner.internal"
	Cert    tls.Certificate // issued by the hlab CA at cloud start
	Open    func(ctx context.Context, vm string) (platform.ConsoleConn, error)
	Clock   scheduler.Clock // nil = real clock; token TTL one minute
	Session SessionOptions
}

// Service issues console URLs and serves them as RFB over WebSocket.
type Service struct {
	cfg   Config
	clock scheduler.Clock
	base  context.Context
	stop  context.CancelFunc

	mu       sync.Mutex
	tokens   map[string]ticket
	sessions map[int64]map[*int]context.CancelFunc
	srv      *http.Server
	closed   bool
	wg       sync.WaitGroup
}

// ticket is an issued, not yet used token.
type ticket struct {
	serverID int64
	vm       string
	password string
	expires  time.Time
}

// New returns a Service; Issue works without Cert and Open.
func New(cfg Config) *Service {
	clk := cfg.Clock
	if clk == nil {
		clk = scheduler.RealClock()
	}
	base, stop := context.WithCancel(context.Background())
	return &Service{
		cfg:      cfg,
		clock:    clk,
		base:     base,
		stop:     stop,
		tokens:   map[string]ticket{},
		sessions: map[int64]map[*int]context.CancelFunc{},
	}
}

// Issue returns wss://<Host>/?server_id=<id>&token=<uuid> and a password
// of 32 random letters and digits; the token is valid for one connection
// started within one minute.
func (s *Service) Issue(serverID int64, vm string) (wssURL, password string, err error) {
	token, err := newUUID()
	if err != nil {
		return "", "", err
	}
	password, err = newPassword()
	if err != nil {
		return "", "", err
	}
	now := s.clock.Now()
	s.mu.Lock()
	for t, tk := range s.tokens {
		if now.After(tk.expires) {
			delete(s.tokens, t)
		}
	}
	s.tokens[token] = ticket{serverID: serverID, vm: vm, password: password, expires: now.Add(tokenTTL)}
	s.mu.Unlock()
	return fmt.Sprintf("wss://%s/?server_id=%d&token=%s", s.cfg.Host, serverID, token), password, nil
}

// take consumes the token if it is known, unexpired and issued for serverID.
func (s *Service) take(serverID int64, token string) (ticket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tk, ok := s.tokens[token]
	if !ok || tk.serverID != serverID || s.clock.Now().After(tk.expires) {
		return ticket{}, false
	}
	delete(s.tokens, token)
	return tk, true
}

// register records an open console of serverID; false after Close.
func (s *Service) register(serverID int64, key *int, cancel context.CancelFunc) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if s.sessions[serverID] == nil {
		s.sessions[serverID] = map[*int]context.CancelFunc{}
	}
	s.sessions[serverID][key] = cancel
	s.wg.Add(1)
	return true
}

func (s *Service) unregister(serverID int64, key *int) {
	s.mu.Lock()
	delete(s.sessions[serverID], key)
	if len(s.sessions[serverID]) == 0 {
		delete(s.sessions, serverID)
	}
	s.mu.Unlock()
	s.wg.Done()
}

// Handler upgrades a request with a valid token to a WebSocket and serves
// the server's console on it; a bad token gets 403 before the upgrade.
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		serverID, err := strconv.ParseInt(q.Get("server_id"), 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		tk, ok := s.take(serverID, q.Get("token"))
		if !ok {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		// Outside clients (hcloud users' own VNC clients) send any origin.
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		ctx, cancel := context.WithCancel(s.base)
		defer cancel()
		key := new(int)
		if !s.register(serverID, key, cancel) {
			_ = ws.Close(websocket.StatusGoingAway, "")
			return
		}
		defer s.unregister(serverID, key)

		src, err := s.cfg.Open(ctx, tk.vm)
		if err != nil {
			_ = ws.Close(websocket.StatusInternalError, "console unavailable")
			return
		}
		defer func() { _ = src.Close() }()
		conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)
		_ = Serve(ctx, conn, src, tk.password, s.cfg.Session)
	})
}

// Serve accepts TLS connections with Cert on ln and serves Handler; it
// returns nil after Close.
func (s *Service) Serve(ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{s.cfg.Cert}, MinVersion: tls.VersionTLS12},
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = ln.Close()
		return nil
	}
	s.srv = srv
	s.mu.Unlock()
	err := srv.ServeTLS(ln, "", "")
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// CloseServer ends the open consoles of serverID and drops its unused
// tokens.
func (s *Service) CloseServer(serverID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cancel := range s.sessions[serverID] {
		cancel()
	}
	for t, tk := range s.tokens {
		if tk.serverID == serverID {
			delete(s.tokens, t)
		}
	}
}

// Close ends the listener and all open consoles and waits for them.
func (s *Service) Close() error {
	s.mu.Lock()
	s.closed = true
	srv := s.srv
	s.tokens = map[string]ticket{}
	s.mu.Unlock()
	s.stop()
	var err error
	if srv != nil {
		err = srv.Close()
	}
	s.wg.Wait()
	return err
}

// newUUID returns a random (version 4) UUID in lower case.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// newPassword returns passwordLength random letters and digits.
func newPassword() (string, error) {
	n := big.NewInt(int64(len(passwordAlphabet)))
	b := make([]byte, passwordLength)
	for i := range b {
		v, err := rand.Int(rand.Reader, n)
		if err != nil {
			return "", err
		}
		b[i] = passwordAlphabet[v.Int64()]
	}
	return string(b), nil
}
