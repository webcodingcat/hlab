package apitest

import (
	"context"
	"sync"
	"testing"
	"time"
)

// PushGate holds agent pushes while it is closed, so an action whose work
// pushes stays running until the test lets its push through. A module's
// PushAgent calls Wait first.
type PushGate struct {
	mu     sync.Mutex
	closed bool
	held   chan chan struct{}
}

// NewPushGate returns an open gate that opens again at the end of t.
func NewPushGate(t testing.TB) *PushGate {
	g := &PushGate{held: make(chan chan struct{}, 64)}
	t.Cleanup(g.Open)
	return g
}

// Wait is called by every push; while the gate is closed it waits until
// the test releases this push or ctx ends.
func (g *PushGate) Wait(ctx context.Context) error {
	g.mu.Lock()
	if !g.closed {
		g.mu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	g.held <- ch
	g.mu.Unlock()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close makes every later push wait.
func (g *PushGate) Close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}

// Next waits up to ActionTimeout for the next held push and returns its
// release, which also runs at the end of t.
func (g *PushGate) Next(t testing.TB, what string) func() {
	t.Helper()
	select {
	case ch := <-g.held:
		release := sync.OnceFunc(func() { close(ch) })
		t.Cleanup(release)
		return release
	case <-time.After(ActionTimeout):
		t.Fatalf("%s: no agent push within %s", what, ActionTimeout)
		return nil
	}
}

// Open lets every push through, the held ones included.
func (g *PushGate) Open() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = false
	for {
		select {
		case ch := <-g.held:
			close(ch)
		default:
			return
		}
	}
}
