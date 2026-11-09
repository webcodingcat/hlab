package fake

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"

	"github.com/webcodingcat/hlab/internal/agentproto"
)

// Agent stands in for hlab-agent in the appliance of the fake machine: it
// takes every picture that decodes and passes its check, and reports the
// generations it took, as the real agent does. Serve it with
// Fake.ServeAgent.
type Agent struct {
	handler http.Handler

	mu          sync.Mutex
	generations map[string]uint64
	pictures    map[string][]byte
	holds       map[string]*hold // by picture: the next put waits (HoldNext)
	failures    map[string]bool  // by picture: the next put fails (FailNext)
	puts        map[string]int   // by picture: the puts that arrived, refused ones too
}

// hold makes a put wait: waiting closes when it arrives, the put goes on
// when release closes, and applied closes when it is done.
type hold struct {
	waiting, release, applied chan struct{}
}

// NewAgent returns an agent that has applied no picture yet.
func NewAgent() *Agent {
	a := &Agent{generations: map[string]uint64{}, pictures: map[string][]byte{}, holds: map[string]*hold{}, failures: map[string]bool{}, puts: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+agentproto.PathStatus, a.status)
	mux.HandleFunc("PUT "+agentproto.PathPictures+"{name}", a.putPicture)
	a.handler = mux
	return a
}

// Serve answers HTTP requests on one connection, as Fake.ServeAgent hands
// it over.
func (a *Agent) Serve(conn net.Conn) {
	server := &http.Server{Handler: a.handler}
	// Serve returns when the listener has no second connection; the
	// connection's own goroutine answers until the client closes it
	_ = server.Serve(&oneConnListener{conn: conn})
}

// Restart forgets every picture, as the agent of a restarted appliance has
// none.
func (a *Agent) Restart() {
	a.mu.Lock()
	defer a.mu.Unlock()
	clear(a.generations)
	clear(a.pictures)
}

// Picture returns the body of the named picture it applied last, or nil.
func (a *Agent) Picture(name string) []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pictures[name]
}

// HoldNext makes the next put of the named picture wait. waiting closes
// when it arrives; release lets it go on and returns once it is applied.
// A test uses it to send another picture while the first is on its way.
func (a *Agent) HoldNext(name string) (waiting <-chan struct{}, release func()) {
	next := &hold{waiting: make(chan struct{}), release: make(chan struct{}), applied: make(chan struct{})}
	a.mu.Lock()
	a.holds[name] = next
	a.mu.Unlock()
	return next.waiting, func() {
		close(next.release)
		<-next.applied
	}
}

// Puts returns how many puts of the named picture arrived, refused and
// failed ones too.
func (a *Agent) Puts(name string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.puts[name]
}

// FailNext makes the next put of the named picture fail with 503, as an
// agent that cannot apply it now; a refused picture (422) would not fail
// the sender. A test uses it to make an action's steps fail after the
// pictures before it were applied.
func (a *Agent) FailNext(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures[name] = true
}

// status answers with the generations applied.
func (a *Agent) status(w http.ResponseWriter, _ *http.Request) {
	a.mu.Lock()
	status := agentproto.Status{Version: "fake", Generations: map[string]uint64{}}
	for name, generation := range a.generations {
		status.Generations[name] = generation
	}
	a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

// putPicture checks a picture as the agent does and keeps it.
func (a *Agent) putPicture(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body bytes.Buffer
	if _, err := body.ReadFrom(r.Body); err != nil {
		return // the client went away
	}
	var generation uint64
	var err error
	switch name {
	case agentproto.PictureLinks:
		generation, err = check[agentproto.Links](body.Bytes())
	case agentproto.PictureRuleset:
		generation, err = check[agentproto.Ruleset](body.Bytes())
	case agentproto.PictureDHCP:
		generation, err = check[agentproto.DHCP](body.Bytes())
	case agentproto.PictureMetadata:
		generation, err = check[agentproto.Metadata](body.Bytes())
	default:
		http.Error(w, "no such picture", http.StatusNotFound)
		return
	}
	a.mu.Lock()
	a.puts[name]++
	failing := a.failures[name]
	delete(a.failures, name)
	a.mu.Unlock()
	if failing {
		http.Error(w, "failing as the test asked", http.StatusServiceUnavailable)
		return
	}
	var refusal *refusedError
	switch {
	case errors.As(err, &refusal):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.mu.Lock()
	held := a.holds[name]
	delete(a.holds, name)
	a.mu.Unlock()
	if held != nil {
		close(held.waiting)
		<-held.release
		defer close(held.applied)
	}
	a.mu.Lock()
	a.generations[name] = generation
	a.pictures[name] = body.Bytes()
	a.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// picture is a picture of agentproto: it checks itself and carries a
// generation.
type picture interface {
	agentproto.Links | agentproto.Ruleset | agentproto.DHCP | agentproto.Metadata
	Check() error
}

// refusedError is a picture that decodes but fails its check (422).
type refusedError struct{ err error }

func (e *refusedError) Error() string { return e.err.Error() }

// check decodes a picture strictly, as the agent does, checks it and
// returns its generation.
func check[P picture](body []byte) (uint64, error) {
	var wanted P
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wanted); err != nil {
		return 0, err
	}
	if err := wanted.Check(); err != nil {
		return 0, &refusedError{err: err}
	}
	var header struct {
		Generation uint64 `json:"generation"`
	}
	if err := json.Unmarshal(body, &header); err != nil {
		return 0, err
	}
	return header.Generation, nil
}

// oneConnListener hands out one connection, then reports that it is closed.
type oneConnListener struct {
	mu   sync.Mutex
	conn net.Conn
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conn == nil {
		return nil, net.ErrClosed
	}
	conn := l.conn
	l.conn = nil
	return conn, nil
}

func (l *oneConnListener) Close() error { return nil }

func (l *oneConnListener) Addr() net.Addr { return pipeAddr{} }

// pipeAddr is the address of an in-memory pipe.
type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }
