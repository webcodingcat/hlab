# System images for milestone 1 (research, 2026-11-24)

Superseded where it differs by the experiments E30-E34 in EXPERIMENTS.md and
the real API values in `hetzner-system-images.json` (read 2026-11-24): rocky-8
and alma-8 are offered, all eleven images passed, fedora-43 is unavailable
after 2027-02-09.

Legend: [S] read at the cited source in this run; [K] background knowledge, not verified in this run; [?] unclear, needs a test or the real API.

## 1. Hetzner x86 system images today

Source: Hetzner Cloud API changelog, https://docs.hetzner.cloud/changelog (the only official list; there is no image list in the docs, and the repo's frozen `cloud.spec.json` has only the `os_flavor` enum `ubuntu, centos, debian, fedora, rocky, alma, opensuse, unknown` and one example value). The real API was not called.

| name | x86 ID [S] | state on 2026-11-24 | announced / change [S] |
|---|---|---|---|
| ubuntu-22.04, 24.04, 26.04, debian-12, debian-13 | already in catalog (ubuntu-26.04 ID 387894169 matches the changelog) | available | |
| fedora-44 | 387893188 | available | added 2026-05-18 |
| fedora-43 | 342035601 | available, **deprecated** | added 2025-12-18; deprecated 2026-11-12, unavailable for new servers after 2027-02-12 |
| opensuse-16 | 342262088 | available | added 2025-12-18 |
| centos-stream-10 | 210163277 | available | added 2025-01-08 |
| alma-10 | 245249013 | available | added 2025-06-17 |
| rocky-10 | 245249370 | available | added 2025-06-17 |
| debian-11 | not in changelog text read | available, **deprecated** | deprecated 2026-08-31, unavailable after 2026-11-30 (6 days from today); skip it |
| alma-9, rocky-9, centos-stream-9 | [?] | almost certainly available (alma-8/9 announced 2023-03; no removal entry for 2025/2026) but no source names them or their IDs | |
| alma-8, rocky-8 | [?] | same uncertainty, weaker | |

