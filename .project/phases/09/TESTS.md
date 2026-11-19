# Phase 09 test map

| Requirement | Behaviour | Level | Test | Plan |
|-------------|-----------|-------|------|------|
| HOST-02 | `public_range` accepts an IPv4 /8 to /28 and normalises it; refuses non-IPv4, other lengths and overlaps with 0/8, 10/8, 100.64/10, 127/8, 169.254/16, 172.16/12, 192.168/16, 224/3, 185.12.64.1, 185.12.64.2, naming `network.public_range` (table) | unit | internal/settings/settings_test.go: TestSettings/HOST-02 | 09-04 |
| HOST-02 | host, appliance, API and agent addresses derive from a custom range (default and one other range) | unit | internal/transit/transit_test.go: TestTransit/HOST-02 | 09-04 |
| HOST-02 | a range overlapping a host address or route is a fatal `range_collision` naming the interface or route; a disjoint range gives none (table) | unit | internal/hostnet/windows/hostnet_test.go: TestHostNet/HOST-02 | 09-06 |
| HOST-02 | IPs in use outside a changed range are reported with the recorded old range; none when all lie inside | unit | internal/ipam/ipam_test.go: TestIPAM/HOST-02 | 09-05 |
| LIFE-02 | `Halt` "save" saves running VMs, keeps their status `running`, sets `off` for off VMs, falls back to shutdown and hard off when a save fails; at a host shutdown it changes nothing; `Restore` resumes saved servers with status running and leaves `off` ones | unit | internal/resources/servers/lifecycle_test.go: TestServers/LIFE-02 | 09-07 |
| LIFE-03 | `Halt` "shutdown" asks every running guest, turns off what still runs after the (short) deadline; `Restore` powers on servers with status running and leaves `off` ones off | unit | internal/resources/servers/lifecycle_test.go: TestServers/LIFE-03 | 09-07 |
| LIFE-02 | with "save", after `hlab cloud stop` and `start` the running server resumes with continuous uptime (same boot ID, uptime grew) | e2e | test/e2e/life_test.go: TestE2E/LIFE-02 | 09-12 |
| LIFE-03 | with "shutdown", after stop and start the server that ran is running with a new boot, the stopped server stays off | e2e | test/e2e/life_test.go: TestE2E/LIFE-03 | 09-12 |
| LIFE-04 | after each stop/start cycle the fixture works: SSH to the server's IPv4, private network, floating IP, firewall (allowed port open, other closed), LB TCP service, volume mounted with its data, zone record resolves from the host | e2e | test/e2e/life_test.go: TestE2E/LIFE-04 | 09-12 |
| LIFE-07 | while stopped no route of the public range via the hlab adapter and no NRPT rule with the profile tag exist and forwarding of the hlab and WSL adapters equals `hostnet-restore.json`; after start route and NRPT rules (base zones and the fixture zone) are back | e2e | test/e2e/life_test.go: TestE2E/LIFE-07 | 09-12 |
| LIFE-08 | `store.Open` returns `*IncompatibleError` for a higher version (Newer, message names a newer hlab version), a lower unknown version and a non-database file; versions 0 and 1 open (table) | unit | internal/store/store_test.go: TestStore/LIFE-08 | 09-02 |
| LIFE-08 | `store.Discard` renames the file with its `-wal`/`-shm` to `.discarded-<time>`, and the next `Open` gives an empty state | unit | internal/store/store_test.go: TestStore/LIFE-08/discard | 09-02 |
| LIFE-08 | `discard.Run` deletes the profile's server VMs, `disks`, `volumes`, `vms` and `image-*.vhdx`, keeps the appliance, the image and ISO cache; `Requested` is false for a compatible state or without the parameter | unit | internal/discard/discard_test.go: TestDiscard/LIFE-08 | 09-10 |
| LIFE-08 | `hlab cloud start --discard-state` starts the service with parameter `discard-state`; `cloud status` of a refused start shows the warning | unit | internal/cli/cli_test.go: TestCLI/LIFE-08 | 09-11 |
| LIFE-09 | the writer rotates at the size limit to `.1`..`.5`, drops the oldest, never splits a line | unit | internal/logfile/logfile_test.go: TestLogfile/LIFE-09/rotate | 09-03 |
| LIFE-09 | `Tail` returns the last n lines over rotated files and follows new lines across a rotation until cancelled | unit | internal/logfile/logfile_test.go: TestLogfile/LIFE-09/follow | 09-03 |
| LIFE-09 | `hlab logs` shows the lines through the management API while running and from the files while stopped, human or `--output json`, and `--follow` prints a line written later | unit | internal/cli/cli_test.go: TestCLI/LIFE-09 | 09-11 |
| LB-01 | a load balancer without IPv6 primary IP shows `2001:db8:a000:<id>::1` | unit | internal/resources/loadbalancers/loadbalancers_test.go: TestLoadBalancers/LB-01/create | 09-08 |
| LB-09 | `Recover` moves a PTR entry from the old formal /64 to the new address, a second run changes nothing, an LB with an IPv6 primary IP keeps its entries | unit | internal/resources/loadbalancers/loadbalancers_test.go: TestLoadBalancers/LB-09/recover | 09-08 |

The LIFE-02, LIFE-03, LIFE-04 and LIFE-07 rows run alone before the
parallel rows of TestE2E. They share one lazily built fixture and one
stop/start cycle per stop behaviour, and restore settings.toml at the end.
LIFE-05 (manual) has no row. DOC-01 to DOC-06 keep their rows in
test/docs/docs_test.go (TestDocs/DOC-0n); DOC-07 is manual.
