// Package agentclient pushes the agent document to hlab-agent in the
// appliance and reads its status over the agent's HTTP interface.
package agentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/store"
)

// generationKey is the kv key of the last pushed generation.
const generationKey = "agent.generation"

// pollInterval is the gap between two status requests of WaitReady.
const pollInterval = 2 * time.Second

// Options configure a Client.
type Options struct {
	// Addr is host:port of the agent interface, "198.19.0.1:9100".
	Addr string
	// Secret is the bearer secret the agent accepts.
	Secret string
	// Store keeps the generation counter.
	Store *store.Store
	// HTTP sends the requests; nil means a client with a 30 s timeout.
	HTTP *http.Client
}

// Client talks to one hlab-agent.
type Client struct {
	opts Options
	mu   sync.Mutex // one push at a time, so generations arrive in order
}

// New returns a Client.
func New(opts Options) *Client {
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{opts: opts}
}

// Push sends cfg as the full document with the schema of this build and
// the next generation, which only grows (kept in the store across restarts
// of hlabd and of the appliance).
func (c *Client) Push(ctx context.Context, cfg agentapi.Config) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var gen int
	err := c.opts.Store.Tx(func(tx store.Tx) error {
		v, err := store.GetKV(tx, generationKey)
		switch {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			return err
		default:
			if gen, err = strconv.Atoi(v); err != nil {
				return fmt.Errorf("kv %s: %w", generationKey, err)
			}
		}
		gen++
		return store.SetKV(tx, generationKey, strconv.Itoa(gen))
	})
	if err != nil {
		return fmt.Errorf("agent push: %w", err)
	}
	cfg.Schema = agentapi.Schema
	cfg.Generation = gen
	body, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("agent push: %w", err)
	}
	var st agentapi.Status
	if err := c.do(ctx, http.MethodPut, "/v1/config", body, &st); err != nil {
		return fmt.Errorf("agent push generation %d: %w", gen, err)
	}
	return nil
}

// Status returns the agent's applied generation and load balancer state.
func (c *Client) Status(ctx context.Context) (agentapi.Status, error) {
	var st agentapi.Status
	if err := c.do(ctx, http.MethodGet, "/v1/status", nil, &st); err != nil {
		return agentapi.Status{}, fmt.Errorf("agent status: %w", err)
	}
	return st, nil
}

// WaitReady polls the agent's status until it answers or ctx ends; the
// caller sets the deadline.
func (c *Client) WaitReady(ctx context.Context) error {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		_, err := c.Status(ctx)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("agent not ready: %w (last: %v)", ctx.Err(), err)
		case <-t.C:
		}
	}
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+c.opts.Addr+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.opts.Secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.opts.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(data))
	}
	return json.Unmarshal(data, out)
}
