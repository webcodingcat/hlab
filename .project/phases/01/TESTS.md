# Phase 01 test map

Subtest names are the requirement ID (`t.Run("API-02", ...)`), in a group
named after the component. End-to-end subtests live under `TestE2E` in
`test/e2e` (build tag `e2e`) and share one harness: build, profile guard,
dev cloud started once, one project `e2e-<time>` with a read/write token,
one Ubuntu 24.04 server shared by the server rows where possible.

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| API-02 | a request without token, with a malformed or unknown token gets `401` with code `unauthorized`; a valid token's request sees its own project only | unit | internal/api/auth_test.go: TestAPI/API-02 | 01-04 |
| API-10 | every response (success and error) of every endpoint served in this phase (`/actions`, `/locations`, `/datacenters`, `/server_types`, `/images`, `/ssh_keys`, `/servers`, 401, 404) validates against the frozen OpenAPI description; table over the endpoints | unit | internal/resources/openapi_test.go: TestResources/API-10 | 01-16 |
| API-01 | `GET http://api.hetzner.internal/v1/locations` with the token answers 200 from the host, from WSL2 (`wsl.exe -e curl`) and from a server (over SSH) | e2e | test/e2e/api_test.go: TestE2E/API-01 | 01-16 |
| PROJ-01 | `hlab project create`, `list`, `delete` against the management API change and show the projects; a duplicate name fails; every command except `cloud` fails with "the cloud is not running" while the management pipe is unreachable | unit | internal/cli/cli_test.go: TestCLI/PROJ-01 | 01-14 |
| PROJ-02 | `hlab token create` prints a token of exactly 64 characters `[A-Za-z0-9]` once, read or read/write per `--read-only`; `token list` never shows a value; the token authenticates against the API with its permission | unit | internal/cli/cli_test.go: TestCLI/PROJ-02 | 01-14 |
| PROJ-05 | in the non-elevated test process (asserted), `hlab project create/list` and `hlab token create` succeed with exit code 0 and no prompt | e2e | test/e2e/cli_test.go: TestE2E/PROJ-05 | 01-14 |
| CAT-01 | `/locations` lists exactly fsn1, nbg1, hel1, ash, hil, sin with their network zones; `/locations/{id}` returns one; `/datacenters` answers as the frozen description says (410) | unit | internal/resources/locations/locations_test.go: TestLocations/CAT-01 | 01-06 |
| CAT-06 | `/images?type=system` lists ubuntu-22.04, ubuntu-24.04, ubuntu-26.04, debian-12, debian-13 with Hetzner's attributes (`os_flavor`, `os_version`, `architecture` x86, `type` system, `status` available); `?name=` finds one | unit | internal/resources/images/images_test.go: TestImages/CAT-06 | 01-06 |
| CAT-07 | first `Ensure` downloads image and checksum file from the source (httptest), verifies, converts once and returns the cached base; a second call does not download or convert; a checksum mismatch fails and leaves no cached base; concurrent calls convert once | unit | internal/images/images_test.go: TestImages/CAT-07 | 01-08 |
| SRV-01 | a server created with `hcloud server create --image ubuntu-24.04 --ssh-key <key> --user-data-from-file` has its name as hostname, accepts the key for `root` and has run the user_data (marker file) | e2e | test/e2e/server_test.go: TestE2E/SRV-01 | 01-16 |
| SRV-03 | the server's public IPv4 is a /32 on eth0 with default route via 172.31.1.1 on-link, and `ssh root@<ip>` from the host works | e2e | test/e2e/server_test.go: TestE2E/SRV-03 | 01-16 |
| SRV-03 | allocation starts at `.4`, continues after the last address handed out even when a lower one was released, wraps to `.4` at the end of the range and skips used ones; full range gives an error | unit | internal/ipam/ipam_test.go: TestIPAM/SRV-03 | 01-16 |
| SRV-04 | a created server has `public_net.ipv6.ip` as a /64 in the API, distinct per server | unit | internal/resources/servers/servers_test.go: TestServers/SRV-04 | 01-16 |
| SRV-05 | from the server, `curl -sS https://cloud-images.ubuntu.com/` (DNS name and HTTPS) succeeds | e2e | test/e2e/server_test.go: TestE2E/SRV-05 | 01-16 |
| SRV-06 | `Up` issues no inbound NAT static mapping, port proxy or route that exposes the public range beyond the host; the firewall rule is limited to TCP 8780, the hlab interface, local 198.19.0.2, remote 198.19.0.1 and hlabd.exe; a second `Up` replaces the rule instead of adding one | unit | internal/hostnet/windows/hostnet_test.go: TestHostNet/SRV-06 | 01-11 |
| SRV-16 | `hcloud server delete` removes the VM and its disk file, and the next server does not get the released IP at once | e2e | test/e2e/server_test.go: TestE2E/SRV-16 | 01-16 |
| SRV-19 | the server's disk is a dynamic VHDX (`Get-VHD` VhdType Dynamic) under the configured data folder | e2e | test/e2e/server_test.go: TestE2E/SRV-19 | 01-16 |
| LIFE-01 | in the non-elevated test process, `hlab cloud stop` stops and `hlab cloud start` starts the dev cloud (service state and `hlab cloud status`) without a prompt | e2e | test/e2e/cloud_test.go: TestE2E/LIFE-01 | 01-15 |
| LIFE-10 | `Check` reports a fatal `other_instance_running` problem whose message names the other profile when its service runs, and none when it is stopped | unit | internal/hostnet/windows/hostnet_test.go: TestHostNet/LIFE-10 | 01-11 |
| DOC-01 | every command and flag of `cli.Commands()` appears in docs/user/commands.md | unit | test/docs/docs_test.go: TestDocs/DOC-01 | 01-17 |
| DOC-02 | every key of `settings.toml` (from the `settings` struct tags) appears in docs/user/configuration.md, and the custom server types file and catalog files are named there | unit | test/docs/docs_test.go: TestDocs/DOC-02 | 01-17 |
| DOC-03 | every entry of `api.Deviations()` appears in docs/user/deviations.md | unit | test/docs/docs_test.go: TestDocs/DOC-03 | 01-17 |
| DOC-04 | every relative link and anchor in README.md and docs/ resolves | unit | test/docs/docs_test.go: TestDocs/DOC-04 | 01-17 |
| DOC-05 | every view in `ui/frontend/src/views.json` has `ui/frontend/src/help/<view>.md` and every help file belongs to a view (empty list in this phase) | unit | test/docs/docs_test.go: TestDocs/DOC-05 | 01-17 |
| DOC-06 | every `task <name>` in docs/dev/*.md is a task in Taskfile.yml | unit | test/docs/docs_test.go: TestDocs/DOC-06 | 01-17 |

Not tested: LIFE-06 (`none`: manual start type and `AutomaticStartAction
Nothing` are configuration), DOC-07 (`manual`).
