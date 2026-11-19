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
- Answer with `api.JSON`. Lists go through `api.ListRequest` with an
  `api.ListOptions`: name the `name` filter, the labels for
  `label_selector`, the sort fields and other filters exactly as Hetzner's
  API description offers them for the endpoint; `ListRequest` does the
  filtering, sorting, paging and `meta.pagination`.
- Return errors from the transaction and answer them with `api.Fail`:
  `store.ErrNotFound` becomes `not_found`, `actions.ErrLocked` and
  `actions.ErrProtected` become `locked` and `protected`, and an
  `*api.FieldError` becomes `invalid_input`, `uniqueness_error` or the
  operation-specific code in its `Code`, with `details.fields`. The HTTP
  status of every code comes from one table in
  [internal/api/errors.go](../../internal/api/errors.go); a new
  operation-specific code is added there. `api.InvalidInput`,
  `api.NotFound`, `api.Error` and `api.ServerError` answer directly.
- Check labels with `api.CheckLabels` and names with `store.NameTaken`
  (`uniqueness_error` on field `name`); a `PUT` replaces the whole label
  set.

Keep the resource in the store as a JSON document in Hetzner's API shape,
inside `store.Tx`: `store.NextID`, `store.Put` with a `store.Meta`
(kind, ID, project, name, labels, created), `store.Get[T]`,
`store.List[T]` and `store.Delete`. Every access takes the project ID, so a
project never sees another's resources.

Work that takes time (a VM, a disk) runs in an action: create it with
`ActionsFrom(ctx).Start(tx, projectID, "create_widget", refs)` inside the
transaction, answer with the action, and hand the work to `Run` after the
commit. `Start` fails with `locked` while another action holds the
resource. A request that starts several actions on the same resources at
once (the firewalls' `set_rules` starts `set_firewall_rules` plus one
`apply_firewall` per server) uses
`StartAll(tx, projectID, lock, specs)`: it checks locks once, only on the
refs in `lock`, and the actions of one batch never lock each other.
`actions.RoutesFor(r, "widgets", "widget")` from
[internal/resources/actions](../../internal/resources/actions) adds the
generic `GET /widgets/actions`, `/widgets/{id}/actions` and
`/widgets/{id}/actions/{action_id}` endpoints.

## 2. Delete with the project (optional)

`hlab project delete` deletes every resource of the project. Rows are
removed with the project; a module whose resources need work to delete (a
VM, a disk, an address) implements `resource.Deleter`:

```go
func (m *Module) Delete(ctx context.Context, id int64) error
```

Project deletion calls it once per resource after the project's running
actions have finished; a resource already gone is no error. Use the same
code in the module's own delete action, as the servers do.

## 3. Contribute to the network (optional)

A module whose resources change the appliance's network (routes, DNS
records) also implements `resource.NetContributor`:

```go
func (m *Module) Contribute(tx store.Tx, doc *agentapi.Config) error
```

hlabd rebuilds the whole agent document from the store before every push
and calls `Contribute` on each module; add your part to `doc`. After a
change that touches the network, the work calls the cloud's `PushAgent`
(the servers receive it as `PushAgent` in their `servers.Deps`).

## 4. Run periodic work (optional)

A module with work that repeats while the cloud runs (polling VM states,
sampling) implements `resource.Scheduled`:

```go
func (m *Module) Jobs() []scheduler.Job {
	return []scheduler.Job{{Name: "widgets watch", Every: 5 * time.Second, Run: m.watch}}
}
```

[internal/scheduler](../../internal/scheduler) runs each job once per
interval after the API is up, never two runs of one job at once, and
cancels the context at cloud stop. Take the time from a
`scheduler.Clock` in your dependencies, so tests can drive it.

A job that starts actions has no request context to take `ActionsFrom`
from. Such a module also implements `resource.ActionUser`:

```go
func (m *Module) UseActions(am *actions.Manager) { m.actions = am }
```

The router calls it with its action manager when it mounts the module,
before `Routes`; the job then calls `m.actions.Start` itself, as the
servers do for the daily backups.

## 5. Clean up after a restart (optional)

An action cut short by a cloud stop ends with `server_error` at the next
start, but the record it worked on may be left half-made (a `creating`
volume with a partial disk). A module with such records implements
`resource.Recoverer`:

```go
func (m *Module) Recover(ctx context.Context) error
```

Cloud start calls it once, after the interrupted actions are marked
failed and before the API serves or any job runs; an error fails the
start. Remove what cannot be finished, as the images and volumes do with
records still `creating` and their files; a missing file is no error.

## 6. Register the module

Add it to `Cloud.modules()` in
[internal/cloud/cloud.go](../../internal/cloud/cloud.go). The router mounts
every module of that list.

## 7. Test it

Test with [internal/apitest](../../internal/apitest): `apitest.New(t,
widgets.New())` runs the handlers in process with a temporary store and a
project `p` with a read/write token; `env.Do(method, path, body)` sends a
request and validates the response against Hetzner's OpenAPI description
(run `task openapi` once first). `env.NewToken` gives tokens for other
projects or read-only tokens; `env.WaitAction(id)` polls an action until
it has finished.

- Add one row per new endpoint to the table of the `API-10` test in
  [internal/resources/openapi_test.go](../../internal/resources/openapi_test.go),
  and mount the module in `newFullEnv` of
  [internal/resources/conventions_test.go](../../internal/resources/conventions_test.go),
  whose table-driven rows check read-only tokens, project isolation,
  paging, filters, labels and name uniqueness across all resources; add
  the new endpoints to their tables.
- Write tests per requirement, named with its ID
  (`t.Run("KEY-01", ...)`), and keep cases that differ only in data in one
  table.
- Behaviour that needs real Hyper-V goes into [test/e2e](../../test/e2e).

## 8. Document deviations

When the resource behaves differently from Hetzner, add an entry to
`api.Deviations()` in [internal/api/deviations.go](../../internal/api/deviations.go)
and describe it in [docs/user/deviations.md](../user/deviations.md); the
documentation test checks that every entry is listed there.