Gone, do not add: fedora-40/41/42, opensuse-15, centos-stream-8, ubuntu-20.04 and older (all "no longer available for new servers"). Deprecated images stay for at least 3 months after the announcement (https://docs.hetzner.com/cloud/servers/faq).

Attributes hlab must mirror:
- `os_flavor`: `fedora`, `opensuse`, `centos` (for CentOS Stream), `alma`, `rocky` (enum in the spec). `architecture: x86`, `type: system`, `status: available`, `rapid_deploy: true` (changelog calls every one of the new ones a "Rapid Deploy Image"; ubuntu-20.04 in the spec example is `false`, so it is a real per-image flag).
- `deprecation`: object `{announced, unavailable_after}` (date-time), `null` if not deprecated (spec). For fedora-43: `{"announced":"2026-11-12T00:00:00Z","unavailable_after":"2027-02-12T00:00:00Z"}` (time of day [?]). Once `unavailable_after` passes, the image is no longer listed and cannot create servers (spec text), so hlab should apply that rule at runtime. The old `deprecated` field was removed from images on 2026-11-02 (changelog), so do not emit it.
- `description`, `os_version`, `disk_size`, `image_size`, `created`: **not documented anywhere I could reach.** `image_size` is null for system images (as in the catalog). `disk_size` is "size of the disk contained in the Image in GB" (spec); real values per image are [?]. Likely patterns [K, unverified]: description "Fedora 44", "Rocky Linux 10", "AlmaLinux 10", "CentOS Stream 10", "openSUSE Leap 16.0"; `os_version` "44", "10", "10", "stream-10", "16.0"; `disk_size` 5 for Fedora, 10 for Alma/Rocky/CentOS, a larger value for openSUSE. Check once with `hcloud image list --type system -o json` on a real account, or accept approximations (nothing functional depends on them except the minimum disk check).
- `created`: only the announce dates above are known; use them.

## 2. Official generic cloud image per image (x86_64)

| image | stable URL base | file | format | checksum file / algorithm / layout |
|---|---|---|---|---|
| rocky-10 | https://dl.rockylinux.org/pub/rocky/10/images/x86_64/ | `Rocky-10-GenericCloud-Base.latest.x86_64.qcow2` (versioned today: `...Base-10.3-20261112.0...`, 568 MB) | qcow2 | `<file>.CHECKSUM`, SHA-256, BSD style `SHA256 (name) = hash` with a `# name: size bytes` comment line; also `.CHECKSUM.asc` [S] |
| alma-10 | https://repo.almalinux.org/almalinux/10/cloud/x86_64/images/ | `AlmaLinux-10-GenericCloud-latest.x86_64.qcow2` (versioned today: `...10.3-20261116.0...`, 554 MB). `-ext4-` variant also exists, and a `x86_64_v2` tree for 10.1 | qcow2 | one `CHECKSUM` for the directory, GNU style `hash  name` (64 hex = SHA-256), `.asc` beside it [S] |
| centos-stream-10 | https://cloud.centos.org/centos/10-stream/x86_64/images/ | `CentOS-Stream-GenericCloud-10-latest.x86_64.qcow2` (versioned today: `-10-20261118.0.`, 1.0 GB) | qcow2 | `<file>.SHA256SUM` (also .MD5SUM, .SHA1SUM), BSD style [S] |
| centos-stream-9 | https://cloud.centos.org/centos/9-stream/x86_64/images/ | `CentOS-Stream-GenericCloud-9-latest.x86_64.qcow2` (note: a second family `GenericCloud-x86_64-9` stopped at 20260728.1) | qcow2 | same | 
| rocky-9 | https://dl.rockylinux.org/pub/rocky/9/images/x86_64/ | `Rocky-9-GenericCloud-Base.latest.x86_64.qcow2` (9.9-20261112.0) | qcow2 | same as Rocky 10 [S] |
| alma-9 | https://repo.almalinux.org/almalinux/9/cloud/x86_64/images/ | `AlmaLinux-9-GenericCloud-latest.x86_64.qcow2` (9.9-20261116) | qcow2 | same as Alma 10 [S] |
| fedora-44 | https://download.fedoraproject.org/pub/fedora/linux/releases/44/Cloud/x86_64/images/ (mirror view checked: https://mirrors.kernel.org/fedora/releases/44/Cloud/x86_64/images/) | `Fedora-Cloud-Base-Generic-44-1.7.x86_64.qcow2` (557 MB). No "latest" name; the releases tree is frozen, so the versioned URL is stable. A `Fedora-Cloud-Base-UEFI-UKI-44-1.7` variant also exists | qcow2 | `Fedora-Cloud-44-1.7-x86_64-CHECKSUM`: **PGP clear-signed**, SHA-256, BSD style with `#` comment lines, one entry per image in the release [S] |
| fedora-43 | .../releases/43/Cloud/x86_64/images/ | `Fedora-Cloud-Base-Generic-43-1.6.x86_64.qcow2` | qcow2 | `Fedora-Cloud-43-1.6-x86_64-CHECKSUM`, same layout [S] |
| opensuse-16 | https://download.opensuse.org/distribution/leap/16.0/appliances/ | `Leap-16.0-Minimal-VM.x86_64-Cloud.qcow2` (324 MiB; also a `-Build18.96` build-numbered copy that gets replaced) | qcow2 | `<file>.sha256`, GNU style `hash  name`, SHA-256 [S] |

Retention pitfall [S]: Rocky keeps only the current versioned file next to `.latest`; Alma only the current; CentOS keeps several dated builds but prunes; openSUSE replaces the build-numbered file. A pinned versioned URL plus a stored hash therefore rots. Use the `latest` URL and fetch the checksum file at download time (as debian-12/13 do). Fedora is the exception (frozen). Checksum parsers needed: GNU (`hash  name`), BSD (`SHA256 (name) = hash`, with `#` lines), and Fedora's clear-signed multi-entry file (find the entry for the file name). GPG signatures exist for all but need not be verified.
Fedora hosts: `dl.fedoraproject.org` answered with an Anubis "Access Denied" bot page to my fetch; use the `download.fedoraproject.org` redirector or a mirror, and expect to follow redirects [?].
openSUSE has no "latest" problem but the Hyper-V variant `...-MS-HyperV.vhdx.xz` (216 MiB) exists; per SUSE docs it does not bring cloud-init (it uses Combustion/JeOS firstboot), so it is no substitute [S: https://documentation.suse.com/sles/16.0/html/SLES-Minimal-VM/index.html].

### Per-image capabilities

| | cloud-init + NoCloud | UEFI, shim signed by Microsoft UEFI CA | hv_storvsc / hv_netvsc in kernel and initramfs | network stack, cloud-init renderer | MAC |
|---|---|---|---|---|---|
| rocky-9/10 | cloud-init yes [K]; NoCloud works via `cidata` label [K] | hybrid BIOS+UEFI GPT, 100M ESP, osbuild description "hybrid" [S: https://osbuild.org/docs/user-guide/image-descriptions/rocky-10.1/qcow2/]; Rocky ships dual-signed shims (old and new MS CA) [S: https://rockylinux.org/news/2026-06-02-secure-boot-ca-kek-transition] | modules in kernel [K]; initramfs content [?] | NetworkManager [K]; EL10 has no ifcfg, so keyfile via cloud-init network-manager renderer [?] | SELinux enforcing [K] |
| alma-9/10 | cloud-init yes [S: https://wiki.almalinux.org/cloud/Generic-cloud.html]; NoCloud [K] | "unified BIOS and UEFI boot" since 8.9 [S: cloud-changelog]; Secure Boot on Hyper-V not documented [?] | [?] | NetworkManager [K] | SELinux enforcing [K] |
| centos-stream-9/10 | cloud-init yes [K] | osbuild says hybrid [S: https://osbuild.org/docs/user-guide/image-descriptions/centos-10/qcow2]; but Red Hat ticket CS-2672 reports x86_64 GenericCloud lacking UEFI/ESP content and a CentOS list message says Secure Boot works on Stream 9/10 qcow2 since the 2025-08-25 update [S via search snippets, not read in full] | [?] | NetworkManager [K] | SELinux enforcing [K] |
| fedora-43/44 | cloud-init yes [K] | hybrid, Fedora shim is Microsoft-signed [K] | [?] | NetworkManager [K]; Hetzner says cloud-init private-network autoconfig works on Fedora 44 [S: changelog 2026-05-18] | SELinux enforcing [K] |
| opensuse-16 | Cloud image: cloud-init, "supported with data supplied through the platform's interface"; NoCloud explicitly documented only for the kvm-and-xen image [S: SLES Minimal-VM doc] | layout: 2 MB BIOS boot + 512 MB EFI partition for all variants; Secure Boot not addressed [S] | all variants use `kernel-default-base`; Hyper-V drivers [?] | NetworkManager does DHCP [S]; upstream cloud-init's openSUSE distro class only has the `sysconfig` renderer (SUSE flavor, wicked-style) [S: cloud-init `distros/opensuse.py`], so how the Leap 16 package renders v2 is [?] | SELinux enforcing, no firewall in Cloud image [S] |

Hyper-V Gen2 template `MicrosoftUEFICertificateAuthority`: Microsoft's 2011 UEFI CA that signs Linux shims expires in 2026 and is being replaced by "Windows UEFI CA 2023"; a shim signed only by the new CA fails on firmware whose db has only the 2011 CA [S: Rocky post]. Rocky ships dual-signed shims; for the other distros I did not confirm that current shims carry the old signature. A freshly downloaded image today is the safest case, but a `dnf update` in the guest could change shim; check by first boot of each image.

## 3. Pitfalls

1. **/32 plus gateway 172.31.1.1 outside the subnet.** cloud-init mainline (`cloudinit/net/network_manager.py`, `network_state.py`, read on GitHub main) has no on-link handling at all: the v2 route key `on-link` is not in the documented subset for non-netplan renderers (https://docs.cloud-init.io/en/latest/reference/network-config-format-v2.html), and the NM keyfile renderer just copies the gateway. Whether NetworkManager then installs the default route is [?] (Red Hat tracks NM behaviour for gateways in a different subnet: https://access.redhat.com/solutions/4380141); EL downstream cloud-init may carry patches. The sysconfig renderer (openSUSE, EL9 fallback) has the same gap. Test per image with the real seed. Fallbacks, in order: (a) add to the seed an explicit host route `172.31.1.1/32` without gateway (v2 route `via` needs an address, so this may need v1 `routes` or a vendor-data script); (b) have the existing guest hook (`hlab-guest`/vendor-data shell script, which already writes a cloud-init drop-in) write a NetworkManager keyfile or run `nmcli`/`ip route add 172.31.1.1/32 dev X; ip route add default via 172.31.1.1 onlink`. Hetzner's own images avoid this because their DHCP-free datasource and patches are not NoCloud, so do not assume it works because "it works at Hetzner".
2. **Initramfs without hv drivers.** I could not confirm for any of the six distros that the shipped initramfs contains `hv_vmbus`/`hv_storvsc` (dracut generic vs hostonly build is not documented for these images; the Red Hat/MS fix is `add_drivers+=" hv_vmbus hv_netvsc hv_storvsc "` plus `dracut -f`, https://docs.redhat.com/en/documentation/red_hat_enterprise_linux/10/html/deploying_and_managing_rhel_on_microsoft_azure/installing-hyper-v-device-drivers). Failure shows as "dropped to dracut shell / cannot find root". cloud-init cannot fix it (it runs after root mount). If an image fails, the fix needs an offline step on the VHD (the existing agent job pattern) or a different image variant; for Fedora the `Azure` image is built for Hyper-V but has no NoCloud guarantee, for Rocky/Alma/CentOS `Azure` VHDs are another option [K]. Check with `lsinitrd | grep hv_` after the first boot of each image. openSUSE's "kernel-default-base" and Fedora/openSUSE KVM-only builds are the highest risk.
3. **qcow2 features vs go-qcow2reader v0.8.0** (checked in the module source in the Go mod cache): zlib-compressed clusters supported; **zstd compression is not registered by default** (`decompressors` has only zlib; `qcow2.SetDecompressor(CompressionTypeZstd, ...)` must be called, klauspost/compress is already an indirect dependency) and hlab does not call it; **external data file** is rejected with `ErrUnsupportedFeature`; **extended L2 entries** only log "experimental" (reads may be wrong, struct is marked "not supported yet"); encryption rejected; backing files are not resolved by the reader for conversion unless hlab handles them [?]. None of the six images was inspected for these bits (no download in this run); distros normally use zlib or uncompressed with compat 1.1 [K]. Run `qemu-img info` or a header read on each file once.
4. **x86-64-v3 for EL10.** Rocky 10, Alma 10 and CentOS Stream 10 are built for x86-64-v3 (AVX2) [K]; Alma publishes a `x86_64_v2` tree for 10.1 (https://mirrors.egr.msu.edu/almalinux/10.1/cloud/x86_64_v2/images/). A Hyper-V VM on a modern host exposes AVX2 unless processor compatibility mode is on; a kernel panic "unsupported CPU" would mean it is on.
5. **Serial console.** hlab serves COM1 as a pipe; the EL and Fedora cloud images normally carry `console=tty0 console=ttyS0,115200n8` [K] and systemd starts `serial-getty@ttyS0`; openSUSE Cloud image cmdline [?]. Without a console= parameter nothing is lost functionally except the server console feature.
6. **Network config re-render per boot.** The existing drop-in `99-hlab-network.cfg` (`updates: network: when: [boot-new-instance, boot]`) assumes cloud-init writes the NM keyfile/ifcfg on each boot; on EL, NM may already own a DHCP profile for eth0 created by the image (`/etc/NetworkManager/system-connections` or a `cloud-init` generated file); first boot needs a check that the old DHCP profile does not win over the static /32 profile.
7. **Disk growth.** `growpart`/`cloud-utils-growpart` and XFS (EL, openSUSE Cloud image uses a dynamically sized root; Fedora uses btrfs by default in current Cloud Base [K]) all self-grow through cloud-init; hlab's resize path (VHDX resize then reboot) should work, but btrfs/xfs cannot shrink, only matters if hlab ever shrinks.
8. **Package of qcow2 virtual size.** Image virtual sizes are around 10 GiB for EL images and 5 GiB for Fedora/openSUSE [K]; Hetzner's `disk_size` should not be smaller than the virtual size, or the minimum-server-disk check in hlab will reject the image.

## Per-image risk summary

- **rocky-10 / alma-10 / centos-stream-10**: medium. v3 CPU requirement; hv initramfs unverified; /32 route under NM keyfile unverified; CentOS Stream UEFI/Secure Boot claims conflict (try `CentOS-Stream-GenericCloud-10-latest`; if no ESP, the `x86_64`-suffixed 9 family hints at a separate UEFI-capable build, 10 has no such family in the listing).
- **rocky-9 / alma-9 / centos-stream-9**: medium-low, same stack as 10 minus v3 requirement; Hetzner availability of these names not confirmed.
- **fedora-44**: medium. Newest NM and cloud-init, which Hetzner itself supports for private networks; hv initramfs unverified; clear-signed checksum parser needed; download host may block bots.
- **fedora-43**: same as 44, plus must be created with a `deprecation` object and drops out of the list after 2027-02-12.
- **opensuse-16**: highest. Cloud image NoCloud unconfirmed, `kernel-default-base` hv drivers unconfirmed, SUSE renderer path (sysconfig vs NM) unclear, no cloud-init in the Hyper-V flavor, so there is no fallback image variant. Consider adding it last.
