# Command reference

`hlab` manages the local cloud: start and stop, projects, API tokens,
the server type catalog and the HLab CA.
In a working copy it is `bin\hlab.exe`; the examples write `hlab`.

`hlab cloud start`, `stop` and `status` work at any time. All other
commands except `hlab logs` and `hlab version` talk to the running cloud
and fail with `hlab: the cloud is not running` while it is stopped.

## Output and exit codes

Every command prints text for people. `--output json` prints JSON instead,
for scripts:

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

```
> hlab project create demo
project demo created
```

### hlab project list

```
> hlab project list
NAME                 RESOURCES  CREATED
demo                 0          2026-11-18 12:56:55
```

### hlab project delete

Deletes a project with everything in it: it revokes the project's tokens
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
