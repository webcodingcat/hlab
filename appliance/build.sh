#!/usr/bin/env bash
# Build the HLab appliance's disk: Debian 13 with the packages of the data
# plane and hlab-agent, as a dynamic VHDX for a generation 2 VM with Secure
# Boot (shim and grub signed for the Microsoft UEFI CA). Runs without root:
# mmdebstrap works in a user namespace, mkfs.ext4 fills the file system from
# a tarball, mtools fill the EFI partition (docs/appliance.md).
#   appliance/build.sh <hlab-agent binary> <output .vhdx>
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly HERE
readonly MIRROR="http://deb.debian.org/debian"
readonly SECURITY_MIRROR="http://deb.debian.org/debian-security"
readonly KEYRING="/usr/share/keyrings/debian-archive-keyring.gpg"
# the disk's size: the VHDX grows only with what is written
readonly DISK_MB=4096
readonly ESP_MB=64
# partition types of the GPT: EFI system, Linux file system
readonly ESP_TYPE="C12A7328-F81F-11D2-BA4B-00A0C93EC93B"
readonly LINUX_TYPE="0FC63DAF-8483-4772-8E79-3D69D8477DE4"
# what the appliance needs, all from Debian 13 (docs/appliance.md)
readonly PACKAGES=(
  # boot: the cloud kernel has Hyper-V's drivers and is signed
  linux-image-cloud-amd64 initramfs-tools systemd-sysv dbus
  shim-signed grub-efi-amd64-signed
  # the data plane
  nftables dnsmasq-base iproute2 knot unbound haproxy pebble
  # unbound's DNSSEC trust anchor: without it unbound does not start (a
  # Recommends, which --variant=apt leaves out)
  dns-root-data
  # the volumes' file systems (agentproto.PathFormat)
  e2fsprogs xfsprogs
  # Storage Boxes and image conversion (later phases)
  openssh-server rsync borgbackup btrfs-progs qemu-utils
  # to look around on the console
  procps
)

#######################################
# Print an error and exit.
# Arguments:
#   The message.
#######################################
die() {
  echo "appliance/build.sh: $*" >&2
  exit 1
}

#######################################
# Build the root file system as a tarball, and take the signed boot loader
# out of it.
# Arguments:
#   The agent binary, the work folder, the root file system's UUID.
#######################################
bootstrap() {
  local agent="$1" work="$2" uuid="$3"
  local packages
  packages="$(
    IFS=,
    echo "${PACKAGES[*]}"
  )"
  # the keyring explicitly: apt on Ubuntu does not trust Debian's keys
  mmdebstrap --mode=unshare --variant=apt --format=tar \
    --keyring="${KEYRING}" --include="${packages}" \
    --customize-hook="sync-in ${HERE}/files /" \
    --customize-hook="upload ${agent} /usr/local/sbin/hlab-agent" \
    --customize-hook="upload ${HERE}/customize.sh /customize.sh" \
    --customize-hook="chroot \"\$1\" bash /customize.sh ${uuid}" \
    --customize-hook="rm \"\$1/customize.sh\"" \
    --customize-hook="download /usr/lib/shim/shimx64.efi.signed \
${work}/shimx64.efi" \
    --customize-hook="download \
/usr/lib/grub/x86_64-efi-signed/grubx64.efi.signed ${work}/grubx64.efi" \
    trixie "${work}/rootfs.tar" \
    "deb ${MIRROR} trixie main" \
    "deb ${MIRROR} trixie-updates main" \
    "deb ${SECURITY_MIRROR} trixie-security main"
}

#######################################
# Build the EFI system partition: shim as the default boot loader, which
# starts grub next to it; grub reads its configuration from EFI/debian,
# where Debian's signed grub looks, and boots the kernel of the root file
# system.
# Arguments:
#   The work folder, the root file system's UUID.
#######################################
make_esp() {
  local work="$1" uuid="$2" esp="$1/esp.img"
  cat >"${work}/grub.cfg" <<EOF
set timeout=0
search --no-floppy --fs-uuid --set=root ${uuid}
linux /vmlinuz root=UUID=${uuid} ro console=tty0 console=ttyS0 quiet
initrd /initrd.img
boot
EOF
  mkfs.vfat -C -n EFI "${esp}" $((ESP_MB * 1024)) >/dev/null
  mmd -i "${esp}" ::/EFI ::/EFI/BOOT ::/EFI/debian
  mcopy -i "${esp}" "${work}/shimx64.efi" ::/EFI/BOOT/BOOTX64.EFI
  mcopy -i "${esp}" "${work}/grubx64.efi" ::/EFI/BOOT/grubx64.efi
  mcopy -i "${esp}" "${work}/grub.cfg" ::/EFI/debian/grub.cfg
}

#######################################
# Put the EFI partition and the root file system into a disk with a GPT.
# The last MiB stays free for the backup GPT.
# Arguments:
#   The work folder, the root file system's UUID.
#######################################
make_disk() {
  local work="$1" uuid="$2" disk="$1/disk.raw"
  local root_mb=$((DISK_MB - ESP_MB - 2))
  mkfs.ext4 -q -L hlab-root -U "${uuid}" -d "${work}/rootfs.tar" \
    "${work}/root.img" "${root_mb}M"
  truncate -s "${DISK_MB}M" "${disk}"
  sfdisk --quiet "${disk}" <<EOF
label: gpt
start=1MiB, size=${ESP_MB}MiB, type=${ESP_TYPE}, name=esp
start=$((ESP_MB + 1))MiB, size=${root_mb}MiB, type=${LINUX_TYPE}, name=root
EOF
  dd if="${work}/esp.img" of="${disk}" bs=1M seek=1 conv=notrunc \
    status=none
  dd if="${work}/root.img" of="${disk}" bs=1M seek=$((ESP_MB + 1)) \
    conv=notrunc,sparse status=none
}

main() {
  [[ $# -eq 2 ]] || die "usage: appliance/build.sh <hlab-agent> <out.vhdx>"
  local agent output work uuid
  agent="$(realpath "$1")"
  output="$2"
  [[ -x "${agent}" ]] || die "no agent binary at ${agent}"
  local tool
  for tool in mmdebstrap mkfs.vfat mmd mkfs.ext4 sfdisk qemu-img; do
    command -v "${tool}" >/dev/null \
      || die "${tool} is missing (docs/appliance.md)"
  done
  work="$(mktemp -d)"
  # shellcheck disable=SC2064 # work is fixed now
  trap "rm -rf '${work}'" EXIT
  uuid="$(cat /proc/sys/kernel/random/uuid)"
  bootstrap "${agent}" "${work}" "${uuid}"
  make_esp "${work}" "${uuid}"
  make_disk "${work}" "${uuid}"
  mkdir -p "$(dirname "${output}")"
  # 1 MiB blocks, as Microsoft advises for Linux guests
  qemu-img convert -f raw -O vhdx -o subformat=dynamic,block_size=1M \
    "${work}/disk.raw" "${output}"
}

main "$@"
