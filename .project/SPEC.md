# SPEC: hlab

Goals and non-goals are in IDEA.md, the requirements in REQUIREMENTS.md.

## Stack

Versions as of 2026-11-18 (reasoning and sources in DECISIONS.md, "Stack").

- Go 1.27.4 (`go.mod`: `go 1.27`, toolchain pinned) for hlabd, hlab,
  hlab-tray (Windows/amd64) and hlab-agent (Linux/amd64).
- Module or package name: Go module path `github.com/webcodingcat/hlab`;
  repository `https://github.com/webcodingcat/hlab`.
- Go modules: `golang.org/x/sys` v0.50.0 (service, SCM, ACLs, virtdisk),
  `github.com/Microsoft/go-winio` v0.6.4 (named pipe, `vhd.CreateVirtualDisk`), `modernc.org/sqlite`
  v1.62.0 (state, WAL, one writer), `github.com/BurntSushi/toml` v1.6.0 (settings),
  `github.com/lima-vm/go-qcow2reader` v0.8.0 (image conversion),
  `github.com/diskfs/go-diskfs` v1.9.4 (cidata ISO, sector size 2048, Rock Ridge), `github.com/miekg/dns`
  v1.1.73 (DNS server in hlab-agent), `github.com/google/nftables` v0.3.0
  (hlab-agent), `github.com/vishvananda/netlink` v1.3.1 (hlab-agent),
  `github.com/pb33f/libopenapi-validator` v0.16.3 (response validation in
  tests),
  `github.com/modelcontextprotocol/go-sdk` v1.8.0 (`hlab mcp`, stdio and
  in-memory transport), `github.com/coder/websocket` v1.8.15 (E22 ran with v1.8.14; server
  console WebSocket in hlabd and the tray's console proxy, phase 23;
  `research/server-console.md`); standard library `net/http`, `log/slog`, `crypto/x509`.
- Hyper-V: Hyper-V PowerShell module through one persistent Windows
  PowerShell 5.1 process; `CreateVirtualDisk` from virtdisk.dll for volume
  disks.
- Tray: Wails v3.1.0 (pinned exactly), Svelte 5.58.0, Vite 8.3.8, TypeScript
  5.9.3 (the 7.x line is not yet confirmed with `svelte-check`),
  `@lucide/svelte` 1.75.0, `@novnc/novnc` 1.7.0 (console window, phase 23;
  builds with the default target, ships no types, so the frontend declares
  the module itself; E22), Node.js 24.22.3 LTS for the frontend build (newer Node versions work for development).
- Windows icon resources: `github.com/tc-hib/winres` v0.3.1 (0BSD) in the
  icon generator `tools/icons`, which writes the committed `.syso` files.
- Storage VM: Debian 13 `generic` cloud image, cloud-init, btrfs-progs
  (data disk, quotas, snapshots), OpenSSH (ports 22 and 23), rsync,
  borgbackup, Samba, Apache 2.4 with mod_dav and libapache2-mpm-itk (WebDAV
  over HTTPS, files owned by the box, E17), vsftpd (FTP and explicit FTPS);
  Debian 13 packages pinned by version and SHA-256 in
  `catalog/storage-debs.json`, downloaded once on the host and put on the
  storage VM's cidata like the appliance's packages (no apt from the VM);
  hlab-agent in role `storage`.
- Appliance: Debian 13 `generic` cloud image, cloud-init, nftables,
  HAProxy 3.0 (Debian package), iproute2 (VRF, VLAN, netns), xfsprogs,
  e2fsprogs; hlab-agent as one static Go binary.
- Guest images: Ubuntu cloud images (`*-server-cloudimg-amd64.img`,
  qcow2) for ubuntu-22.04, 24.04, 26.04; Debian `generic` raw images for
  debian-12, 13; downloaded from cloud-images.ubuntu.com and
  cloud.debian.org with their SHA256/SHA512 sums.
- Installer: WiX Toolset v7.0.1 (MSI plus Burn bundle; the OSMF EULA is
  accepted in the build, no fee for a project without revenue), WebView2
  Evergreen bootstrapper (`MicrosoftEdgeWebview2Setup.exe`, always the current online bootstrapper; it has no version of its own).
- Build and tasks: Task v3.54.0 (`Taskfile.yml`), golangci-lint v2.15.0.
- Tests: Go `testing` (unit, e2e harness in `test/e2e`, build tag `e2e`),
  Playwright Test 1.65.1 (TypeScript) for the tray over CDP.
