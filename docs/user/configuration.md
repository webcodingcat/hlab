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
| `[storage]` `data_folder` | `<data folder>\data` | Folder of the VM disks, seed ISOs, downloaded images and the appliance. |
| `[resources]` `reserve_ram_mb` | `4096` | Accepted and checked (not negative); has no effect in this version. |
| `[resources]` `reserve_disk_gb` | `50` | Accepted and checked (not negative); has no effect in this version. |
| `[certificates]` `trust_ca` | `false` | Accepted; has no effect in this version. |
| `[logging]` `level` | `"info"` | `"debug"`, `"info"`, `"warn"` or `"error"`: the level of `hlabd.log`. |

## Custom server types

`server-types.custom.json` in the data folder is reserved for your own
server types. This version does not read it yet; only the server types of
the shipped catalog are offered.

## Catalogs

The catalogs hold what the API serves as Hetzner's fixed offer. The
development service reads them from `catalog\` of the working copy.

| File | Content | Made by |
|---|---|---|
| `locations.json` | Locations, as Hetzner's API returns them | `task catalog` |
| `server-types.json` | Server types, as Hetzner's API returns them | `task catalog` |
| `load-balancer-types.json` | Load balancer types (not served yet) | `task catalog` |
| `pricing.json` | Prices (not served yet) | `task catalog` |
| `images.json` | The system images ubuntu-22.04, ubuntu-24.04, ubuntu-26.04, debian-12, debian-13, each with its download source in `_hlab.source` | by hand |

hlab downloads an image from the source in `images.json` the first time a
server uses it, checks it against the checksum file of the source, converts
it once and keeps it in `<data_folder>\images`.
