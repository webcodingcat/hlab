# HLab

HLab is a local stand-in for Hetzner Cloud, the Storage Box and "the Internet":
a service that answers like Hetzner's APIs, with servers that are real VMs. Tools
such as OpenTofu (hcloud provider), the hcloud CLI and Ansible are meant to work
against it without changes.

**Status:** being rebuilt in Go; [docs/roadmap.md](docs/roadmap.md) lists what
`hlabd` answers so far: the catalog, SSH keys, servers (Debian 13, Ubuntu
26.04), Primary and Floating IPs, firewalls and private networks. On Windows 11 the
servers are Hyper-V VMs that the host and WSL2 reach on their addresses;
`hlabd --platform fake` runs the same API without VMs.

## Installation

On Windows 11 with Hyper-V, build the folder `build/package` in WSL2
(`bin/dev appliance`, then `bin/dev package`) and run, in a PowerShell as
administrator:

```powershell
build\package\install.ps1
```

It turns Hyper-V on, installs into `C:\Program Files\HLab` with a start menu
entry "HLab", and puts `hlab` on the PATH of Windows and of the default WSL
distribution. Run again with a newer package, it updates HLab while the
servers run on; `install.ps1 -Uninstall` removes it (`-KeepData` keeps the
data). Smart App Control has to be off. Details are in
[docs/tools.md](docs/tools.md) (Installing).

## Usage

HLab runs only when started: from the start menu ("HLab", in a minimized window)
or with `hlab start`, on Windows or in WSL2; Windows asks for administrator rights
each time. `hlab stop` ends it; its servers shut down and come back at the next
start. The log is `logs\hlabd.log` in the data folder (`C:\ProgramData\HLab`).

| Command | What it does |
|---|---|
| `hlab start` | start HLab (UAC) and wait until it answers |
| `eval "$(hlab env)"` | point hcloud, OpenTofu and Ansible at the lab: endpoints and the token `hlab-env` of the project `default`, the same in every shell (`--project`; `hlab env --shell powershell \| iex` on Windows) |
| `hlab status` | whether the lab runs, its endpoints and data folder |
| `hlab stop` | stop the lab and wait until it has stopped (`--no-wait`) |
| `hlab ssh SERVER [COMMAND...]` | log into a server as root with the lab's own key (`--project`; needs `ssh`) |
| `hlab project create\|list\|delete` | projects; a new lab has `default` |
| `hlab token create\|list\|delete --project NAME` | a project's API tokens; `create` prints the token once |

The endpoints are on `198.18.0.1`, which Windows, WSL2 and the servers share, so
`hlab env` prints the same values everywhere; from WSL2 the servers are reached on
their IPv4 addresses. The CLI finds the running lab by itself, through the data
folder that `hlabd` names in `C:\ProgramData\HLab\instance`.

Without a hypervisor, `hlabd` runs on `127.0.0.1` (Linux, WSL2 or Windows):

```bash
hlabd --platform fake --data ~/hlab-data
export HLAB_CONTROL=http://127.0.0.1:8090 HLAB_TOKEN_FILE=~/hlab-data/control.token
```

`hlabd` keeps its state, `hlab.yaml` and `catalog.yaml` in the data folder (default
`C:\ProgramData\HLab`, on Linux `/var/lib/hlab`) and writes a new control token
there at each start; only one `hlabd` runs per machine.

The web UI is at `http://127.0.0.1:8090` (`HLAB_UI`): projects, their tokens and the
catalog.

`hlab.yaml` sets the ports and how long the servers may take to shut down when
the lab stops:

```yaml
ports: {cloud: 8080, hetzner: 8081, control: 8090}
shutdown_timeout: 60   # seconds, then the servers are turned off
room:                  # what the servers may take of the host
  memory_reserve_percent: 25   # memory left free: this share,
  memory_reserve_min_mb: 6144  # but at least this much
  cores_per_thread: 4          # virtual cores per thread of the host
  disk_free_percent: 10        # share of the data folder's drive a create leaves free
```

`examples/tofu-1a` is a small OpenTofu configuration to try against the lab;
`examples/tofu-1b` adds a Debian server with a Primary IP of its own;
`examples/tofu-2a` a private network, a firewall, a Floating IP and two
servers. `bin/dev smoke` applies each of them against `hlabd --platform fake`
(docs/tools.md).

## Compatibility

- Windows 11 with Hyper-V, used from WSL2 (Ubuntu, in its default NAT mode) or
  Windows.
- Linux follows in a later phase.
- The tools and versions HLab is built for (hcloud CLI, the hcloud provider for
  Terraform and OpenTofu, the Ansible collection, Packer) are in
  [compat/versions.yaml](compat/versions.yaml).

## Development

```bash
bin/dev test
```

[DEVELOPING.md](DEVELOPING.md) explains the code, [docs/tools.md](docs/tools.md)
the tools, [STYLE.md](STYLE.md) the standards.
