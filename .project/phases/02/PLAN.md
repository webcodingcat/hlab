# Phase 02: API conventions, catalog and the three clients

Confirmed assumptions and answers: CONTEXT.md. Research: RESEARCH.md
(sections R1 to R6 below mean its numbered sections). Test map: TESTS.md.

## Interfaces

New or changed in this phase; tests and implementers follow them.

```go
// internal/api (02-01): one status table, taken from the error table of the
// frozen OpenAPI description (CONTEXT.md "Corrections").
func Status(code string) int // unknown code: 500
// Error replaces Error(w, status, code, message, details); the status comes
// from Status(code). details nil is written as {} where the description
// requires an object.
func Error(w http.ResponseWriter, code, message string, details any)
func InvalidInput(w http.ResponseWriter, field, message string) // 422, details.fields
// FieldError is an error a handler returns from its transaction for
// invalid_input, uniqueness_error or an operation-specific 422 code
// (placement_error, server_not_stopped, already_in_placement_group).
type FieldError struct {
	Code    string // default "invalid_input"
	Field   string // "" = no details.fields
	Message string
}
func (e *FieldError) Error() string
// Fail answers err: store.ErrNotFound -> not_found, *actions.Error (ErrLocked
// included) -> its code, *FieldError -> its code with details.fields,
// anything else -> server_error (logged).
func Fail(w http.ResponseWriter, err error)

// internal/labels (02-02)
func Validate(l map[string]string) error          // Hetzner key and value rules (R5)
type Selector struct{ /* parsed terms, AND-ed */ }
func ParseSelector(s string) (Selector, error)     // =, ==, !=, k, !k, in (...), notin (...), comma = AND
func (s Selector) Matches(l map[string]string) bool

// internal/api (02-06): list conventions in one place.
type ListOptions[T any] struct {
	Name    func(T) string                    // nil: endpoint has no name filter
	Labels  func(T) map[string]string         // nil: no label_selector
	Sort    map[string]func(a, b T) int       // documented sort fields ("id", "name", "created")
	Filters map[string]func(T, []string) bool // other documented filters ("status", "type", "fingerprint")
}
// ListRequest filters, sorts (repeated sort=<field>[:asc|:desc], in order),
// pages (page default 1, per_page default 25, max 50, invalid values fall
// back as R5 says, page beyond the last: empty list, next_page null) and
// writes items under key with meta.pagination. Unknown sort field or bad
// selector: 422 invalid_input. items arrive ordered by ID.
func ListRequest[T any](w http.ResponseWriter, req *http.Request, key string, items []T, opt ListOptions[T])
func CheckLabels(l map[string]string) error // *FieldError on field "labels"
// internal/store (02-06)
func NameTaken(tx Tx, kind string, projectID int64, name string, exceptID int64) (bool, error)
func IDs(tx Tx, kind string, projectID int64) ([]int64, error)

// internal/catalog (02-03): the field ServerTypes becomes unexported.
func LoadAll(shippedDir, dataDir, customFile string) (*Catalog, []string, error) // []string: warnings for /status
func (c *Catalog) Types() []ServerType                      // shipped or updated types plus own types, guarded
func (c *Catalog) ServerTypeByRef(ref string) (ServerType, bool) // name or ID
func (c *Catalog) ReplaceServerTypes(raw []json.RawMessage) error // after an update; own types merged again
func (c *Catalog) Pricing() json.RawMessage                 // pricing.json with server_types built from Types()
// internal/catalog (02-07)
type UpdateOptions struct {
	BaseURL string       // default https://api.hetzner.cloud/v1; tests use httptest
	DataDir string       // writes <DataDir>\catalog\server-types.json atomically
	Client  *http.Client
}
func Update(ctx context.Context, c *Catalog, opts UpdateOptions, token string) error

// internal/actions (02-11)
var ErrProtected = &Error{Code: "protected", Message: "the resource is protected"}
func (m *Manager) ListFor(projectID int64, refType string, resourceID int64) ([]Action, error) // resourceID 0: all of refType
// internal/resources/actions (02-11)
func RoutesFor(r resource.Registrar, typ, refType string) // GET /<typ>/actions, /<typ>/{id}/actions, /<typ>/{id}/actions/{action_id}

// internal/resources/placementgroups (02-12)
const Kind = "placement_groups"
const MaxServers = 10
func Find(tx store.Tx, projectID int64, ref string) (Group, error)
func AddServer(tx store.Tx, projectID, groupID, serverID int64) error    // *api.FieldError placement_error when full (11th)
func RemoveServer(tx store.Tx, projectID, groupID, serverID int64) error

// internal/resource (02-14): Deleter is the part of Reconciler that
// project deletion needs; servers implement only it in this phase, phase
// 03 adds Reconcile.
type Deleter interface{ Delete(ctx context.Context, id int64) error }
type Reconciler interface {
	Deleter
	Reconcile(ctx context.Context, id int64) error
}

// internal/mgmt (02-07, 02-14): additive fields.
type Deps struct {
	// ... existing fields
	Actions       *actions.Manager                                  // project deletion actions
	Deleters      map[string]resource.Deleter                       // by store kind
	UpdateCatalog func(ctx context.Context, hetznerToken string) error
}
// GET /mgmt/v1/actions/{id} -> 200 {"action": <Hetzner action object>};
// 404 not_found for an unknown ID or an action of a project.
// DELETE /mgmt/v1/projects/{name} -> 202 {"action_id": n}; 409 conflict
// while a deletion of that project runs.
// POST /mgmt/v1/catalog/update {"hetzner_token": "..."} -> 200
// {"server_types": n}; 400 invalid_input without token; 502 server_error
// with Hetzner's message when the real API refuses.

// internal/cli (02-07): additive IO fields.
type IO struct {
	// ... existing fields
	Getenv     func(string) string
	ReadSecret func(prompt string) (string, error) // hidden prompt; x/sys/windows console mode
}

// internal/hostnet (02-04)
const ProblemWSLModeUnsupported = "wsl_mode_unsupported" // bridged, none
// internal/hostnet/windows (02-04)
type Options struct {
	// ... existing fields
	// WSLConfig returns the console user's %UserProfile%\.wslconfig;
	// found false when there is no file or no console user. Default: WTS
	// session token and GetUserProfileDirectory (R4).
	WSLConfig func(ctx context.Context) (data []byte, found bool, err error)
}

// internal/apitest (02-11)
type BlockingMachines struct{ /* Create blocks until Release; Fail makes it fail */ }
func NewBlockingMachines() *BlockingMachines
func (b *BlockingMachines) Release()

// test/e2e (02-05): shared harness helpers.
func (e *env) wsl(ctx context.Context, timeout time.Duration, environ []string, args ...string) (result, error) // wsl.exe -d <distro> -e ...
func (e *env) tofu(t *testing.T, dir string, args ...string) result     // HCLOUD_TOKEN, HCLOUD_ENDPOINT set; plugin cache in user cache
func (e *env) ansible(t *testing.T, module string, args string) result  // in the distro, token via WSLENV
func (e *env) wslKey() string                                           // path of the temp key in the distro (0600)
```

