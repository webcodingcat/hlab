// Package mgmtclient talks HTTP/1.1 to hlabd's management API over the
// management pipe; hlab and hlab-tray share it.
package mgmtclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
)

// ErrNotRunning is a failed connection to the management API.
var ErrNotRunning = errors.New("the cloud is not running")

// APIError is an error answer of the management API.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e APIError) Error() string { return e.Message }

// AnswerError turns an error answer (status >= 300) into an error: its
// APIError, or the status when the body carries none.
func AnswerError(status int, body []byte) error {
	var e struct {
		Error APIError `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil || e.Error.Message == "" {
		return fmt.Errorf("management API answered %d %s", status, http.StatusText(status))
	}
	return e.Error
}

// Client sends requests to the management API through dial.
type Client struct {
	dial func(ctx context.Context) (net.Conn, error)
}

// New returns a client that connects with dial for every request.
func New(dial func(ctx context.Context) (net.Conn, error)) *Client {
	return &Client{dial: dial}
}

// Do sends one request to /mgmt/v1<path>; body nil sends none. It returns
// the status and body of every answer; err is ErrNotRunning when the
// connection failed.
func (c *Client) Do(ctx context.Context, method, path string, body []byte) (status int, resp []byte, err error) {
	r, err := c.send(ctx, method, path, body)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = r.Body.Close() }()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return 0, nil, err
	}
	return r.StatusCode, data, nil
}

// Stream GETs /mgmt/v1<path> and calls line for every NDJSON line (without
// its line end) until ctx ends, the stream ends or line returns an error.
// The end of the stream returns nil, the end of ctx its error, an error
// answer AnswerError.
func (c *Client) Stream(ctx context.Context, path string, line func([]byte) error) error {
	r, err := c.send(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer func() { _ = r.Body.Close() }()
	if r.StatusCode >= 300 {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}
		return AnswerError(r.StatusCode, data)
	}
	rd := bufio.NewReader(r.Body)
	for {
		b, err := rd.ReadBytes('\n')
		if b = bytes.TrimRight(b, "\r\n"); len(b) > 0 {
			if err := line(b); err != nil {
				return err
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// send sends one request without keep-alive; a failed connection is
// ErrNotRunning.
func (c *Client) send(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://hlab/mgmt/v1"+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	var dialFailed atomic.Bool
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, err := c.dial(ctx)
			if err != nil {
				dialFailed.Store(true)
			}
			return conn, err
		},
		DisableKeepAlives: true,
	}}
	resp, err := client.Do(req)
	if err != nil {
		if dialFailed.Load() {
			return nil, ErrNotRunning
		}
		return nil, err
	}
	return resp, nil
}
