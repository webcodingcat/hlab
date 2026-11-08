# Servers, Primary IPs and the lab's own resources

How servers and Primary IPs behave, and the lab's own operations on
projects, tokens and faults. The rules they keep are in DEVELOPING.md
(Actions, Behaviour that changes must keep); section names in parentheses
point to this file unless they name another.

## Servers and Primary IPs

The handlers in `internal/api/cloud/servers.go` and `primary_ips.go` decide;
`internal/lab/servers.go` does the slow part with IDs, through `system`.

- **Create**: the handler records the server with its Primary IPs (status
  `initializing`) and runs the table's rows `create` (`create_server`) and,
  unless `start_after_create` is false, `poweron` (`start_server`, the
  answer's `next_actions`), all in one `Lab.Create`. The create's steps are
  `Lab.CreateServer`; its `Lab.BuildServer` takes the system image from the
  cache, makes the differencing disk, the
  seed and the VM, and sends the appliance the pictures with the server. If
  it fails, the steps remove the VM, the disk and the seed, and the
  action's undo removes the record and its new Primary IPs. A create that
  HLab's end cut short is built anew at the next start.
- **Delete** is the row `delete` (`delete_server`): the record goes at once,
  as on Hetzner, with its Primary IPs that have `auto_delete`; the others
  stay, unassigned. The steps remove the VM, the disk and the seed.
- **Power.** `poweron`, `poweroff`, `shutdown`, `reboot` and `reset` set the
  statuses Hetzner shows (`starting`, `stopping`, …) and wait its times.
  - A reboot asks the guest to shut down (Hyper-V has no ACPI reboot), turns
    it off after a minute and starts it.
  - A reboot or reset of an off server starts it, as on Hetzner.
  - Each sets the status first and then calls the system, because Hetzner
    shows the new status as soon as the action runs. If the call fails, the
    status stays as the steps left it; only a start of an off server sets it
    back to `off` (Room).
  - `want_running` changes only in the plan, when the action is asked for:
    a start sets it, a `poweroff` clears it (Room). The steps leave it, so
    queued actions leave it in the order they were sent: a `poweroff` sent
    during the create ends with the server off and not wanting to run.
- **The watchdog** looks every 2 s at the VMs of the servers that no action
  works on and that are `running`, `starting` or `stopping`. A running
  server whose guest shut itself down is `off` and no longer wants to run.
  A server that a failed action left `starting` or `stopping` gets its VM's
  status, `running` or `off`; `want_running` stays.
- **Who sets a server's status:** the steps of its actions while they run
  (flagged); the watchdog for a server no action works on; the start of the
  lab, which makes every status follow its VM (`resumeServers`). Who sets
  `want_running`: the create, the plans of the power actions, the watchdog
  for a guest that shut itself down.
- **`want_running`** is what a start of the lab follows: a server whose VM
  runs is taken over (hlabd had crashed); one whose VM is off gets its NIC on
  `HLab-Public` again (detached and attached, as a stop of the lab removed the
  switch) and starts if it wants to run. A server without a VM gets a new one
  first (`Lab.restoreServer`): with its disk as it is, if the disk is there
  (an uninstall kept the data folder but removed the VMs); built anew if
  there is no disk (hlabd's end cut its create short). `system.CreateServer`
  keeps a disk that is there and never removes it on a failure. A stop shuts
  every server down, all at once, and turns it off after `shutdown_timeout`
  (`hlab.yaml`, 60 s); `want_running` stays.
- **Room.** A create or a start (`poweron`, and `reboot` or `reset` of an off
  server) beyond the host's room is Hetzner's 412 `resource_unavailable`.
  - Memory (`Lab.CheckRoom`, from `Hypervisor.HostResources`): memory is
    static, so the servers that want to run may take all of the host's memory
    except a reserve (25 %, at least 6 GB) and the appliance's 1 GB.
  - Cores: four virtual cores per thread of the host.
  - Disk, for a create (`Lab.CheckDisk`, from `Hypervisor.DiskSpace`): a
    tenth of the data folder's drive stays free, counted as it is now.
    Dynamic disks take only what the guests wrote, so the servers' disks may
    be larger together than the drive.
  - The room counts the servers whose `want_running` is set. A start sets it
    in its plan and a `poweroff` clears it there, so actions sent at once, or
    queued behind each other, count each other before their steps run. If a
    start's steps fail, the server is off again. An example on a host with
    room for two servers, `a` and `b` running, `c` off: a `poweroff` of `a`
    clears its `want_running` at once, so a `poweron` of `c` right after it
    fits and sets `c`'s; a `poweron` of `a`, queued behind its `poweroff`,
    then finds `a` not wanting to run, checks the room and gets 412.
  - Without the machine started (the API's unit tests) there is always room
    for memory, and the fake reports a drive of 1 TB, half free.
- **Addresses** come from `198.19.0.0/16` (IPv4, without `.0` and `.255`) and
  `2001:db8:1::/48` (one `/64` per IPv6), in order: `state.Lab.Pools` holds
  where the next search starts, so a released address comes back only once the
  pool has gone round (`lab.NewAddress`).
- **Names and MACs.** A server's VM is `hlab-server-<id>`, its public NIC has
  the MAC `96:00:` and its ID in 32 bits; its disk is
  `disks/servers/<id>.vhdx`, its seed `disks/seeds/<id>.iso`.
- **The lab's key.** `hlabd` keeps an SSH key in the data folder, `lab-key`
  (Ed25519 in OpenSSH's format, `internal/sshkey`, made at the first start,
  readable like the control token). Every server's root takes its public key
  besides the server's own keys, from the seed and the metadata service
  (`picture.ServerKeys`); `hlab ssh` logs in with it. A server created
  before the key existed does not have it.
- **The seed** (`internal/seed`) is what cloud-init reads on Hyper-V, where the
  SMBIOS vendor cannot say "Hetzner": `meta-data` (instance ID, host name,
  keys), `user-data`, `vendor-data` (root's login, the same text the metadata
  service answers) and `network-config` version 2: IPv4 by DHCP and IPv6 as
  `::1` of the `/64` with `fe80::1`, as on Hetzner, always with the lab's
  resolvers. Its files are JSON, which is YAML too: cloud-init's YAML 1.1
  parser reads an unquoted MAC of digits as a number.

**The image cache.** The catalog names each system image's source: the
official qcow2 file (Debian's genericcloud, Ubuntu's cloud image) and its
list of checksums. The first server of an image loads it
(`system.LoadImage`): download, checksum, `internal/imagefmt` reads the
qcow2 in Go and writes the disk as a fixed VHD (the raw disk and a 512-byte
footer), and `FlattenDisk` (`Convert-VHD`) makes it a dynamic VHDX,
`images/cache/<sha256>.vhdx`. `state.Lab.Images` records it by the image's
name; one load per image runs at a time. Converting through the appliance
(`qemu-img`) is for imports and exports (3b). A cached image whose source in
the catalog changed is loaded anew. Each start of the lab removes the files
that neither a cache entry nor a server's disk needs, what a load cut short
left, and the disks and seeds of servers that are gone (a project reset
during a create).

## Projects, tokens, faults

These are the lab's own operations (`internal/lab/projects.go`, `tokens.go`,
`faults.go`). The lab control (`/_lab/...`) and the CLI call them. They report
errors as `lab.ErrInvalid`, `lab.ErrExists` and `state.ErrNotFound`.

- **Projects** have a name, limits and Internet access.
  - The name must be a DNS label, because it appears in URLs of the lab control and
    in log file names.
  - A limit of zero means no limit; a negative limit is refused.
  - Internet access is on by default.
  - `Lab.DeleteProject` first removes the records. Its tokens are gone then, so no
    new request is admitted. Then it cancels the running actions, which find their
    actions gone.
  - A request admitted just before the delete may still try to create something.
    So every create calls `lab.CheckProject` inside its `Change` (`Lab.Act` does
    so itself); the API answers `lab.ErrProjectGone` as a deleted token
    (`web.TokenInvalid`).
- **Tokens** are 64 letters and digits, like Hetzner's, with the permission `read`
  or `read_write`. Names are unique per project, so the CLI can refer to a token by
  name. `Lab.CreateToken` returns the token only once. The state keeps its SHA-256
  (`state.HashToken`) and its first four characters, to tell tokens apart.
- **The env token.** `Lab.EnvToken` returns a project's `read_write` token named
  `hlab-env`. It returns the same token on every call and after a restart, so
  `hlab env` gives every shell the same token, in WSL2 and Windows alike.
  - The state cannot show a token a second time, so the clear text is kept in
    `env-tokens.json` in the data folder (`lab.EnvTokenFile`).
  - That file is written like the control token and readable by the same users.
    This adds no risk: whoever can read the control token can create tokens anyway.
  - If the token is gone (deleted, or the file was lost), a new one is made. The
    file keeps only projects that still exist.
- **API faults.** `Lab.SetAPIFault` makes a project's next n API calls fail with a
  given status, each after a delay. `Lab.TakeAPIFault` counts one call against the
  fault. The counter is in the state (`state.Lab.Faults`), so it survives a restart.
  A call that finds no fault saves nothing.
