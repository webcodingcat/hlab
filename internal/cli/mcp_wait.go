package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/webcodingcat/hlab/internal/cloudstatus"
	"github.com/webcodingcat/hlab/internal/mgmtclient"
	"github.com/webcodingcat/hlab/internal/sysint"
)

// progress sends the progress notifications of one tool call: with the
// client's progress token, at most one per interval; without, none.
type progress struct {
	req   *mcp.CallToolRequest
	token any
	every time.Duration
	next  time.Time
	count float64
}

func newProgress(req *mcp.CallToolRequest, every time.Duration) *progress {
	p := &progress{req: req, every: every, next: time.Now().Add(every)}
	if req != nil && req.Params != nil && req.Session != nil {
		p.token = req.Params.GetProgressToken()
	}
	return p
}

// report sends message once the interval since the last one has passed.
func (p *progress) report(ctx context.Context, message string) {
	if p.token == nil || time.Now().Before(p.next) {
		return
	}
	p.next = time.Now().Add(p.every)
	p.count++
	_ = p.req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
		ProgressToken: p.token,
		Progress:      p.count,
		Message:       message,
	})
}

// cloudWait is the wait of one cloud_start or cloud_stop, bounded by
// CloudWait and the request ctx.
type cloudWait struct {
	s        *mcpServer
	ctx      context.Context
	deadline time.Time
	prog     *progress
	limit    error
}

// until polls the service state until done holds and returns that state;
// it gives up with w.limit at the deadline and with the error of ctx.
func (w *cloudWait) until(done func(state string) bool) (string, error) {
	for {
		state, err := w.s.sys.Service.State(w.ctx)
		if err != nil {
			return "", err
		}
		if done(state) {
			return state, nil
		}
		if time.Now().After(w.deadline) {
			return state, w.limit
		}
		w.prog.report(w.ctx, "the cloud is "+state)
		select {
		case <-w.ctx.Done():
			return state, w.ctx.Err()
		case <-time.After(w.s.opts.Poll):
		}
	}
}

func (s *mcpServer) newCloudWait(ctx context.Context, req *mcp.CallToolRequest, target string) *cloudWait {
	return &cloudWait{
		s:        s,
		ctx:      ctx,
		deadline: time.Now().Add(s.opts.CloudWait),
		prog:     newProgress(req, s.opts.Progress),
		limit:    fmt.Errorf("the cloud is still not %s after %s", target, s.opts.CloudWait),
	}
}

func always(string) bool { return true }

// cloudStartWait starts the cloud and returns its status once it runs. A
// running stop is waited out first, a running start waited for. It never
// passes discard-state.
func (s *mcpServer) cloudStartWait(ctx context.Context, req *mcp.CallToolRequest) *mcp.CallToolResult {
	w := s.newCloudWait(ctx, req, sysint.StateRunning)
	state, err := w.until(always)
	if err != nil {
		return errorResult(err)
	}
	if state == sysint.StateStopping {
		if state, err = w.until(func(st string) bool { return st != sysint.StateStopping }); err != nil {
			return errorResult(err)
		}
	}
	switch state {
	case sysint.StateRunning:
		return s.command(ctx, cloudStatus, nil, nil)
	case sysint.StateStopped:
		requested := time.Now()
		if err := s.sys.Service.Start(ctx); err != nil {
			return errorResult(err)
		}
		// The service controller may still report stopped for a while; a
		// stopped is final once the service was seen not stopped or hlabd
		// wrote the status file after the request.
		seen := false
		state, err = w.until(func(st string) bool {
			if st != sysint.StateStopped {
				seen = true
				return st == sysint.StateRunning
			}
			return seen || s.statusWrittenSince(requested)
		})
	default:
		state, err = w.until(func(st string) bool { return st == sysint.StateRunning || st == sysint.StateStopped })
	}
	if err != nil {
		return errorResult(err)
	}
	if state == sysint.StateStopped && s.sys.StatusFile != "" {
		if f, ok := cloudstatus.Read(s.sys.StatusFile); ok {
			switch {
			case f.State == "refused" && f.Problem != nil:
				return errorResult(fmt.Errorf("the cloud refused to start: %s", f.Problem.Message))
			case f.State == sysint.StateStopped && f.Error != "":
				return errorResult(fmt.Errorf("the cloud failed to start: %s", f.Error))
			}
		}
	}
	return s.command(ctx, cloudStatus, nil, nil)
}

// cloudStopWait stops the cloud and returns its status once it is
// stopped. A running start is waited out first, a running stop waited for.
func (s *mcpServer) cloudStopWait(ctx context.Context, req *mcp.CallToolRequest) *mcp.CallToolResult {
	w := s.newCloudWait(ctx, req, sysint.StateStopped)
	state, err := w.until(always)
	if err != nil {
		return errorResult(err)
	}
	if state == sysint.StateStarting {
		if state, err = w.until(func(st string) bool { return st != sysint.StateStarting }); err != nil {
			return errorResult(err)
		}
	}
	if state == sysint.StateRunning {
		if err := s.sys.Service.Stop(ctx); err != nil {
			return errorResult(err)
		}
	}
	if state != sysint.StateStopped {
		if _, err := w.until(func(st string) bool { return st == sysint.StateStopped }); err != nil {
			return errorResult(err)
		}
	}
	return s.command(ctx, cloudStatus, nil, nil)
}

// statusWrittenSince reports whether the status file was written at or
// after t.
func (s *mcpServer) statusWrittenSince(t time.Time) bool {
	if s.sys.StatusFile == "" {
		return false
	}
	fi, err := os.Stat(s.sys.StatusFile)
	return err == nil && !fi.ModTime().Before(t)
}

// projectDeleteWait deletes the project name and returns once its deletion
// action has ended; a deletion already running is waited for.
func (s *mcpServer) projectDeleteWait(ctx context.Context, req *mcp.CallToolRequest, name string) *mcp.CallToolResult {
	var out bytes.Buffer
	sys := s.sys
	sys.Stdout = &out
	c := &call{ctx: ctx, io: sys, json: true, args: []string{name}, flags: map[string]string{}}
	var body struct {
		ActionID int64 `json:"action_id"`
	}
	err := c.mgmt(http.MethodDelete, "/projects/"+pathEscape(name), nil, &body)
	var conflict mgmtclient.APIError
	if errors.As(err, &conflict) && conflict.ActionID != 0 {
		body.ActionID, err = conflict.ActionID, nil
	}
	if err != nil {
		return errorResult(err)
	}
	prog := newProgress(req, s.opts.Progress)
	seen := func(pct int) {
		prog.report(ctx, fmt.Sprintf("deleting project %s: action %d at %d%%", name, body.ActionID, pct))
	}
	if err := c.waitAction(body.ActionID, projectDeleteTimeout, seen); err != nil {
		return errorResult(fmt.Errorf("deleting project %s failed: %w", name, err))
	}
	if err := c.out(body, ""); err != nil {
		return errorResult(err)
	}
	return textResult(out.String())
}
