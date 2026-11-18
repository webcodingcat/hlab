// Package resource defines what a Hetzner resource type module provides to
// the API core. Each resource type lives in its own package under
// internal/resources and is mounted by api.Router.
package resource

import (
	"context"
	"net/http"

	"github.com/webcodingcat/hlab/internal/agentapi"
	"github.com/webcodingcat/hlab/internal/scheduler"
	"github.com/webcodingcat/hlab/internal/store"
)

// Registrar receives the endpoints of a module.
type Registrar interface {
	// Handle registers h for an http.ServeMux pattern relative to /v1,
	// for example "GET /ssh_keys" or "POST /servers/{id}/actions/poweron".
	Handle(pattern string, h http.Handler)
}

// Module is one Hetzner resource type. Handlers reach the store and the
// action manager through the api helpers on the request context.
type Module interface {
	// Type is the resource type as in the API path, for example "servers".
	Type() string
	// Routes registers the module's endpoints and actions.
	Routes(r Registrar)
}

// NetContributor is the optional interface of a module that contributes
// to the agent document, for example the servers with their public IPv4.
// hlabd rebuilds the full document from the store before every push.
type NetContributor interface {
	Contribute(tx store.Tx, doc *agentapi.Config) error
}

// Deleter is the optional interface of a module whose resources need work
// to delete, for example a VM and its disk. Project deletion calls it once
// per resource of the project; the delete handler of the module uses it
// too. Delete removes the resource's row as well; a project deletion
// removes any rows left with the project.
type Deleter interface {
	Delete(ctx context.Context, id int64) error
}

// Reconciler is the optional interface of a module owning real resources:
// Reconcile makes reality match the stored state and is idempotent.
type Reconciler interface {
	Deleter
	Reconcile(ctx context.Context, id int64) error
}

// Scheduled is the optional interface of a module with time-driven work,
// for example a status watch. The cloud runs the jobs after the API is up
// and cancels them first at stop.
type Scheduled interface {
	Jobs() []scheduler.Job
}