- Clients in the e2e harness (pinned): hcloud CLI v1.70.2, OpenTofu 1.13.9
  with provider hetznercloud/hcloud 1.70.2, ansible-core 2.22.0 on Python
  3.13 or newer in WSL2 (installed with `uv` into its own environment, independent of the distro's Python) with
  collection hetzner.hcloud 7.2.0 and hcloud-python 2.27.0.
- Hetzner OpenAPI description: `https://docs.hetzner.cloud/cloud.spec.json`
  (OpenAPI 3.1.2), pinned by SHA-256 in `api/openapi.lock`; see
  DECISIONS.md "OpenAPI copy not committed".

Fixed API answers:

- Subnet `type: vswitch`: `invalid_input`, field `type`, message
  `vswitch subnets are not supported by HLab`.
- `/datacenters`: as at Hetzner since 2026-10-01 (410 Gone, per the frozen
  description).

## Server console

- `POST /servers/{id}/actions/request_console` answers as the frozen
  description says (201, a finished `request_console` action, `wss_url`,
  `password`); `wss_url` is
  `wss://console.hetzner.internal/?server_id=<id>&token=<token>`, and the
  token is valid for one minute to connect, an open connection is not cut
  when it expires. The password is the VNC password (RFB security type
  VNC Authentication).
- The console speaks RFB 3.8 over WebSocket with TLS; the certificate for
  `console.hetzner.internal` comes from the hlab CA. hlab never switches
  "Trust CA" on by itself: an own browser or VNC client accepts the URL
  only while trust is on (docs/deviations.md says so); the tray's console
  window works either way.
- Graphical or serial: phase 23 starts with an experiment on the real host
  that measures, for the graphical path (Hyper-V framebuffer and input
  through WMI), the frame rate at 1024x768, the delay until a key press is
  visible and hlabd's CPU load per open console. If it reaches about 5
  frames per second, about 0.5 s and at most one CPU core, the console is
  graphical; otherwise it shows the server's serial console (COM1, ttyS0)
  rendered as text into the RFB framebuffer, with keyboard input sent to
  the serial port. The values and the choice go to
  `research/EXPERIMENTS.md` (`decisions/server-console.md`).
  E21 already measured 7.3 to 7.7 frames per second uncapped and a steady
  5 capped at 1024x768, with hlabd at about 0.03 cores; the graphical path
  is chosen unless the key delay check of E21, still to run at the start
  of phase 23, fails. One open console costs about 0.7 cores, almost all
  in the Hyper-V service `vmms.exe`; the limit of one core counts hlabd,
  as the user set it, and the documentation names the `vmms.exe` load.
- The certificate for `console.hetzner.internal` is issued by the hlab CA
  at every cloud start, like the CRL renewal. The end-to-end test of CON-01
  trusts the certificate by loading `/ca.pem` into its own client and never
  switches "Trust CA" on.
- `hcloud server request-console` against hlab prints the URL and password
  like at Hetzner; the end-to-end test of CON-01 takes its URL from it.

## References

External descriptions the project must match, such as an API it
re-implements, a file format or a protocol. Agents read the part a phase
touches here instead of relying on memory.

- Hetzner Cloud OpenAPI description: `%LocalAppData%\hlab\openapi\cloud.spec.json`
  (downloaded by `task openapi` from `https://docs.hetzner.cloud/cloud.spec.json`),
  OpenAPI 3.1.2 pinned by SHA-256 in `api/openapi.lock`; settles the
  request and response shapes, error codes and HTTP statuses hlab serves.
- Hetzner API OpenAPI description (Storage Boxes):
  `%LocalAppData%\hlab\openapi\hetzner.spec.json` (downloaded by
  `task openapi` from `https://docs.hetzner.cloud/hetzner.spec.json`, a
  copy from after 2026-04-21), OpenAPI 3.1.2 pinned by SHA-256 in
  `api/hetzner-openapi.lock`; settles the same for `/hetzner/v1`.
- RFB protocol 3.8 (RFC 6143) for the server console, as noVNC 1.7.0
  speaks it (`research/server-console.md`).

## Accounts and secrets

What the user has to provide before the implementation runs. Values never
appear here, only names.

- No account is needed; API tokens are generated locally. The project has
  no environment variables with secrets and no `.env.example`.
- Optional, development and release only: an hcloud context for the real
  API (`https://api.hetzner.cloud/v1`, a read-only token is enough), named
  `HLab` by default; another name is set in the non-secret variable
  `HLAB_HCLOUD_CONTEXT`. It is always selected by name
  (`hcloud --context <name>`), never the active context, which may point
  at hlab during tests. The project never reads hcloud's configuration file
  itself, and hcloud is never required for it:
  - `task catalog` generates the shipped server type, load balancer type,
    location and pricing catalogs from the real API (raw API responses, all
    pages). With a usable context it calls
    `hcloud --context <name> api <path>`; without one it says why and asks
    for a token with a hidden prompt, uses it for this run only and calls
    the API itself. The storage box types (with prices) come from the
    Hetzner API, always `https://api.hetzner.com/v1/storage_box_types`, all
    pages: with a usable context through hcloud itself,
    `hcloud --context <name> --endpoint https://api.hetzner.com/v1 api /storage_box_types -v page=<n> -v per_page=50`,
    so the token never leaves hcloud; without one with the token of the same
    hidden prompt; the context's `hetzner-endpoint` is not read.
  - The live check of `hlab catalog update` (CAT-03) runs unattended, so it
    never prompts: with a usable context its test harness takes the token
    through hcloud
    (`hcloud --context <name> config get token --allow-sensitive`) and
    hands it to `hlab catalog update --hetzner-token-env` in the child
    process only; without one it is skipped with the reason in the test
    output, and CAT-03 is still tested against a recorded response.

  A context is usable only if hcloud is installed, `hcloud context list`
  names it and `hcloud --context <name> config get endpoint` returns
  `https://api.hetzner.cloud/v1` (a missing context only gives a warning
  and the default endpoint, so the list is checked first). Calls to hcloud
  clear `HCLOUD_TOKEN`, `HCLOUD_ENDPOINT` and `HCLOUD_CONTEXT` in the child
  environment.
- Development only, for agents: an hcloud context `hlab-docs` for the dev
  cloud, created once by the user: endpoint `http://api.hetzner.internal/v1`
  and Hetzner endpoint `http://api.hetzner.internal/hetzner/v1` set in the context
  (`hcloud --context hlab-docs config set endpoint ...` and
  `... config set hetzner-endpoint ...`, not `--global`), token of a read/write token of the hlab project `docs`.
  The `doc-writer` agent uses it to run the examples of the user
  documentation against the running dev service:
  - hcloud examples run as `hcloud --context hlab-docs <command>`, with
    `HCLOUD_TOKEN`, `HCLOUD_ENDPOINT` and `HCLOUD_CONTEXT` cleared in the
    child environment; the examples in the documents keep their
    `HCLOUD_ENDPOINT`/`HCLOUD_TOKEN` form.
  - OpenTofu and Ansible examples get the token only inside the one command
    that needs it, as
    `HCLOUD_TOKEN="$(hcloud --context hlab-docs config get token --allow-sensitive)" <command>`
    (PowerShell: set `$env:HCLOUD_TOKEN` from the same call in the same
    command and remove it afterwards); the value is never printed, written
    to a file or read by the agent.
  - Agents work only in project `docs`, never create, delete or list
    projects or tokens there, never run an example whose output contains a
    token value, and delete the resources they created before they finish.
  - The context is usable if `hcloud context list` names it and
    `hcloud --context hlab-docs config get endpoint` returns
    `http://api.hetzner.internal/v1` (and, for Storage Box examples,
    `config get hetzner-endpoint` returns
    `http://api.hetzner.internal/hetzner/v1`); otherwise the examples are
    not run and the documentation plan says why. The active context is never changed.
- Operation: the product has no dependency on hcloud. `hlab catalog
  update` and the tray ask the user for a real Hetzner token (ARCHITECTURE.md,
  "hlab"); hlab uses it for that one update and never stores it.

## Machine prerequisites

- Windows 11 Pro (or Enterprise/Education, or Windows Server 2022/2025 with
  Desktop Experience), x64, Hyper-V and its PowerShell module enabled,
  hardware virtualization on; the developer is a member of Hyper-V Administrators; for the end-to-end tests at least 16 GB RAM
  and 100 GB free disk.
- WSL2 with one Ubuntu distribution (24.04 or newer) in NAT mode with `dnsTunneling`
  on (the defaults), for Ansible and the WSL2 tests, with `rsync`,
  `borgbackup`, `smbclient` and `lftp` for the storage box tests.
- Go, Node.js, Task, WiX (as dotnet tool), hcloud, OpenTofu on the host;
  `uv`, ansible-core and the collection in WSL2 — versions as in "Stack".
- Internet access for image, ISO, package and OpenAPI downloads.
- Elevated rights (administrator):
  - Operation: hlabd runs as `LocalSystem`; it creates virtual switches,
    host routes, forwarding, NAT and NRPT rules, which Windows allows only
    with administrator rights. This cannot be avoided. Users never get a
    prompt: the installer (elevated once) gives Interactive Users the right
    to start and stop the service and to use the management pipe.
  - Development: once per machine, `/wcc:prepare` asks the user to run
    `scripts/dev-setup.ps1` in an elevated terminal. It runs `hlabd install
    --dev`, which registers the service `hlab-dev` (profile `dev`, see
    ARCHITECTURE.md "Profiles") with `bin\hlabd.exe` of the working copy and
    the same security descriptor and ACLs as the installer. After that, phases build, start and stop the dev service and
    run all tests **without** elevated rights; Claude Code is not started
    elevated. Until phase 01 has built `hlabd install`, the script does the
    same with `sc.exe` and `icacls`.
  - The installer suite (`task e2e:elevated`, INST-01, 04, 05, 06) installs
    and uninstalls MSI packages and inspects Hyper-V and the host; it needs
    an elevated terminal. It runs in phase 16, in the development test VM
    (see "Test VMs for the release checks").
  - A dev service that runs a binary from a user-writable folder as SYSTEM
    is acceptable only on a development machine; the developer guide says
    so.
- The dev service (profile `dev`) and an installed hlab (profile `release`)
  may be installed on the same machine; their names, folders and host
  objects differ (ARCHITECTURE.md "Profiles"). Only one of their clouds runs
  at a time: each refuses to start while the other runs, and the end-to-end
  harness refuses to run while the release cloud runs.
- Test VMs for the release checks (phase 16), set up by hand by the user
  as `docs/development/release-checks.md` describes; Claude creates
  neither. Both are Hyper-V VMs on the development machine with a fresh
  Windows 11 Pro and nested virtualization, so HLab can run its own VMs
  inside.
  - Development test VM: the development tools of this section and a
    checkout of the repository; runs only the elevated installer suite.
  - User test VM: no development tools; the user installs HLab with the
    setup EXE built on the development machine and goes through the
    manual checks as a user would (installation, console, shutdown,
    uninstallation).

## Documentation

The documents the project delivers, from "Documentation" in IDEA.md. Every
phase brings them up to date for what it delivers, in its documentation
plan, carried out by the `doc-writer` agent. They describe what is built,
never the target vision. Their requirements are in category `DOC` of
REQUIREMENTS.md. The `doc-writer` runs the examples it writes against the
dev cloud through the hcloud context `hlab-docs` (see "Accounts and
secrets").

- D1 User guide: `README.md`, for developers testing Hetzner setups,
  compact: a banner with the application icon, "HLab" and a subtitle,
  badges and a link line; what HLab is; screenshots; prerequisites;
  installation; first steps (project, token, client cloud and Hetzner endpoint settings for
  hcloud, OpenTofu and Ansible); tray program; WSL2 prerequisites;
  uninstallation; a short note on the MCP server `hlab mcp`; links to the
  other documents. The detailed usage per
  resource lives in `docs/guide.md`. Images live in `docs/images/`;
  screenshots of the console are generated from the running tray
  program, in light and dark mode.
- D2 Command reference: `docs/commands.md`, every `hlab` command and
  flag with an example, and a section `hlab mcp` with every tool and a
  configuration example for Claude Code and Claude Desktop.
- D3 Configuration reference: `docs/configuration.md`, every key of
  `settings.toml`, the custom server types file, the custom storage box types file and the catalog files.
- D4 Known deviations from Hetzner: `docs/deviations.md`, for users: short
  prose grouped by topic (servers, images and backups, volumes, networks
  and IPs, firewalls, DNS, load balancers and certificates, storage boxes,
  projects and server types, WSL2), saying what a user notices and what is not
  available, without routes or endpoint lists; about 250 lines at most.
- D5 Help inside the program: one help text per tray view in
  `ui/frontend/src/help/<view>.md`, shown by the help button.
- D6 Developer guide: `docs/development/`, for outside contributors: `setup.md`
  (tools, `dev-setup.ps1`), `build-and-test.md` (Taskfile targets, unit,
  e2e, elevated suite), `components.md` (overview linking to the code),
  `adding-a-resource.md` (a new resource module step by step),
  `release-checks.md` (setting up the two test VMs and what runs in
  which, from phase 16),
  `api-coverage.md` (every endpoint hlab serves, every endpoint or
  behaviour that differs from Hetzner with its route, and every
  `unsupported_error`).
