# Phase 02 context

Confirmed with the user at the start of the phase (2026-11-18).

## Assumptions confirmed

- `api.List` handles `name`, `label_selector`, `sort`, `page`/`per_page` in one place; each endpoint offers exactly the parameters and sort keys of the frozen OpenAPI description; 25 per page by default, at most 50; an unknown sort key or bad selector gives `invalid_input`.
- One shared label validation and selector (`labels.Validate`, `labels.Selector`) with all Hetzner operators; PUT replaces the whole label set.
- API-07: `PUT /ssh_keys/{id}`, `PUT /servers/{id}`, `PUT /placement_groups/{id}` (name and labels).
- API-11: one store-side uniqueness check per project and kind; SSH keys also unique by fingerprint (`uniqueness_error`, field `public_key`).
- API-12: servers get `change_protection` (delete, rebuild); protected delete refused now, rebuild refusal tested in phase 03.
- API-13: one helper maps `actions.ErrLocked` to `locked`; tested on servers.
- API-08: generic action endpoints for servers; create reports progress 0 to 100.
- API-03, API-04, API-14, PROJ-03: existing behaviour, proven by tests (API-03/04 table-driven over all endpoints).
- PROJ-04: `resource.Reconciler` in `internal/resource` as ARCHITECTURE.md defines it; servers implement only `Delete` now; project delete revokes tokens first, then deletes every resource.
- CAT-02 to CAT-05, KEY-01, SRV-17, SRV-22, HOST-03, HOST-04 and the harness as the planner assumed (catalog files and update, `resource_unavailable` for arm, pricing from merged types, spread groups up to 10 servers, `request_console` as `unsupported_error`).
- The harness checks the pinned tofu and Ansible versions and installs nothing.
- Documentation: commands.md, configuration.md, deviations.md, README (WSL2, endpoints).

## Answers

- HOST-04 detection: hlabd reads the console user's `%UserProfile%\.wslconfig` (`networkingMode`, `dnsTunneling`) and checks the host adapters in `hostnet.Check`; VirtioProxy fallback stays undetected (recommendation taken).
- E2E rows for success criteria 1 and 4 are named CLI-02 and CLI-03 rows for ssh_keys, placement_groups and `server_info`; CLI-02/CLI-03 stay open until phase 13 (recommendation taken).
- PROJ-04 completion: add `GET /mgmt/v1/actions/{id}`; `hlab project delete` waits for success or error; ARCHITECTURE.md is updated with the management API (recommendation taken).

## Corrections by the user

- API-09: the HTTP status of every error code comes from one table in one place in the code, taken from the error table of the frozen OpenAPI description (`info.description`), never from phase 01 or memory. The table there: json_error 400, unauthorized 401, forbidden 403, maintenance 403, resource_limit_exceeded 403, not_found 404, method_not_allowed 405, uniqueness_error 409, conflict 409, deprecated_api_endpoint 410, resource_unavailable 412, invalid_input 422, service_error 422, unsupported_error 422, locked 423, protected 423, rate_limit_exceeded 429, server_error 500, bad_gateway 502, unavailable 503, timeout 504.
- API-09 live check (read-only, hcloud context HLab, 2026-11-18): `GET /v1/servers?label_selector=!!!==` answered `HTTP/2.0 422` with code `invalid_input`. So `invalid_input` moves from 400 to 422; `protected` is 423 (not 403).
- E2E: Ansible runs in the Ubuntu distribution SPEC.md names ("one Ubuntu distribution, 24.04 or newer"), selected explicitly with `wsl.exe -d <name>`, never the default distro. The harness copies the test key into a temporary directory inside that distro (mode 0600) and deletes it at the end of the run.

## Added during the phase

- 02-12 review: `DELETE /placement_groups/{id}` removes a group that still has members. Plan 02-13 makes the server object show its group and must keep it consistent: deleting a group clears the servers' `placement_group` reference (or refuses, if RESEARCH or the description says Hetzner refuses).
- 02-03: the updated and custom catalog files live in the config root (`Paths.Config`, `%ProgramData%\hlab[-dev]\catalog\server-types.json` and `server-types.custom.json`), as ARCHITECTURE.md "Files and formats" says, not in the data folder. The CAT-03 e2e row (test/e2e/catalog_test.go) saves and restores `dataFolder()\catalog\server-types.json`, the wrong file; plan 02-07 changes what it uses (the path), not what it checks.
