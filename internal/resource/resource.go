// Package resource defines what a Hetzner resource type module provides to
// the API core. Each resource type lives in its own package under
// internal/resources and is mounted by api.Router.
package resource

import "net/http"

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
