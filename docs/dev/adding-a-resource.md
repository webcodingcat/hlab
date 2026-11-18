# Adding a resource

Each Hetzner resource type is one Go package under
`internal/resources/<type>` that implements `resource.Module`. The SSH keys
in [internal/resources/sshkeys](../../internal/resources/sshkeys) are a
small complete example; [internal/resources/servers](../../internal/resources/servers)
shows actions and VM work.

## 1. Write the module

```go
// Package widgets serves Hetzner's widgets of a project.
package widgets

type Module struct{}

func New() *Module { return &Module{} }

// Type is the resource type as in the API path.
func (m *Module) Type() string { return "widgets" }

// Routes registers the endpoints, relative to /v1.
func (m *Module) Routes(r resource.Registrar) {
	r.Handle("GET /widgets", http.HandlerFunc(m.list))
	r.Handle("POST /widgets", http.HandlerFunc(m.create))
	r.Handle("GET /widgets/{id}", http.HandlerFunc(m.get))
	r.Handle("DELETE /widgets/{id}", http.HandlerFunc(m.delete))
}
```

The router has already authenticated the token, rejected writes with a
read-only token and checked that the body is JSON. In a handler:

- `api.ProjectFrom(req.Context())` gives the caller's project,
  `api.StoreFrom` the store, `api.ActionsFrom` the action manager.
- `api.Decode` reads the body, `api.PathID` the `{id}`.
- Answer with `api.JSON`, lists with `api.List` (adds `meta.pagination`),
  errors with `api.InvalidInput`, `api.NotFound`, `api.Error` or
  `api.ServerError`, so every answer has Hetzner's error shape.

Keep the resource in the store as a JSON document in Hetzner's API shape,
inside `store.Tx`: `store.NextID`, `store.Put` with a `store.Meta`
(kind, ID, project, name, labels, created), `store.Get[T]`,
`store.List[T]` and `store.Delete`. Every access takes the project ID, so a
project never sees another's resources.

Work that takes time (a VM, a disk) runs in an action: create it with
`ActionsFrom(ctx).Start(tx, projectID, "create_widget", refs)` inside the
transaction, answer with the action, and hand the work to `Run` after the
commit. `Start` fails with `locked` while another action holds the
resource.

## 2. Contribute to the network (optional)

A module whose resources change the appliance's network (routes, DNS
records) also implements `resource.NetContributor`:

```go
func (m *Module) Contribute(tx store.Tx, doc *agentapi.Config) error
```

hlabd rebuilds the whole agent document from the store before every push
and calls `Contribute` on each module; add your part to `doc`. After a
change that touches the network, the work calls the cloud's `PushAgent`
(see how `internal/cloud/servers.go` does it for servers).

## 3. Register the module

Add it to `Cloud.modules()` in
[internal/cloud/cloud.go](../../internal/cloud/cloud.go). The router mounts
every module of that list.

## 4. Test it

Test with [internal/apitest](../../internal/apitest): `apitest.New(t,
widgets.New())` runs the handlers in process with a temporary store and a
project `p` with a read/write token; `env.Do(method, path, body)` sends a
request and validates the response against Hetzner's OpenAPI description
(run `task openapi` once first). `env.NewToken` gives tokens for other
projects or read-only tokens.

- Add one row per new endpoint to the table of the `API-10` test in
  [internal/resources/openapi_test.go](../../internal/resources/openapi_test.go).
- Write tests per requirement, named with its ID
  (`t.Run("KEY-01", ...)`), and keep cases that differ only in data in one
  table.
- Behaviour that needs real Hyper-V goes into [test/e2e](../../test/e2e).

## 5. Document deviations

When the resource behaves differently from Hetzner, add an entry to
`api.Deviations()` in [internal/api/deviations.go](../../internal/api/deviations.go)
and describe it in [docs/user/deviations.md](../user/deviations.md); the
documentation test checks that every entry is listed there.
