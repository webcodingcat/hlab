package bridge

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/webcodingcat/hlab/internal/logfile"
	"github.com/webcodingcat/hlab/internal/mgmtclient"
)

// logLines is how many of the last lines a follow starts with, as many as
// GET /mgmt/v1/logs replays.
const logLines = 1000

// LogsDeps are the parts Logs uses.
type LogsDeps struct {
	Client   *mgmtclient.Client
	LogDir   string
	Running  func() bool
	Emit     func(lines []string) // one "logs" event per batch
	Interval time.Duration        // reconnect interval (2 s)
	Batch    time.Duration        // 100 ms
}

// Logs follows hlabd's log: through the management API while the cloud
// runs, from the log files while it is stopped.
type Logs struct {
	d LogsDeps

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewLogs returns Logs that follows nothing yet.
func NewLogs(d LogsDeps) *Logs { return &Logs{d: d} }

// Follow with on emits the last 1000 lines, then new ones, until Follow is
// called again; a running follow ends first. Without on it only ends it.
func (l *Logs) Follow(on bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancel != nil {
		l.cancel()
		<-l.done
		l.cancel, l.done = nil, nil
	}
	if !on {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	l.cancel, l.done = cancel, done
	go func() {
		defer close(done)
		l.run(ctx)
	}()
}

// run reads from the stream or the files, whichever fits the cloud state,
// switches when the state changes and reconnects a broken stream.
func (l *Logs) run(ctx context.Context) {
	f := &follow{maxWait: l.d.Interval}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		f.flushEvery(ctx, l.d.Batch, l.d.Emit)
	}()
	defer wg.Wait()

	for ctx.Err() == nil {
		running := l.d.Running()
		sub, cancel := context.WithCancel(ctx)
		var switched atomic.Bool
		go func() {
			t := time.NewTicker(l.d.Interval)
			defer t.Stop()
			for {
				select {
				case <-sub.Done():
					return
				case <-t.C:
					if l.d.Running() != running {
						switched.Store(true)
						cancel()
						return
					}
				}
			}
		}()

		f.resync()
		if running {
			_ = l.d.Client.Stream(sub, "/logs?follow=1", func(b []byte) error {
				f.line(string(b))
				return nil
			})
		} else {
			_ = logfile.Tail(sub, l.d.LogDir, "hlabd.log", logLines, true, func(b []byte) error {
				f.line(strings.TrimRight(string(b), "\r\n"))
				return nil
			})
		}
		cancel()
		if switched.Load() {
			continue
		}
		// The source broke or failed: try again after the interval.
		t := time.NewTimer(l.d.Interval)
		select {
		case <-ctx.Done():
			t.Stop()
		case <-t.C:
		}
	}
}

// follow collects the lines of one follow into batches and drops the lines
// a new source replays up to the last line already taken.
type follow struct {
	maxWait time.Duration

	mu      sync.Mutex
	pending []string
	last    string
	have    bool // last is set
	// syncing: the source replays its last lines; they are held until last
	// shows up (all held lines are old) or the replay is over without it
	// (logLines held, or maxWait without a new line: all held lines are new).
	syncing bool
	held    []string
	heldAt  time.Time
}

// resync starts a new source.
func (f *follow) resync() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncing = f.have
	f.held = nil
}

func (f *follow) line(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.syncing {
		f.take(s)
		return
	}
	if s == f.last {
		f.syncing, f.held = false, nil
		return
	}
	f.held = append(f.held, s)
	f.heldAt = time.Now()
	if len(f.held) >= logLines {
		f.release()
	}
}

// take queues lines for the next batch; f.mu is held.
func (f *follow) take(lines ...string) {
	if len(lines) == 0 {
		return
	}
	f.pending = append(f.pending, lines...)
	f.last, f.have = lines[len(lines)-1], true
}

// release ends syncing and takes the held lines as new; f.mu is held.
func (f *follow) release() {
	f.take(f.held...)
	f.syncing, f.held = false, nil
}

// flushEvery emits the pending lines as one batch every interval until ctx
// ends.
func (f *follow) flushEvery(ctx context.Context, interval time.Duration, emit func([]string)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		f.mu.Lock()
		if f.syncing && len(f.held) > 0 && time.Since(f.heldAt) >= f.maxWait {
			f.release()
		}
		batch := f.pending
		f.pending = nil
		f.mu.Unlock()
		if len(batch) > 0 {
			emit(batch)
		}
	}
}
