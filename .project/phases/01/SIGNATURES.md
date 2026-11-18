# Phase 01 signatures assumed by the tests

The tests were written before the code; implementers follow these shapes
(on top of PLAN.md "Interfaces new in this phase").

## Unit tests (internal/...)

- store: `Open(path) (*Store, error)`, `(*Store).Close() error`, `(*Store).Tx(func(tx store.Tx) error) error`; `Project{ID int64; Name string; ...}`; `CreateProject(tx, name) (Project, error)`; `CreateToken(tx, projectID, name string, sha256 []byte, readOnly bool) error`; token hash = SHA-256 of the raw token string.
- actions: `New(*store.Store) *Manager`, workers run without a Start call.
- resource: `Registrar interface{ Handle(pattern string, h http.Handler) }` (http.ServeMux patterns relative to /v1, e.g. "GET /ssh_keys"); `Module{Type() string; Routes(r resource.Registrar)}`. Modules get store and action manager through the request context / api helpers, not their constructors.
- api: `New(api.Deps{Store, Actions}) *Router`; `(*Router).Mount(resource.Module)`; serves under "/v1"; `ProjectFrom(ctx) (store.Project, bool)`; 401 `unauthorized`; read-only non-GET 403 `token_readonly`.
- catalog: `catalog.Load(dir) (*catalog.Catalog, error)` on the repo's `catalog/` folder.
- modules: `locations.New(*catalog.Catalog)`, `datacenters.New()` (410), `servertypes.New(*catalog.Catalog)` (internal/resources/servertypes), `images.New(*catalog.Catalog)` (internal/resources/images), `sshkeys.New()` (internal/resources/sshkeys), `actions.New()` (internal/resources/actions).
- servers: `servers.New(servers.Deps{Catalog, PublicRange netip.Prefix, Machines servers.Machines})`; `Machines interface{ Create(ctx, id int64) error; Delete(ctx, id int64) error }`; POST returns `action`, DELETE returns 200 with `action`.
- ipam: `NextPublic(tx, r netip.Prefix) (netip.Addr, error)`, `Release(tx, a netip.Addr) error`.
- images (internal/images): `Source{URL, ChecksumURL, Format string}` (raw|qcow2); `Options{Dir; Catalog map[string]Source; HTTP *http.Client; Convert func(ctx, src, dst string) error}` (src fixed VHD, dst base to write); `New(Options) *Store`; `Ensure(ctx, name) (string, error)`; checksum lines "<hex> *<file>" (also accept "<hex>  <file>").
- profile: `Profile{Name, ServiceName, PipeName, DataDirName, Tag}`; Tag = firewall group and switch name.
- hostnet/windows: `Runner func(ctx, script string) ([]byte, error)`; `Options{Run; OtherInstanceRunning func(ctx) (bool, error); StateDir string; Program string}` (Program default os.Executable()); `New(Options) *HostNet`; `Up` and `Check` succeed when every script returns empty output; cmdlet parameters inline on one line (no splatting, no backtick continuation); firewall `-InterfaceAlias "vEthernet (<SwitchName>)"`, `-Group <Tag>`, `Remove-NetFirewallRule -Group <tag>` before `New-NetFirewallRule`; route `New-NetRoute -DestinationPrefix <range> -NextHop 198.19.0.1`; other_instance_running message contains `profile.Other().Name`.
- mgmt: `New(mgmt.Deps{Store, API http.Handler, Status func() mgmt.Status}) http.Handler`; `Status{State string; ...}`.
- cli: `Run(ctx, args, io cli.IO) int` (args without program name); IO as PLAN.md; Dial failure: exit 1, stderr "the cloud is not running" (not for `cloud` and `version`); `token create` prints the token as one run of 64 `[A-Za-z0-9]`.
- apitest (test helper): `New(t, mods...) *Env`, `Env{Store, Actions, Router, Project, Token}`, `For`, `NewToken`, `Do`, `DoAuth`, `Decode`, `ErrorCode`, `RandomToken`, `ProbeModule`, `NoopMachines`; OpenAPI cache `<os.UserCacheDir()>/hlab/openapi/cloud.spec.json` (written by `task openapi`); range keys "4xx" are upper-cased before validation (libopenapi-validator only matches "4XX").

## E2E and doc tests

- `cli.Commands() []cli.Command`, `Command{Path string; Flags []string}` (Path without "hlab", e.g. "token create").
- `settings.Settings`: one nested struct per TOML table (`toml:"cloud"`), leaf fields with toml tags.
- `api.Deviations() []api.Deviation`, `Deviation{Endpoint, Description string}`, including every `unsupported_error`.
- `hlab cloud status --output json` prints `{"state": ...}` also while stopped; `hlab token create ... --output json` prints `{"token": ...}`; `hlab project list` shows names as words.
- VM name of a server in profile dev: `hlab-dev-server-<id>`; data folder `storage.data_folder`, default `%ProgramData%\hlab-dev\data`.
