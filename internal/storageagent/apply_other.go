//go:build !linux

package storageagent

import (
	"context"
	"errors"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

// errNeedsLinux is the answer of every apply and job outside Linux; the
// storage agent runs only on the storage VM.
var errNeedsLinux = errors.New("the storage agent needs Linux")

func (a *Agent) apply(context.Context, agentapi.Storage) error { return errNeedsLinux }

func (a *Agent) folders(context.Context, agentapi.StorageJob) (agentapi.StorageJobResult, error) {
	return agentapi.StorageJobResult{}, errNeedsLinux
}

func (a *Agent) usage(context.Context, agentapi.StorageJob) (agentapi.StorageJobResult, error) {
	return agentapi.StorageJobResult{}, errNeedsLinux
}
