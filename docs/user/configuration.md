# Configuration reference

## settings.toml

hlab reads `settings.toml` from its data folder when the cloud starts:
`%ProgramData%\hlab-dev\settings.toml` for the development service
(`%ProgramData%\hlab\settings.toml` for the release profile). Setting up the
service creates an empty file that interactive users may edit. Every key is
optional; a missing key keeps its default. Changes apply at the next
`hlab cloud start`.

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
| `[cloud]` `stop_behaviour` | `"save"` | `"save"` or `"shutdown"`: the automatic stop action Hyper-V applies to the appliance and the server VMs when Windows shuts down. `hlab cloud stop` does not use it yet. |
| `[network]` `public_range` | `"198.19.0.0/16"` | IPv4 range of the public addresses. The first four addresses connect host and appliance; servers get addresses from the fifth on. The start is refused when the range collides with an address, route or NAT of the host. |
| `[storage]` `data_folder` | `<data folder>\data` | Folder of the VM disks, seed ISOs, downloaded images and ISOs, snapshots, backups, volumes and the appliance; see [Data folder](#data-folder). |
| `[resources]` `reserve_ram_mb` | `4096` | RAM in MB kept free for Windows. Creating, starting or changing the type of a server answers `resource_unavailable` when the server's RAM exceeds the host's free RAM minus this reserve. |
| `[resources]` `reserve_disk_gb` | `50` | Disk space in GB kept free in the data folder. Creating, rebuilding or changing the type of a server, creating an image and creating or resizing a volume answer `resource_unavailable` when the disks of all servers and volumes, grown to their full size, would leave less than this free. |
| `[certificates]` `trust_ca` | `false` | Accepted; has no effect in this version. |
| `[logging]` `level` | `"info"` | `"debug"`, `"info"`, `"warn"` or `"error"`: the level of `hlabd.log`. |

## Custom server types

Your own server types go in `server-types.custom.json` in the data folder
(`%ProgramData%\hlab-dev\server-types.custom.json` for the development
service). The file is a JSON array of server type objects in the format of
Hetzner's API. hlab reads it when the cloud starts and serves the types
after those of the catalog, in `/server_types` and `/pricing`, and
`POST /servers` accepts them like any other type:

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
- `architecture` is `x86`; hlab refuses to create servers of `arm` types.
- Without `prices`, `/pricing` shows zero prices for the type in each of
  its locations.

A file that cannot be read, or whose types break these rules, is ignored
as a whole; `hlab cloud status` then names the reason on a `problem:`
line and only the catalog types are served. The same happens when
`hlab catalog update` brings a type with the name or ID of one of yours.
Changes to the file apply at the next `hlab cloud start`.

## Catalogs

The catalogs hold what the API serves as Hetzner's fixed offer. The
development service reads them from `catalog\` of the working copy.

| File | Content | Made by |
|---|---|---|
| `locations.json` | Locations, as Hetzner's API returns them | `task catalog` |
| `server-types.json` | Server types, as Hetzner's API returns them | `task catalog` |
| `load-balancer-types.json` | Load balancer types (not served yet) | `task catalog` |
| `pricing.json` | Prices, served at `/pricing` | `task catalog` |
| `images.json` | The system images ubuntu-22.04, ubuntu-24.04, ubuntu-26.04, debian-12, debian-13, each with its download source in `_hlab.source` | by hand |
| `isos.json` | The ISOs served at `/isos`, see [ISOs](#isos) | by hand |
| `appliance-debs.json` | Debian packages the appliance needs for formatting volumes, see [Appliance packages](#appliance-packages) | by hand |

`hlab catalog update` writes the current server types from Hetzner to
`catalog\server-types.json` in the data folder
(`%ProgramData%\hlab-dev\catalog\server-types.json`). While that file
exists, hlab serves its server types instead of those of the shipped
`server-types.json`; delete it to go back to the shipped ones at the next
start.

hlab downloads an image from the source in `images.json` the first time a
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
- `fallback_url` and `fallback_checksum_url` are optional; hlab uses them
  when the first pair answers 404, as Debian's `current` directory does
  after the next point release.
- An ISO with a `deprecation` is listed but cannot be attached.
- `_hlab` is not served.

The first `attach_iso` of an ISO downloads it inside the action, checks it
against the checksum file and keeps it unchanged in
`<data_folder>\isos`; later attaches use that copy.

### Appliance packages

`appliance-debs.json` names Debian packages the appliance VM lacks:
`xfsprogs` and its libraries `libicu76` and `libinih1`, needed to format
volumes with `xfs`. Each entry has a `name`, a list of `urls` tried in
order, and the `sha256` of the file:

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
them while `mkfs.xfs` is missing. Without them the cloud still starts;
creating a volume with `format` `xfs` then fails.

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
