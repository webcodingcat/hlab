# Command reference

`hlab` manages the local cloud: start and stop, projects, API tokens,
the server type catalog and the hlab CA.
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
API. The command returns at once; follow the start with `hlab cloud status`.
No administrator rights are needed.

```
> hlab cloud start
the cloud is starting
```

`--discard-state` discards a state file that this hlab version cannot
read (written by a newer version, or damaged): the file is renamed, the
server VMs and their disks and volumes are deleted, and the cloud starts
empty. A start refused for such a state names this flag in
`hlab cloud status`. A readable state is never discarded.

### hlab cloud stop

Stops the API and the appliance and removes the host networking. Server
VMs are not saved or shut down yet; they stay as they are in Hyper-V.

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
a zone you create in hlab through hlab, so a zone such as `example.com`
hides the real domain. hlab asks the public DNS for each primary zone when
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
for example) have finished, then deletes its servers with their VMs and
disks (protected servers too), SSH keys and placement groups. The command
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
without that flag the command exits with `2`. hlab uses the token for this
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

The hlab CA signs the managed certificates of load balancers; see
[Load balancers and certificates](../../README.md#load-balancers-and-certificates).

### hlab ca export

`hlab ca export <file>` writes the certificate of the hlab CA (PEM) to the
file, for clients that do not use the host's trust store, such as
`curl --cacert` or a server that gets it through cloud-init. The file
holds no private key.

```
> hlab ca export hlab-ca.pem
hlab CA written to hlab-ca.pem
```

### hlab ca trust

`hlab ca trust on` adds the hlab CA to the trust store of Windows
(Local Computer, Trusted Root Certification Authorities) and of every
WSL2 distribution, as `/usr/local/share/ca-certificates/hlab-ca.crt`;
`hlab ca trust off` removes it from both. Read the
[risk](../../README.md#trusting-the-hlab-ca) first.

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
the hlab CA is trusted on this host
> hlab ca trust off
the hlab CA is no longer trusted on this host
```

## Other

### hlab logs

Shows the last 1000 lines of the hlabd log, oldest first, as
`time LEVEL message key=value...`; `--output json` prints the lines as
they are in the file. While the cloud runs the lines come through the
cloud, while it is stopped from the log files. `--follow` keeps printing
new lines until Ctrl+C.

```
> hlab logs
2026-11-19 11:00:00 INFO cloud started profile=hlab-dev
```

### hlab version

```
> hlab version
hlab dev
```