## Plans

- [x] 02-01 Error codes from one table
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: API-09, API-14 | Tests: internal/api/errors_test.go, internal/api/router_test.go
  - Reads: REQUIREMENTS API; CONTEXT.md "Corrections"; ARCHITECTURE "api"; E8
  - Size: ~250 lines in 8 files
  - Adjusts: -
  - [x] Status table, `Error` without status, `InvalidInput` (422), `FieldError`, `Fail`; the router's own not_found, json_error, unauthorized and token_readonly answers use it (internal/api/response.go, errors.go, path.go, router.go)
  - [x] Move the callers to the new `Error`/`Fail` (internal/resources/actions/actions.go, datacenters/datacenters.go, servers/servers.go, sshkeys/sshkeys.go, internal/apitest/probe.go)

- [x] 02-02 Labels and label selectors
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: API-06, API-07 | Tests: internal/labels/labels_test.go
  - Reads: REQUIREMENTS API; RESEARCH R5 (labels, selector)
  - Size: ~250 lines in 1 file
  - Adjusts: -
  - [x] `Validate`, `ParseSelector`, `Selector.Matches` with every operator (internal/labels/labels.go)

- [ ] 02-04 WSL2 mode check
  - Wave: 1 | Depends on: - | Review: alone
  - Requirements: HOST-04 | Tests: internal/hostnet/windows/hostnet_test.go (HOST-04)
  - Reads: REQUIREMENTS HOST; ARCHITECTURE "hostnet", "Network" (WSL2); decisions/wsl2-networking.md; RESEARCH R4; E4
  - Size: ~220 lines in 4 files
  - Adjusts: -
  - [ ] `.wslconfig` parser (section `[wsl2]`, `networkingMode`, `dnsTunneling`, case-insensitive) mapped to non-fatal problems; `Check` appends them; new constant (internal/hostnet/hostnet.go, internal/hostnet/windows/wsl.go, hostnet.go)
  - [ ] Default `Options.WSLConfig` through `WTSGetActiveConsoleSessionId`, `WTSQueryUserToken`, `GetUserProfileDirectoryW`; no console user means found false; never writes the file (internal/hostnet/windows/wslconfig.go)

