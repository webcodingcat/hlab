# Fault injection

Decided with the user on 2026-11-24 in `/wcc:change`; moved from the target
vision into milestone 1.

## Decision

- Faults belong to a project and hit only its resources and its clients.
  The tray's project console has an entry "Faults" to create, see and end
  them; the top bar shows "N faults active" while any fault of any project
  is active, leading to a list of all active faults with "End all"; the
  tray menu has "End all faults" while faults are active.
- A fault is active until it is ended, its optional duration runs out, its
  target or project is deleted, or the cloud stops. Faults live in memory
  only; after a cloud start none is active and every resource behaves
  normally.
- Ways to control faults: the tray, the management API, `hlab fault ...`
  and the MCP tools `fault_create`, `fault_list`, `fault_delete`.
- Kinds:
  - `server_crash`: the server's VM is turned off hard and the server shows
    `off`, like after a host failure. While the fault is active, actions
    that start the server (poweron, reboot, reset) fail; ending the fault
    leaves the server off.
  - `server_freeze`: the VM is paused; the API keeps showing `running`, the
    server answers nothing. Ending the fault resumes it. A power action on
    the server (reset, poweroff, ...) ends the freeze first, like resetting
    a hung server.
  - `network`: per server, for its public traffic, its private traffic or
    both: cut completely or drop a share of the packets (1-100 %). A cut
    hits everything, also hlab's own services the server uses through the
    appliance (DNS, metadata service, API); the console in the tray keeps
    working, since it does not use the network. After the fault ends the
    server reaches everything again without a restart, shown by a test.
  - `latency`: a delay with optional jitter, between the project's servers
    in two locations (for example fsn1 and hel1) or for one server.
  - `api_error`: a Hetzner error code with its HTTP status for a share of
    the project's requests (1-100 %), optionally only reads or only writes
    and only one resource type (for example `servers`), in the Cloud API
    and the Storage Box API.
  - `rate_limit`: a limit of N requests per hour with Hetzner's behaviour
    (`429 rate_limit_exceeded`, refill over the hour). Every response of
    both APIs carries Hetzner's `RateLimit-*` headers, with Hetzner's
    default of 3600 when no fault is active, never limiting then.
  - `volume_unavailable`: the volume's disk disappears from its server, so
    I/O fails; ending the fault attaches it again. The API keeps showing it
    attached. In the guest (E28): a filesystem nobody uses is unmounted by
    systemd, and later writes under the empty mount point land on the
    system disk; a busy one stays mounted and every access fails with an
    I/O error. After the fault ends the disk is back, possibly under
    another `sdX` name but always under its `/dev/disk/by-id` name, and
    has to be mounted again (`umount -l` first in the busy case); the data
    is intact.
  - `disk_slow`: a volume or a server's system disk is limited to N I/Os of
    8 KiB per second (Hyper-V's normalized IOPS).
  - `storage_box_unavailable`: a storage box is unreachable on every
    protocol until the fault ends.
- Seeds: `api_error` and the packet loss of `network` take an optional seed.
  With the same seed and the same sequence of requests or packets, the same
  requests or packets are hit, so a test can repeat a run. Without a seed
  hlab chooses one and shows it with the fault.
- API errors and rate limits hit only requests with a project token
  (clients, scripts); the tray console, which goes through the management
  API's project proxy, is never affected.

## Rejected alternatives

- Faults only in an Administration view with project selection, without a
  hint in the top bar: active faults would go unnoticed.
- Faults that survive a cloud restart: a forgotten fault would keep
  breaking the cloud.
- Only the management API, or no MCP tools: goal 5 gives AI agents the
  functions of `hlab`.
- Only a hard crash as server failure: a hung server is the harder case
  for monitoring and failover.
- Network faults only per server: latency between locations is the case
  cluster setups across locations need.
- API errors without filters and without a seed: tests need to hit one
  kind of request, repeatably.
- Rate limits without headers: clients read Hetzner's headers.
- API errors in the tray console too: the console must stay usable to end
  the faults.
