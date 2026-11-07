// Package cloud is Hetzner's Cloud API: one file per resource, each with its
// routes, its JSON and its action table. It decides what to change and asks
// the lab to do it; it never calls the platform.
package cloud

import (
	"github.com/webcodingcat/hlab/internal/lab"
	"github.com/webcodingcat/hlab/internal/web"
)

// Register adds the Cloud API's routes to api.
func Register(api *web.API, l *lab.Lab) {
	registerActions(api, l)
}
