# Command reference

`hlab` manages the local cloud: start and stop, projects and API tokens.
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

Shows the state (`running`, `stopped`, `starting`, `stopping`), the version
and any problem found at start.

```
> hlab cloud status
state: running
version: dev
> hlab cloud status --output json
{
  "problems": [],
  "state": "running",
  "version": "dev"
}
```

When the last start was refused, for example because the cloud of the
other profile runs, the status shows `state: refused` and the reason on a
`problem:` line.

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

Deletes a project and its tokens. A project that still has resources
(servers, SSH keys) cannot be deleted yet; delete them first.

```
> hlab project delete demo
hlab: project has resources; deleting resources comes in a later version
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

## Other

### hlab version

```
> hlab version
hlab dev
```