- [ ] 02-05 E2E harness: WSL2 distro, OpenTofu and Ansible
  - Wave: 1 | Depends on: - | Review: alone | e2e
  - Requirements: HOST-03, CLI-03 | Tests: test/e2e/wsl_test.go, test/e2e/ansible_test.go
  - Reads: SPEC "Stack" (client versions), "Machine prerequisites"; CONTEXT.md "Corrections" (distro, key); RESEARCH R1, R2; E4
  - Size: ~300 lines in 3 files
  - Adjusts: -
  - [ ] Distro selection (`HLAB_E2E_WSL_DISTRO`, else the one registered distro whose name starts with `Ubuntu`; fail when none or several), temp key directory in the distro with mode 0600 deleted at teardown, `env.wsl` (test/e2e/main_test.go, test/e2e/clients_test.go)
  - [ ] Version checks for tofu 1.13.9 and ansible-core 2.22.0 with collection 7.2.0 (fail with a clear message, install nothing); `env.tofu` with plugin cache in the user cache folder, `env.ansible` with `HCLOUD_TOKEN`/`HCLOUD_ENDPOINT` through `WSLENV` (test/e2e/clients_test.go, test/e2e/e2e_test.go)

- [ ] 02-03 Catalog with own server types and pricing
  - Wave: 2 | Depends on: 02-01 | Review: alone
  - Requirements: CAT-02, CAT-05 | Tests: internal/catalog/catalog_test.go, internal/resources/pricing/pricing_test.go
  - Reads: REQUIREMENTS CAT; ARCHITECTURE "Files and formats", "Data schema" (catalogs in memory); CONTEXT.md
  - Size: ~330 lines in 6 files
  - Adjusts: tests reading `Catalog.ServerTypes` (none known)
  - [ ] `LoadAll`: data-folder `catalog\server-types.json` before the shipped one, own types from `server-types.custom.json` (IDs from 100000, unique names and IDs; else a warning and shipped types only), guarded `Types`, `ServerTypeByRef`, `ReplaceServerTypes`, `Pricing` (own types without prices get zero prices per location) (internal/catalog/catalog.go, custom.go)
  - [ ] Field `ServerTypes` unexported; servertypes and servers use `Types`/`ServerTypeByRef`; new `/pricing` module; hlabd loads with `LoadAll` and reports warnings as problems in `/status` (internal/resources/servertypes/servertypes.go, servers/servers.go, pricing/pricing.go, internal/cloud/cloud.go)

- [x] 02-06 List conventions, label checks and uniqueness
  - Wave: 2 | Depends on: 02-01, 02-02 | Review: alone
  - Requirements: API-05, API-06, API-11 | Tests: internal/api/list_test.go
  - Reads: REQUIREMENTS API; RESEARCH R5 (pagination, sorting); ARCHITECTURE "api", "store"
  - Size: ~280 lines in 3 files
  - Adjusts: -
  - [x] `ListRequest` and `ListOptions`; `api.List` stays for single-page catalog answers until its callers move (internal/api/list.go)
  - [x] `CheckLabels` (internal/api/labels.go); `NameTaken`, `IDs` (internal/store/resources.go)

