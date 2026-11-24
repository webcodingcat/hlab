# Phase 25 context

Confirmed by the user on 2026-11-24 without corrections.

## Assumptions

- The fault model validates all nine kinds and ends faults on expiry, target or project deletion and cloud stop; only `api_error` and `rate_limit` have an effect yet, later kinds are refused with "this kind is not available yet" until phases 26/27 add them.
- An `api_error` hit refuses the request before hlab acts on it; the message names the injecting HLab fault and its ID.
- Reads are GET and HEAD, writes everything else; the resource filter is the first path segment after `/v1` or `/hetzner/v1`; an unknown segment is invalid input.
- Any code of hlab's Hetzner error table can be injected, with its usual HTTP status.
- Without a given seed hlab picks one below 2^53; any 64-bit seed is accepted through the management API and the CLI.
- Matching requests draw in arrival order, so the same seed repeats the same hits; non-matching requests draw nothing.
- A `rate_limit` of N per hour starts with N tokens and regains one every 3600/N seconds; the eleventh request in a row at 10/h gets `429 rate_limit_exceeded`.
- One bucket per project covers the Cloud API and the Storage Box API.
- The rate limit is checked before `api_error`; a 429 does not count against the limit nor for the error share; an injected error does count against the limit.
- Every response of both APIs carries `RateLimit-Limit`, `RateLimit-Remaining`, `RateLimit-Reset` (errors, 401, unknown paths included); 401 shows 3600/3600 and never counts.
- Without a `rate_limit` fault each project's token requests lower `RateLimit-Remaining` from 3600, refilling one per second, stopping at 0 without refusing; tray requests through the management API never count, are never refused or hit.
- `RateLimit-Reset` is the Unix time in seconds when the bucket is full again (now while full).
- When a `rate_limit` fault ends the project returns to the default headers; a new limit starts with a full bucket.
- Fault IDs restart at 1 after every cloud start; durations like "5m", from 1 s to 24 h.
- `hlab cloud status` shows "N faults active"; `GET /mgmt/v1/status` carries `faults`.
- `hlab fault create --project <p> <kind>` with target flags (`--server`, `--volume`, `--storage-box`, `--from`/`--to`), parameter flags (`--traffic`, `--loss-percent`, `--delay-ms`, `--jitter-ms`, `--code`, `--percent`, `--only`, `--resource`, `--per-hour`, `--iops`), `--seed`, `--duration`; prints the new fault with ID and seed.
- `hlab fault list` shows ID, project, kind, target, parameters, seed, remaining time; `--output json` gives the objects.
- `hlab fault delete` ends one fault (`--project p <id>`), a project's faults (`--project p --all`) or all (`--all`).
- MCP tools `fault_create`, `fault_list`, `fault_delete` take the management API's fields and return the `--output json` shape; while the cloud is stopped they answer "the cloud is not running".
- Through MCP a seed is at most 2^53-1 (larger refused with a tool error, since the MCP library parses numbers as floats); `docs/commands.md` states this.
- hcloud, OpenTofu and Ansible retry `rate_limit_exceeded` up to five times (about 5 to 31 s); refused requests do not count, so retries use no budget.
- Guide examples for `api_error` use `service_error`/`server_error` (not retried by clients) and `conflict` (retried); the guide says which client retries which codes.
- Success criterion 2 is checked with `service_error`; criterion 3 with `hcloud server list` (one request per call).
- Docs: `docs/commands.md` describes `hlab fault` and the three MCP tools; `docs/guide.md` gets "Fault injection" with `api_error` and `rate_limit` examples; `docs/deviations.md` says hlab never limits requests without a `rate_limit` fault.

## Technical choices

- New package `internal/faults`: in-memory `Manager` with mutex, injectable clock and timers, and a kind registry (validator plus optional `Apply`/`Undo` effect) that phases 26/27 extend; unit tests use a fake effect.
- Target lookup through a function the cloud supplies (resource exists in project, volume attached for `volume_unavailable`, location in catalog).
- `faults.Middleware` between token auth and `checkJSON`, reading a new mark `api.ByToken(ctx)` set only by the token path of `authenticate`; `WithProject` stays unmarked; an outer layer in `underPrefix` sets default headers on responses that never reach it.
- The router takes the fault layer as an optional `func(http.Handler) http.Handler` in `api.Deps` to avoid an import cycle; `faults` uses `api.Error`/`api.Status`.
- `api_error` uses one `rand.New(rand.NewPCG(seed, seed))` per fault under the manager lock, one `IntN(100) < percent` draw per matching request; injected `invalid_input` carries `details.fields: []`.
- Token buckets per project as float tokens with lazy refill; default bucket kept apart from the fault bucket.
- `EndTarget` wired into server, volume and storage box deletes; `EndProject` in `mgmt.deleteProject`; `EndAll` in `cloud.teardown` before the API servers shut down (skipped at host shutdown); a new Manager at every cloud start.
- Management routes in `internal/mgmt` through `mgmt.Deps.Faults` (nil leaves them out); `Status` gets additive `Faults int`.
- CLI parser gains optional positional arguments and many optional flags; `fault create` builds the JSON body from flags.
- MCP tools added to `mcpTools` sorted by name; argument structs: `target *faultTarget` omitempty, `params map[string]any` omitempty, `seed *uint64`, `duration` string omitempty; `fault_delete`: `project` string omitempty, `id *int64`, `all bool`; hand-written input schemas add `maximum: 9007199254740991` to `seed`, no `default` keywords (go-sdk panics on them).
- MCP unit tests cover omitted versus null of `target`, `seed` and `id` once through the in-memory transport.

## Answers

- No questions were asked.
