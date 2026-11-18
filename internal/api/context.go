package api

import (
	"context"

	"github.com/webcodingcat/hlab/internal/actions"
	"github.com/webcodingcat/hlab/internal/store"
)

type ctxKey int

const (
	keyDeps ctxKey = iota
	keyProject
	keyReadOnly
)

func withDeps(ctx context.Context, d Deps) context.Context {
	return context.WithValue(ctx, keyDeps, d)
}

func withAuth(ctx context.Context, p store.Project, readOnly bool) context.Context {
	ctx = context.WithValue(ctx, keyProject, p)
	return context.WithValue(ctx, keyReadOnly, readOnly)
}

// WithProject returns ctx carrying project p with read/write rights. The
// router serves a request with such a context for p without a token; only
// trusted callers in the same process (the management API's project proxy)
// set it.
func WithProject(ctx context.Context, p store.Project) context.Context {
	return withAuth(ctx, p, false)
}

// ProjectFrom returns the project the request's token selected.
func ProjectFrom(ctx context.Context) (store.Project, bool) {
	p, ok := ctx.Value(keyProject).(store.Project)
	return p, ok
}

// ReadOnlyFrom reports whether the request's token is read-only.
func ReadOnlyFrom(ctx context.Context) bool {
	ro, _ := ctx.Value(keyReadOnly).(bool)
	return ro
}

// StoreFrom returns the store for a request served by the router.
func StoreFrom(ctx context.Context) *store.Store {
	d, _ := ctx.Value(keyDeps).(Deps)
	return d.Store
}

// ActionsFrom returns the action manager for a request served by the router.
func ActionsFrom(ctx context.Context) *actions.Manager {
	d, _ := ctx.Value(keyDeps).(Deps)
	return d.Actions
}