- [ ] 02-07 Catalog update and token revocation path
  - Wave: 3 | Depends on: 02-03, 02-05 | Review: alone | e2e
  - Requirements: CAT-03, PROJ-03 | Tests: internal/catalog/update_test.go, internal/cli/cli_test.go (CAT-03, PROJ-03), test/e2e/catalog_test.go
  - Reads: REQUIREMENTS CAT, PROJ; SPEC "Accounts and secrets"; ARCHITECTURE "hlab", "Management API"; RESEARCH R3, R6
  - Size: ~380 lines in 6 files
  - Adjusts: -
  - [ ] `catalog.Update`: all pages with `per_page=50` until `next_page` is null, Bearer token, atomic write, `ReplaceServerTypes`; old file and types stay on any error; the token is never logged or written (internal/catalog/update.go)
  - [ ] `POST /mgmt/v1/catalog/update` and `Deps.UpdateCatalog`, wired in hlabd (internal/mgmt/mgmt.go, internal/cloud/cloud.go)
  - [ ] `hlab catalog update [--hetzner-token-env VAR]`: variable, else hidden prompt on a terminal, else exit 2 naming the flag; `IO.Getenv`, `IO.ReadSecret` (internal/cli/cli.go, internal/cli/secret_windows.go, secret_other.go, cmd/hlab/main.go)

- [ ] 02-08 SSH keys complete
  - Wave: 3 | Depends on: 02-05, 02-06 | Review: alone | e2e
  - Requirements: KEY-01 | Tests: internal/resources/sshkeys/sshkeys_test.go, test/e2e/tofu_test.go (CLI-02/ssh_keys)
  - Reads: REQUIREMENTS KEY, API; RESEARCH R1 (`hcloud_ssh_key`), R5 (ssh_keys filters, sort `id`, `name`)
  - Size: ~180 lines in 1 file
  - Adjusts: -
  - [ ] List through `ListRequest` (name, fingerprint, label_selector, sort id/name); `PUT /ssh_keys/{id}` (name and labels, each only when present); label check; unique name and fingerprint per project (`uniqueness_error`, field `name` or `public_key`) (internal/resources/sshkeys/sshkeys.go)

- [ ] 02-09 Catalog lists by the conventions
  - Wave: 3 | Depends on: 02-03, 02-06 | Review: alone
  - Requirements: API-05, API-06 (catalog endpoints) | Tests: - (rows in internal/resources/conventions_test.go, plan 02-13)
  - Reads: RESEARCH R5, R6 (server_types: name and paging only)
  - Size: ~100 lines in 4 files
  - Adjusts: -
  - [ ] `/server_types`, `/locations`, `/images` and `/actions?id=` through `ListRequest` with exactly the parameters of the frozen description (internal/resources/servertypes/servertypes.go, locations/locations.go, images/images.go, actions/actions.go)

- [ ] 02-10 Servers by the conventions
  - Wave: 3 | Depends on: 02-03, 02-06 | Review: alone
  - Requirements: CAT-04, API-07, API-11 (servers) | Tests: internal/resources/servers/servers_test.go (CAT-04)
  - Reads: REQUIREMENTS SRV, CAT, API; RESEARCH R5 (servers filters and sort)
  - Size: ~200 lines in 1 file
  - Adjusts: -
  - [ ] List through `ListRequest` (name, label_selector, status, sort id/name/created); `PUT /servers/{id}` (name, labels); label check on create and update; unique name; an `arm` type answers 412 `resource_unavailable` before anything is stored (internal/resources/servers/servers.go)

- [ ] 02-11 Server actions, protection, locks, console
  - Wave: 4 | Depends on: 02-10 | Review: alone
  - Requirements: API-08, API-12, API-13, SRV-17 | Tests: internal/resources/servers/servers_test.go (API-08, API-12, API-13, SRV-17)
  - Reads: REQUIREMENTS API, SRV-17; SPEC "Fixed API answers"; ARCHITECTURE "actions"
  - Size: ~330 lines in 5 files
  - Adjusts: -
  - [ ] `Manager.ListFor`, `ErrProtected`; `RoutesFor` with list conventions (status filter, sort id/command/status/started/finished as documented) (internal/actions/actions.go, internal/resources/actions/actions.go)
  - [ ] `change_protection` (delete, rebuild) as an action, protected delete refused with 423 `protected`, `request_console` answers `unsupported_error` with the SPEC message, create reports progress; `api.Deviations` gets the console entry (internal/resources/servers/actions.go, servers.go, internal/api/deviations.go)
  - [ ] `BlockingMachines` for lock and progress tests (internal/apitest/apitest.go)

