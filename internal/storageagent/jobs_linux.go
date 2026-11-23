//go:build linux

package storageagent

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/webcodingcat/hlab/internal/agentapi"
)

var (
	errNotFound    = &jobError{status: http.StatusNotFound, code: agentapi.JobErrNotFound}
	errInvalidPath = &jobError{status: http.StatusUnprocessableEntity, code: agentapi.JobErrInvalidPath}
)

// folders lists the names of the direct subdirectories of job.Path below
// the box's data, hidden ones included, sorted. An absolute or escaping
// path is invalid_path; a missing one, or one that leaves the data through
// a symlink, is not_found.
func (a *Agent) folders(_ context.Context, job agentapi.StorageJob) (agentapi.StorageJobResult, error) {
	p := job.Path
	if p == "" {
		p = "."
	}
	if !filepath.IsLocal(p) {
		return agentapi.StorageJobResult{}, errInvalidPath
	}
	r, err := os.OpenRoot(filepath.Join(a.o.Root, job.Box))
	if errors.Is(err, fs.ErrNotExist) {
		return agentapi.StorageJobResult{}, errNotFound
	}
	if err != nil {
		return agentapi.StorageJobResult{}, err
	}
	defer func() { _ = r.Close() }()
	f, err := r.Open(p)
	if err != nil {
		return agentapi.StorageJobResult{}, errNotFound
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err != nil || !st.IsDir() {
		return agentapi.StorageJobResult{}, errNotFound
	}
	entries, err := f.ReadDir(-1)
	if err != nil {
		return agentapi.StorageJobResult{}, err
	}
	names := []string{}
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return agentapi.StorageJobResult{Folders: names}, nil
}

// usage returns the bytes of the box from its qgroups (E18): Data is what
// the box's subvolume references, Snapshots what the level-1 qgroup holds
// beyond it.
func (a *Agent) usage(ctx context.Context, job agentapi.StorageJob) (agentapi.StorageJobResult, error) {
	n, err := boxUID(job.Box)
	if err != nil {
		return agentapi.StorageJobResult{}, err
	}
	data := filepath.Join(a.o.Root, job.Box)
	if _, err := os.Lstat(data); errors.Is(err, fs.ErrNotExist) {
		return agentapi.StorageJobResult{}, errNotFound
	}
	out, err := run(ctx, "btrfs", "inspect-internal", "rootid", data)
	if err != nil {
		return agentapi.StorageJobResult{}, err
	}
	qg, err := a.qgroups(ctx)
	if err != nil {
		return agentapi.StorageJobResult{}, err
	}
	u := agentapi.Usage{Data: qg["0/"+strings.TrimSpace(out)]}
	u.Snapshots = max(qg["1/"+strconv.Itoa(n)]-u.Data, 0)
	return agentapi.StorageJobResult{Usage: u}, nil
}
