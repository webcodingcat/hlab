package agentclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/store"
)

// storageGenerationKey is the kv key of the last pushed storage generation.
const storageGenerationKey = "storage.generation"

// Deadlines of the requests to the storage agent. An apply or a job runs
// a few btrfs commands; the longest legitimate one waits for the file
// system on a large data disk (btrfs subvolume sync after the deletion of
// a large box, a quota rescan, fstrim) and the agent ends any command
// after 10 minutes (storageagent commandTimeout). An apply may run two
// such commands (a box removal and the quota rescan), a job as well (the
// trim's subvolume sync and fstrim, a rollback's deletions), plus the
// short rest; the deadlines leave room for that, so an expired command
// reaches hlabd in the agent's error before hlabd gives up.
const (
	storageApplyTimeout = 30 * time.Minute
	storageJobTimeout   = 25 * time.Minute
)

// StorageClient talks to the storage agent on the storage VM.
type StorageClient struct {
	base         *Client       // request helper; its jobHTTP has no timeout
	applyTimeout time.Duration // bounds one apply, also without a deadline of the caller
	jobTimeout   time.Duration // bounds one job, also without a deadline of the caller
	mu           sync.Mutex    // one push at a time, so generations arrive in order
}

// JobError is a storage job the agent refused: the HTTP status and the
// code of its body (agentapi.JobErrNotFound, agentapi.JobErrInvalidPath).
type JobError struct {
	Status int
	Code   string
}

func (e *JobError) Error() string {
	return fmt.Sprintf("storage job error %d %s", e.Status, e.Code)
}

// NewStorage returns a StorageClient; opts.Addr is transit Addrs.StorageAgent.
func NewStorage(opts Options) *StorageClient {
	return &StorageClient{base: New(opts), applyTimeout: storageApplyTimeout, jobTimeout: storageJobTimeout}
}

// WaitReady polls the storage agent until it answers any HTTP request or
// ctx ends; the caller sets the deadline.
func (c *StorageClient) WaitReady(ctx context.Context) error {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		err := c.ping(ctx)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("storage agent not ready: %w (last: %v)", ctx.Err(), err)
		case <-t.C:
		}
	}
}

// ping succeeds when the agent answers at all; the storage agent has no
// status path, so any HTTP answer to GET /v1/storage counts.
func (c *StorageClient) ping(ctx context.Context) error {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := c.base.doWith(pctx, c.base.jobHTTP, http.MethodGet, "/v1/storage", nil, &struct{}{})
	var se *statusError
	if err == nil || errors.As(err, &se) {
		return nil
	}
	return err
}

// Push sends doc with the schema of this build and the next generation,
// which only grows (kept in the store), and returns after the agent
// applied it.
func (c *StorageClient) Push(ctx context.Context, doc agentapi.Storage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var gen int
	err := c.base.opts.Store.Tx(func(tx store.Tx) error {
		v, err := store.GetKV(tx, storageGenerationKey)
		switch {
		case errors.Is(err, store.ErrNotFound):
		case err != nil:
			return err
		default:
			if gen, err = strconv.Atoi(v); err != nil {
				return fmt.Errorf("kv %s: %w", storageGenerationKey, err)
			}
		}
		gen++
		return store.SetKV(tx, storageGenerationKey, strconv.Itoa(gen))
	})
	if err != nil {
		return fmt.Errorf("storage push: %w", err)
	}
	doc.Schema = agentapi.StorageSchema
	doc.Generation = gen
	body, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("storage push: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, c.applyTimeout)
	defer cancel()
	var applied struct{ Generation int }
	if err := c.base.doWith(ctx, c.base.jobHTTP, http.MethodPut, "/v1/storage", body, &applied); err != nil {
		return fmt.Errorf("storage push generation %d: %w", gen, err)
	}
	return nil
}

// Job runs job on the storage agent and returns when it is done; a job the
// agent refuses is a *JobError.
func (c *StorageClient) Job(ctx context.Context, job agentapi.StorageJob) (agentapi.StorageJobResult, error) {
	body, err := json.Marshal(job)
	if err != nil {
		return agentapi.StorageJobResult{}, fmt.Errorf("storage job %s: %w", job.Kind, err)
	}
	ctx, cancel := context.WithTimeout(ctx, c.jobTimeout)
	defer cancel()
	var res agentapi.StorageJobResult
	err = c.base.doWith(ctx, c.base.jobHTTP, http.MethodPost, "/v1/jobs", body, &res)
	var se *statusError
	if errors.As(err, &se) {
		var b agentapi.StorageJobError
		if json.Unmarshal(se.body, &b) == nil && b.Error != "" {
			return agentapi.StorageJobResult{}, &JobError{Status: se.code, Code: b.Error}
		}
	}
	if err != nil {
		return agentapi.StorageJobResult{}, fmt.Errorf("storage job %s: %w", job.Kind, err)
	}
	return res, nil
}