- [ ] 02-12 Placement groups
  - Wave: 3 | Depends on: 02-06 | Review: alone
  - Requirements: SRV-22 | Tests: - (rows in plan 02-13, which wires the module)
  - Reads: REQUIREMENTS SRV-22, API; RESEARCH R1 (`hcloud_placement_group`: separate PUTs, nullable action), R5 (limits, filters, sort)
  - Size: ~260 lines in 1 file
  - Adjusts: -
  - [ ] Module: create (type `spread` only, answer with `action: null`), get, list (name, label_selector, type, sort id/name/created), PUT with name or labels alone, delete; `servers` list in the document; `Find`, `AddServer` (11th: 422 `placement_error`), `RemoveServer` (internal/resources/placementgroups/placementgroups.go)

- [ ] 02-13 Servers in placement groups; conventions across resources
  - Wave: 5 | Depends on: 02-05, 02-08, 02-09, 02-11, 02-12 | Review: alone | e2e
  - Requirements: SRV-22, API-03, API-04, API-05, API-06, API-07, API-11 | Tests: internal/resources/placementgroups/placementgroups_test.go, internal/resources/conventions_test.go, test/e2e/tofu_test.go (CLI-02/placement_groups)
  - Reads: REQUIREMENTS API, SRV-22; RESEARCH R1 (`hcloud_placement_group`), R5 (`add_to_placement_group` errors)
  - Size: ~220 lines in 4 files
  - Adjusts: -
  - [ ] `placement_group` on server create; `add_to_placement_group` (server must be off: `server_not_stopped`; member already: `already_in_placement_group`) and `remove_from_placement_group` as actions; deleting a server leaves its group; the server object shows its group; register the placement groups module in hlabd (internal/resources/servers/servers.go, servers/actions.go, placementgroups/placementgroups.go, internal/cloud/cloud.go)
  - [ ] Fix whatever the conventions table finds in the modules of this phase (same files, no new behaviour)

- [ ] 02-14 Project deletion with resources
  - Wave: 6 | Depends on: 02-07, 02-13 | Review: alone
  - Requirements: PROJ-04 | Tests: internal/mgmt/mgmt_test.go
  - Reads: REQUIREMENTS PROJ; ARCHITECTURE "resource", "Management API"; decisions/resource-reconciler.md; CONTEXT.md "Answers"
  - Size: ~300 lines in 5 files
  - Adjusts: internal/cli/cli_test.go (`newHlab` passes `mgmt.Deps.Actions`)
  - [ ] `resource.Deleter` and `Reconciler` (internal/resource/resource.go); servers implement `Delete` (VM, disk, IP release, group membership, row), used by the delete handler too (internal/resources/servers/servers.go)
  - [ ] `DELETE /projects/{name}`: revoke the tokens in the first transaction, then one action deletes every resource through its module's `Deleter`, then the project with the rest by cascade, pushes the agent document; a failure ends the action with error and keeps the project; a second delete while one runs answers 409; `GET /mgmt/v1/actions/{id}` (internal/mgmt/mgmt.go, internal/cloud/cloud.go)
  - [ ] `hlab project delete` waits for the action with a deadline and reports its error (internal/cli/cli.go)

- [ ] 02-15 Documentation
  - Wave: 7 | Depends on: all | Review: alone
  - Requirements: DOC-01, DOC-02, DOC-03, DOC-04, DOC-05, DOC-06 | Tests: test/docs/docs_test.go (existing rows)
  - Reads: SPEC "Documentation"; REQUIREMENTS DOC
  - Size: ~250 lines in 6 files
  - Adjusts: -
  - [ ] commands.md (`catalog update`, `project delete` waiting), configuration.md (`server-types.custom.json` with an example, catalog in the data folder), deviations.md (console, placement groups without physical placement, WSL modes) (docs/user/)
  - [ ] README (WSL2 prerequisites and detection, endpoint settings for OpenTofu and Ansible); docs/dev/build-and-test.md (`HLAB_E2E_WSL_DISTRO`, tofu and Ansible prerequisites of `task e2e`)
