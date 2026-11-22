# Command reference

`hlab` manages the local cloud: start and stop, projects, API tokens,
the server type catalog and the HLab CA; `hlab mcp` offers the same to AI
agents as an [MCP server](#mcp-server).
The installation puts it on `PATH`; in a working copy it is
`bin\hlab.exe`. The examples write `hlab`.

`hlab cloud start`, `stop` and `status` work at any time. All other
commands except `hlab logs`, `hlab version` and `hlab mcp` talk to the running cloud
and fail with `hlab: the cloud is not running` while it is stopped.

## Output and exit codes

Every command prints text for people. `--output json` prints JSON instead,
for scripts (every command except `hlab mcp`):

```
> hlab version --output json
{
  "version": "dev"
}
```

Exit codes: `0` success, `1` error, `2` wrong command line (the usage is
printed). Errors go to standard error, prefixed with `hlab:`.

## Cloud

### hlab cloud start

Starts the cloud: virtual switch, appliance VM, host networking and the
API. The servers that ran at the last stop are resumed or powered on;
servers that were off stay off. The command returns at once; follow the
start with `hlab cloud status`. No administrator rights are needed.

```
> hlab cloud start
the cloud is starting
```

`--discard-state` confirms that HLab may throw away a state it refused to
start with: a state file this version cannot read (written by a newer
HLab version, or damaged), or a state that holds addresses outside a
changed `public_range` (see
[configuration](configuration.md#settingstoml)). HLab then renames the
state file to `state.db.discarded-<UTC time>`, deletes the server VMs with
their disks, volumes and snapshots, removes the old host network settings
and DNS rules, and starts with an empty state. Downloaded images and ISOs,
the appliance and the HLab CA stay. A start refused for such a state shows
`state: refused` and names this flag on the `problem:` line of
`hlab cloud status`. A state that can be used is never discarded; the flag
is then ignored and the log says so:

```
> hlab cloud start --discard-state
the cloud is starting
> hlab logs
...
2026-11-19 19:22:23 INFO discard of the state requested, but the state is compatible; it is kept
```

### hlab cloud stop

Stops the API, then saves or shuts down the running server VMs as
`stop_behaviour` in [settings.toml](configuration.md#settingstoml) says
at that moment, then stops the appliance and removes the host networking.
With `"shutdown"`, servers whose guest has not shut down after 60 seconds
are turned off.

```
> hlab cloud stop
the cloud is stopping
```

### hlab cloud status

Shows the state (`running`, `stopped`, `starting`, `stopping`), the version,
any problem found at start, and the DNS zones that hide a public domain.

```
> hlab cloud status
state: running
version: dev
> hlab cloud status --output json
{
  "problems": [],
  "shadowing_zones": [],
  "state": "running",
  "version": "dev"
}
```

When the last start was refused, for example because the cloud of the
other profile runs, the status shows `state: refused` and the reason on a
`problem:` line.

While the cloud runs, the host, WSL2 and the servers resolve the names of
a zone you create in HLab through HLab, so a zone such as `example.com`
hides the real domain. HLab asks the public DNS for each primary zone when
it is created or imported and at every cloud start; when the domain
exists, the status shows one line per zone, and the JSON output lists it
under `shadowing_zones` with `zone` and `project`:

```
warning: zone example.com (project demo) shadows a public domain
```

Zones under `internal`, `test` and `home.arpa` and secondary zones are
never listed, and neither is a zone whose check got no answer.

## Projects

### hlab project create

Creates a project from its display name, the name shown in the window.
Quote a display name with spaces. HLab derives the project's name from
it and prints that name; commands, the token commands and the window's
addresses use the name.

```
> hlab project create "Web Coding Cat"
project web-coding-cat created
> hlab project create "München Büro"
project muenchen-buero created
```

The name is the display name in lower case with ä, ö, ü and ß written as
ae, oe, ue and ss and other accents dropped (é becomes e); every other run
of characters, spaces included, becomes one `-`, and no `-` stays at
either end. Letters without an accent to drop, such as ø or æ, become
`-` as well. A name is at most 255 characters. A display name is 1 to 255
characters after leading and trailing spaces are removed and holds no
control characters.

The command fails with exit code 1 when the display name is empty, when
nothing is left of it for the name, or when the name is taken; two
display names that differ only in case give the same name:

```
> hlab project create "  "
hlab: the display name is empty
> hlab project create "!!!"
hlab: the display name gives an empty project name
> hlab project create "Docs"
hlab: project docs already exists
```

### hlab project list

Lists the projects by name with their number of resources, creation time
and display name. `--output json` adds `resources_by_type`, the number of
resources per section of the project console.

```
> hlab project list
NAME            RESOURCES  CREATED              DISPLAY NAME
muenchen-buero  3          2026-11-21 10:14:40  München Büro
web-coding-cat  0          2026-11-21 10:12:03  Web Coding Cat
```

### hlab project delete

Deletes the project of the given name (not its display name) with
everything in it: it revokes the project's tokens
at once, waits until running actions of the project (a server creation,
for example) have finished, then deletes everything the project owns:
servers with their VMs and disks (protected servers too), volumes with
their disk files, snapshots and backups, networks, primary and floating
IPs, firewalls, load balancers, certificates, DNS zones, SSH keys and
placement groups. The command
returns when all is gone, which takes a while for many servers. If a
resource cannot be deleted, the command fails with
`hlab: deleting project <name> failed: <reason>`; the project stays with
what is left in it but without tokens, and you can run the command again.

```
> hlab project delete demo
project demo deleted
```

## Tokens

Tokens are the API tokens of a project for hcloud and other clients
(`HCLOUD_TOKEN`). Token names are 1 to 63 characters, unique per project.

### hlab token create

Creates a token with `--project <project>` and `--name <name>`. Add
`--read-only` for a token that can only read. The command prints the
64-character token once, alone on its line; it cannot be shown again.

```
> hlab token create --project demo --name ci
<64-character token>
> hlab token create --project demo --name viewer --read-only --output json
{
  "name": "viewer",
  "read_only": true,
  "created": "2026-11-18T11:56:56.2027341Z",
  "token": "<64-character token>"
}
```

### hlab token list

Lists the tokens of `--project <project>` without their values.

```
> hlab token list --project demo
NAME    ACCESS      CREATED
ci      read/write  2026-11-18 12:56:56
viewer  read-only   2026-11-18 12:56:56
```

### hlab token revoke

Revokes the named token of `--project <project>`; clients using it are
rejected from then on.

```
> hlab token revoke viewer --project demo
token viewer revoked
```

## Catalog

### hlab catalog update

Updates the server type catalog from the real Hetzner Cloud API with a
Hetzner API token of yours. On a terminal the command asks for the token
without showing it; in a script, put the token in an environment variable
and name it with `--hetzner-token-env <variable>`. Without a terminal and
without that flag the command exits with `2`. HLab uses the token for this
one update and never stores or logs it. The updated types are served at
once and kept for later starts; your own server types stay.

```
> hlab catalog update
Hetzner API token:
server type catalog updated: 14 server types
> $env:HZ_TOKEN = "<your Hetzner token>"
> hlab catalog update --hetzner-token-env HZ_TOKEN
server type catalog updated: 14 server types
```

Without a terminal and without the flag:

```
> hlab catalog update
hlab: no terminal to ask for the Hetzner token; pass it in an environment variable named with --hetzner-token-env
```

## Certificate authority

The HLab CA signs the managed certificates of load balancers; see
[Load balancers and certificates](guide.md#load-balancers-and-certificates).

### hlab ca export

`hlab ca export <file>` writes the certificate of the HLab CA (PEM) to the
file, for clients that do not use the host's trust store, such as
`curl --cacert` or a server that gets it through cloud-init. The file
holds no private key.

```
> hlab ca export hlab-ca.pem
HLab CA written to hlab-ca.pem
```

### hlab ca trust

`hlab ca trust on` adds the HLab CA to the trust store of Windows
(Local Computer, Trusted Root Certification Authorities) and of every
WSL2 distribution, as `/usr/local/share/ca-certificates/hlab-ca.crt`;
`hlab ca trust off` removes it from both. Read the
[risk](guide.md#trusting-the-hlab-ca) first.

- The switch is kept in `[certificates] trust_ca` of settings.toml; while
  it is on, every cloud start adds the CA again.
- WSL2 distributions without `update-ca-certificates`, and the
  `docker-desktop` distributions, are skipped. The change in WSL2 runs as
  the user logged on at the console; with nobody logged on it waits until
  someone logs on.
- When the Windows store cannot be changed, the command fails and the
  setting stays as it was.
- `--output json` prints `{"on": true}` or `{"on": false}`.

```
> hlab ca trust on
the HLab CA is trusted on this host
> hlab ca trust off
the HLab CA is no longer trusted on this host
```

## Other

### hlab logs

Shows the last 1000 lines of the hlabd log, oldest first, as
`time LEVEL message key=value...`; `--output json` prints the lines as
they are in the file. While the cloud runs the lines come through the
cloud, while it is stopped from the log files, so it also works when the
cloud is stopped. `--follow` keeps printing new lines until Ctrl+C.

```
> hlab logs
...
2026-11-19 19:22:01 INFO cloud stopping reason=requested
2026-11-19 19:22:01 INFO halting servers stop_behaviour=save host_shutdown=false
2026-11-19 19:22:11 INFO cloud stopped
> hlab logs --output json
...
{"time":"2026-11-19T19:18:57.1154384+01:00","level":"INFO","msg":"api request","method":"GET","path":"/v1/servers","status":200,"duration":512400}
> hlab logs --follow
```

To search the whole log, the rotated files included, or to read lines
older than the last 1000, use Administration > Logs in the
[tray program](../README.md#the-tray-program). The log files are
described in the [configuration reference](configuration.md#log-files).

### hlab version

```
> hlab version
HLab dev
```

## MCP server

### hlab mcp

`hlab mcp` is a [Model Context Protocol](https://modelcontextprotocol.io)
server for AI agents such as Claude Code and Claude Desktop. You do not
run it yourself: the MCP client starts it and talks to it over standard
input and output; it ends when the client closes its input. It takes no
flags; `hlab mcp --output json` fails with exit code 2.

The tools start and stop the cloud and manage projects, tokens, the log
and the HLab CA. Servers, networks and the other Hetzner resources have no
tools; an agent manages them with hcloud, OpenTofu or Ansible and a token
from `token_create`.

#### Configuration

Claude Code, for all your projects:

```
> claude mcp add --transport stdio --scope user hlab -- "C:\Program Files\hlab\hlab.exe" mcp
```

Claude Desktop: add the server to
`%APPDATA%\Claude\claude_desktop_config.json` and restart Claude Desktop:

```json
{
  "mcpServers": {
    "hlab": {
      "command": "C:\\Program Files\\hlab\\hlab.exe",
      "args": ["mcp"]
    }
  }
}
```

In a working copy, use the path of `bin\hlab.exe` instead; it controls the
dev service.

#### Tools

| Tool | Arguments | Result |
|---|---|---|
| `cloud_status` | none | like `hlab cloud status` |
| `cloud_start` | none | starts the cloud, waits until it runs, then the cloud status |
| `cloud_stop` | none | stops the cloud, waits until it is stopped, then the cloud status |
| `project_create` | `display_name` | like `hlab project create` |
| `project_list` | none | the projects with `resources_by_type`, like `hlab project list` |
| `project_delete` | `name` | deletes the project and waits until it is gone, like `hlab project delete` |
| `token_create` | `project`, `name`, `read_only` (optional, default false) | the token with its value, and a note that the value cannot be retrieved again |
| `token_list` | `project` | the tokens without their values, like `hlab token list` |
| `token_revoke` | `project`, `name` | like `hlab token revoke` |
| `logs` | `lines` (optional, 1 to 1000, default 200) | the last lines of the hlabd log, one JSON line each |
| `ca_export` | none | the certificate of the HLab CA as PEM text; no file is written |
| `ca_trust` | `on` (true or false) | like `hlab ca trust on` or `off` |
| `version` | none | like `hlab version` |

A result is text: what the `hlab` command prints with `--output json`.
`cloud_start` returns, for example:

```
{
  "problems": [],
  "shadowing_zones": [],
  "state": "running",
  "version": "dev"
}
```

`logs` with `"lines": 2`:

```
{"time":"2026-11-22T07:36:24.6139668+01:00","level":"INFO","msg":"halting servers","stop_behaviour":"save","host_shutdown":false}
{"time":"2026-11-22T07:36:37.6531115+01:00","level":"INFO","msg":"cloud stopped"}
```

When the command fails, the tool returns a tool error with the message
`hlab` prints, without the `hlab:` prefix. An argument outside its range,
such as `"lines": 2000`, is a tool error as well.

No tool discards the state or updates the server type catalog; use
`hlab cloud start --discard-state` and `hlab catalog update` for that.

#### While the cloud is stopped

`cloud_status`, `cloud_start`, `cloud_stop`, `logs` (read from the log
files) and `version` work. Every other tool returns the tool error
`the cloud is not running`, and `hlab mcp` keeps running, so the agent can
call `cloud_start`.

#### Waiting

- `cloud_start` returns once the cloud runs, at once if it already runs. A
  stop in progress is waited out first, a start in progress is waited for.
  A refused start is the tool error `the cloud refused to start: <problem>`,
  a failed one `the cloud failed to start: <error>`. After 5 minutes the
  tool gives up with `the cloud is still not running after 5m0s`.
- `cloud_stop` works the same way the other way round and gives up with
  `the cloud is still not stopped after 5m0s`.
- `project_delete` waits for the deletion, at most 30 minutes. If the
  project is already being deleted, it waits for that deletion. A failure
  is the tool error `deleting project <name> failed: <reason>`.

When the client asks for progress, these three tools report the current
state about every 10 seconds, for example `the cloud is starting`.

A client that stops waiting ends only the wait: the start, stop or
deletion goes on in HLab, and calling the tool again waits for it. Some
clients cancel a call after a short time (Claude Desktop probably after
about 60 seconds); then `cloud_status` and `project_list` show how far the
operation got.
