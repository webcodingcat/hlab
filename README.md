# HLab

HLab is a local stand-in for Hetzner Cloud, the Storage Box and "the Internet":
a service that answers like Hetzner's APIs, with servers that are real VMs. Tools
such as OpenTofu (hcloud provider), the hcloud CLI and Ansible are meant to work
against it without changes.

**Status:** being rebuilt in Go, phase 1b (see [docs/roadmap.md](docs/roadmap.md)).
`hlabd` answers the catalog (locations, server and load balancer types, pricing,
images, ISOs), SSH keys, servers (Debian 13, Ubuntu 26.04; power actions) and
Primary IPs. On Windows 11 the servers are Hyper-V VMs that the host and WSL2
reach on their addresses; `hlabd --platform fake` runs the same API without VMs.
The earlier implementation (Docker and QEMU) is on the branch `legacy`.

## Installation

On Windows 11 with Hyper-V, build the folder `build/package` in WSL2
(`bin/dev appliance`, then `bin/dev package`) and run, in a PowerShell as
administrator:

```powershell
build\package\install.ps1
```

It turns Hyper-V on (Windows may want a restart), installs into
`C:\Program Files\HLab`, adds the start menu entry "HLab", puts `hlab.exe` on the
PATH and the Linux CLI into `~/.local/bin` of the default WSL distribution, and
adds the firewall rule "HLab". `install.ps1 -Uninstall` removes all of it with the
lab's VMs, switches and data (`-KeepData` keeps `C:\ProgramData\HLab` and the
VMs). Smart App
Control blocks HLab's unsigned programs; it has to be off.

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
catalog. `hlab.yaml` sets the ports, the time scale and how long the servers may
take to shut down when the lab stops:

```yaml
time_scale: 1   # 1 is Hetzner's speed, 0.5 twice as fast, 0 does not wait
ports: {cloud: 8080, hetzner: 8081, control: 8090}
shutdown_timeout: 60   # seconds, then the servers are turned off
```

`examples/tofu-1a` is a small OpenTofu configuration to try against the lab;
`examples/tofu-1b` adds a Debian server with a Primary IP of its own.

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

[DEVELOPING.md](DEVELOPING.md) explains the code and the tools, [STYLE.md](STYLE.md)
the standards.
