# Command reference

`hlab` manages the local cloud: start and stop, projects, API tokens and
the server type catalog.
In a working copy it is `bin\hlab.exe`; the examples write `hlab`.

`hlab cloud start`, `stop` and `status` work at any time. All other
commands except `hlab version` talk to the running cloud and fail with
`hlab: the cloud is not running` while it is stopped.

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

## Other

### hlab version

```
> hlab version
hlab dev
```
