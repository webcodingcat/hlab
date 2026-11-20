# Configuration reference

## settings.toml

HLab reads `settings.toml` from its data folder when the cloud starts:
`%ProgramData%\hlab-dev\settings.toml` for the development service
(`%ProgramData%\hlab\settings.toml` for the release profile). Setting up the
service creates an empty file that interactive users may edit. Every key is
optional; a missing key keeps its default. Changes apply at the next
`hlab cloud start`; `stop_behaviour` is also read again at
`hlab cloud stop`.

Administration > Settings in the [tray program](../README.md#the-tray-program)
edits the file too. It saves only when every value is valid, and while the
cloud runs, a save applies `level` at once. `hlab ca trust` and the
tray rewrite the file with all current settings, and comments in it are
lost.

```toml
[cloud]
stop_behaviour = "save"

[network]
public_range = "198.19.0.0/16"

[storage]
data_folder = 'C:\ProgramData\hlab-dev\data'

[resources]
reserve_ram_mb = 4096
reserve_disk_gb = 50

[certificates]
trust_ca = false

[logging]
level = "info"
```

An unknown key or an invalid value stops the start; the message is in the
log `%ProgramData%\hlab-dev\logs\hlabd.log`.

| Key | Default | Meaning |
|---|---|---|
| `[cloud]` `stop_behaviour` | `"save"` | `"save"` or `"shutdown"`: what happens to running servers when the cloud stops or Windows shuts down; see [Stop behaviour](#stop-behaviour). |
| `[network]` `public_range` | `"198.19.0.0/16"` | IPv4 range of the public addresses; see [Public range](#public-range). |
| `[storage]` `data_folder` | `<data folder>\data` | Folder of the VM disks, seed ISOs, downloaded images and ISOs, snapshots, backups, volumes and the appliance; see [Data folder](#data-folder). |
| `[resources]` `reserve_ram_mb` | `4096` | RAM in MB kept free for Windows. Creating, starting or changing the type of a server answers `resource_unavailable` when the server's RAM exceeds the host's free RAM minus this reserve. |
| `[resources]` `reserve_disk_gb` | `50` | Disk space in GB kept free in the data folder. Creating, rebuilding or changing the type of a server, creating an image and creating or resizing a volume answer `resource_unavailable` when the disks of all servers and volumes, grown to their full size, would leave less than this free. |
| `[certificates]` `trust_ca` | `false` | Whether the HLab CA, which signs managed certificates, is in the trust store of Windows and of the WSL2 distributions. Set it with [`hlab ca trust`](commands.md#hlab-ca-trust); while it is `true`, every cloud start adds the CA again. Read the [risk](guide.md#trusting-the-hlab-ca) before you switch it on. |
| `[logging]` `level` | `"info"` | `"debug"`, `"info"`, `"warn"` or `"error"`: the level of `hlabd.log`; see [Log files](#log-files). |

### Stop behaviour

`hlab cloud stop` reads `stop_behaviour` again, so a change applies to
the next stop without a restart; if the file is invalid at that moment,
the value read at the last start is used.

- `"save"`: the running servers are saved one after another, like
  hibernation, and the next `hlab cloud start` resumes them where they
  left off. A server that cannot be saved is shut down instead.
- `"shutdown"`: every running server is asked to shut down at once;
  servers still running after 60 seconds are turned off. The next
  `hlab cloud start` powers on the servers that were running.

Servers that were off stay off.

When Windows shuts down while the cloud runs, Hyper-V itself saves or
shuts down the appliance and the servers, with the stop behaviour of the
last `hlab cloud start`. Hyper-V does not accept a new value for running
VMs, so a changed `stop_behaviour` applies to a Windows shutdown only
after the next `hlab cloud start`. After the reboot the cloud is stopped;
`hlab cloud start` resumes or powers on the servers that ran.

### Public range

`public_range` must be an IPv4 range from `/8` to `/28`; host bits are
ignored (`198.19.5.0/16` means `198.19.0.0/16`). It may not overlap
`0.0.0.0/8`, `10.0.0.0/8`, `100.64.0.0/10`, `127.0.0.0/8`,
`169.254.0.0/16`, `172.16.0.0/12`, `192.168.0.0/16`, `224.0.0.0/3`, or
Hetzner's resolvers `185.12.64.1` and `185.12.64.2`; such a value stops
the start like any invalid setting. The private ranges stay free for
your private networks.

The first four addresses connect host and appliance: the appliance has
the first (`198.19.0.1` with the default range) and the host the second,
where the API listens on port 8780 behind the appliance's proxy on port
80. Servers, primary IPs, floating IPs and load balancers get addresses
from the fifth on.

The start is refused, and `hlab cloud status` shows `state: refused` with
a `problem:` line, when

- the range collides with an address, route or NAT of the host
  (`range_collision`, naming the interface or route), or
- the range was changed while the state still holds addresses outside
  the new one (`public_range_changed`). Set the range back, or start with
  [`hlab cloud start --discard-state`](commands.md#hlab-cloud-start) to
  delete all servers and resources and begin with an empty state.

## Custom server types

Your own server types go in `server-types.custom.json` beside
settings.toml (`%ProgramData%\hlab-dev\server-types.custom.json` for the
development service). The cloud start creates it with an empty list `[]`
when it is missing, with the permissions of settings.toml, so interactive
users may edit it. Administration > Server Types in the
[tray program](../README.md#the-tray-program) adds and removes types in
it; you can also edit it by hand.

The file is a JSON array of server type objects in the format of
Hetzner's API. HLab serves the types after those of the catalog, in
`/server_types` and `/pricing`, and `POST /servers` accepts them like any
other type:

```json
[
  {
    "id": 100001,
    "name": "big1",
    "description": "Big test server",
    "cores": 8,
    "memory": 32,
    "disk": 160,
    "architecture": "x86",
    "cpu_type": "dedicated",
    "category": "regular_purpose",
    "storage_type": "local",
    "deprecation": null,
    "locations": [
      {"id": 1, "name": "fsn1", "available": true, "recommended": false, "deprecation": null}
    ]
  }
]
```

- `id` is 100000 or more; `id` and `name` are unique and taken by no
  catalog type.
- `cores`, `memory` (GB) and `disk` (GB) are the size of the VM.
- `locations` names the locations the type is offered in.
- `architecture` is `x86`; HLab refuses to create servers of `arm` types.
- Without `prices`, `/pricing` shows zero prices for the type in each of
  its locations.

A file that cannot be read, or whose types break these rules, is ignored
as a whole; `hlab cloud status` then names the reason on a `problem:`
line and only the catalog types are served. The same happens when
`hlab catalog update` brings a type with the name or ID of one of yours.

Changes by hand apply at the next `hlab cloud start`; changes made in the
tray apply at once while the cloud runs, otherwise at the next start. The
tray gives a new type the next free `id` from 100000, `architecture`
`x86`, `storage_type` `local` and zero prices in its locations; it
writes the locations ticked in its form, or every location of the catalog
when "All locations" is ticked.

Servers of a type you remove keep running, and power on and
rebuild still work for them; new servers and `change_type` cannot use the
type any more.

## Catalogs

The catalogs hold what the API serves as Hetzner's fixed offer. The
development service reads them from `catalog\` of the working copy.

| File | Content | Made by |
|---|---|---|
| `locations.json` | Locations, as Hetzner's API returns them | `task catalog` |
| `server-types.json` | Server types, as Hetzner's API returns them | `task catalog` |
| `load-balancer-types.json` | Load balancer types, served at `/load_balancer_types` | `task catalog` |
| `pricing.json` | Prices, served at `/pricing` | `task catalog` |
| `images.json` | The system images ubuntu-22.04, ubuntu-24.04, ubuntu-26.04, debian-12, debian-13, each with its download source in `_hlab.source` | by hand |
| `isos.json` | The ISOs served at `/isos`, see [ISOs](#isos) | by hand |
| `appliance-debs.json` | Debian packages the appliance needs for formatting volumes and for load balancers, see [Appliance packages](#appliance-packages) | by hand |

`hlab catalog update` writes the current server types from Hetzner to
`catalog\server-types.json` in the data folder
(`%ProgramData%\hlab-dev\catalog\server-types.json`). While that file
exists, HLab serves its server types instead of those of the shipped
`server-types.json`; delete it to go back to the shipped ones at the next
start.

HLab downloads an image from the source in `images.json` the first time a
server uses it, checks it against the checksum file of the source, converts
it once and keeps it in `<data_folder>\images`.

### ISOs

`isos.json` lists the ISOs that `GET /isos` serves and `attach_iso`
accepts: Ubuntu 22.04.5, 24.04.5 and 26.04.1 live server and Debian
12.15.0 and 13.7.0 netinst, all `x86`. Each entry is an ISO object of
Hetzner's API plus its download source:

```json
{
  "isos": [
    {
      "id": 130005,
      "name": "debian-13.7.0-amd64-netinst.iso",
      "description": "Debian 13.7.0 netinst (amd64)",
      "type": "public",
      "deprecation": null,
      "architecture": "x86",
      "_hlab": {
        "source": {
          "url": "https://cdimage.debian.org/debian-cd/current/amd64/iso-cd/debian-13.7.0-amd64-netinst.iso",
          "checksum_url": "https://cdimage.debian.org/debian-cd/current/amd64/iso-cd/SHA256SUMS",
          "fallback_url": "https://cdimage.debian.org/cdimage/archive/13.7.0/amd64/iso-cd/debian-13.7.0-amd64-netinst.iso",
          "fallback_checksum_url": "https://cdimage.debian.org/cdimage/archive/13.7.0/amd64/iso-cd/SHA256SUMS"
        }
      }
    }
  ]
}
```

- `url` and `checksum_url` are the ISO and the `SHA256SUMS` file that
  lists it.
- `fallback_url` and `fallback_checksum_url` are optional; HLab uses them
  when the first pair answers 404, as Debian's `current` directory does
  after the next point release.
- An ISO with a `deprecation` is listed but cannot be attached.
- `_hlab` is not served.

The first `attach_iso` of an ISO downloads it inside the action, checks it
against the checksum file and keeps it unchanged in
`<data_folder>\isos`; later attaches use that copy.

### Appliance packages

`appliance-debs.json` names Debian packages the appliance VM lacks:

- `xfsprogs` and its libraries `libicu76` and `libinih1`, needed to
  format volumes with `xfs`;
- `haproxy` with `liblua5.4-0`, `libopentracing-c-wrapper0t64`,
  `libopentracing1` and `lsb-base`, which runs the load balancers.

Each entry has a `name`, a list of `urls` tried in order, and the
`sha256` of the file:

```json
{
  "debs": [
    {
      "name": "xfsprogs",
      "urls": [
        "http://deb.debian.org/debian/pool/main/x/xfsprogs/xfsprogs_6.13.0-2+deb13u1_amd64.deb",
        "https://snapshot.debian.org/archive/debian/20260713T202617Z/pool/main/x/xfsprogs/xfsprogs_6.13.0-2+deb13u1_amd64.deb"
      ],
      "sha256": "79e00160bfc45d25a46d2709edd364afb41723d9b96713de8f6c80b6a17f2d74"
    }
  ]
}
```

At `hlab cloud start`, hlab downloads each package once into
`<data_folder>\cache\debs` and hands them to the appliance, which installs
the xfs packages while `mkfs.xfs` is missing and the HAProxy packages
while `haproxy` is missing. Without them the cloud still starts; creating
a volume with `format` `xfs` then fails, and load balancers accept no
traffic.

## Data folder

The folder of `[storage]` `data_folder` holds:

| Folder | Content |
|---|---|
| `vms` | The configuration files of the server VMs. |
| `disks` | Server disks and their seed ISOs, and the rescue disk of a server in rescue. |
| `images` | Downloaded system images as base disks, the rescue base `rescue-v2.vhdx`, and the snapshots and backups of all projects as `image-<id>.vhdx`. |
| `isos` | Downloaded ISOs of `isos.json`. |
| `volumes` | Volume disks as `volume-<id>.vhdx`. |
| `cache\debs` | Downloaded packages of `appliance-debs.json`. |
| `appliance` | The appliance VM with its disk and seed ISO. |

Deleting a snapshot, backup, volume or project deletes its files; the
downloaded images, ISOs and packages stay until you delete them.

The HLab CA is not in the data folder but in `ca` beside settings.toml
(`%ProgramData%\hlab-dev\ca`): `ca.pem` (the CA certificate), `ca.key`
(its private key) and `ca.crl` (the revocation
list). The first cloud start creates them; they stay the same until you
delete the folder.

## Log files

hlabd writes its log to `hlabd.log` in the `logs` folder beside
settings.toml (`%ProgramData%\hlab-dev\logs\hlabd.log`), one JSON object
per line. At 10 MB the file becomes `hlabd.log.1`, older files move on up
to `hlabd.log.5`, and the oldest is deleted, so the log never takes much
more than 60 MB. [`hlab logs`](commands.md#hlab-logs) shows and follows
these files; Administration > Logs in the tray program also searches all
of them and loads older lines from them.
