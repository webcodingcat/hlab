#!/usr/bin/env bash
# The Storage Box stand-in: one sshd on ports 22 (SFTP, always on) and 23 (SSH
# support: SFTP and a few commands, only with ssh_enabled), as on Hetzner; every
# account (box user u<id>, sub-account u<id>-sub<n>) locked into its own root
# where only its directory is visible, as /home. Called by hlabd
# (hlabd/fabric/storagebox.py), which keeps the boxes and accounts; nothing here
# decides anything.
#
# Layout: /hlab/box (size-limited ext4 image) holds data/<box>/ and
# snapshots/<box>/<name>/; every account gets a root /run/box-roots/<user>
# (root-owned, as sshd's ChrootDirectory demands) with /usr read-only and its
# directory bound to /home. OpenSSH's sftp-server renames by link()+unlink()
# like the Storage Box: a rename never overwrites.
#
# sshd runs two helpers, installed from this directory by start_box:
# box-keys.sh (AuthorizedKeysCommand) and box-shell.sh (ForceCommand).
#
# The lab is a test lab: box users run the operator's own code, so root does
# the work in their directories and hands the results to the account's IDs.
set -euo pipefail
# set -e also inside $(...)
shopt -s inherit_errexit

# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib.sh
source "$(dirname "$0")/lib.sh"

# The lab's addresses come from hlabd (hlabd/config.py) through hlab.sh.
readonly SERVICES="${HLAB_SERVICES:?}"
readonly SERVICES6="${HLAB_SERVICES6:?}"
readonly PUBLIC_NET="${HLAB_PUBLIC_NET:?}"
readonly FLOATING_NET="${HLAB_FLOATING_NET:?}"
readonly CLIENT_NET="${HLAB_CLIENT_NET:?}"
readonly IPV6_PREFIX="${HLAB_IPV6_PREFIX:?}"
readonly CLIENT6="${HLAB_CLIENT6:?}"
# The lab's own IPv6 service range (a /48).
readonly LAB6="${HLAB_SERVICES_NET6:?}"

readonly BOX=/hlab/box
readonly ROOTS=/run/box-roots
# The helpers live below /usr, which every account's root has (read-only).
readonly LIB=/usr/local/lib/lab
readonly PID_FILE=/run/box-sshd.pid

# Set once this run holds the lock of the access settings.
access_lock_held=0

# start_box <size gb>: Mount the box's image, install the helpers, write the
# sshd configuration, and (re)start sshd.
start_box() {
  local size_gb="$1"
  mkdir -p "${BOX}" /run/sshd "${ROOTS}" "${LIB}" /hlab/box-keys
  if ((size_gb > 0)) && ! mountpoint -q "${BOX}"; then
    mount_image "${size_gb}"
  fi
  mkdir -p "${BOX}/data" "${BOX}/snapshots"
  [[ -f /hlab/box-hostkey ]] \
    || ssh-keygen -q -t ed25519 -N "" -f /hlab/box-hostkey
  install_helpers
  write_sshd_config
  write_deny_config
  stop_pid "${PID_FILE}" sshd
  /usr/sbin/sshd -f /hlab/box-sshd.conf
}

# mount_image <size gb>: Mount /hlab/box.img at the box, creating it or
# growing it to the size first.
mount_image() {
  local size_gb="$1" n
  # The loop device belongs to the Docker host's kernel: create missing
  # /dev/loopN nodes, unmount on stop.
  for ((n = 0; n < 32; n++)); do
    [[ -e /dev/loop${n} ]] || mknod -m 0660 "/dev/loop${n}" b 7 "${n}"
  done
  if [[ ! -f /hlab/box.img ]]; then
    truncate -s "${size_gb}G" /hlab/box.img
    mkfs.ext4 -q -F -m 0 /hlab/box.img
  fi
  # size_gb grew: the box grows.
  if (($(stat -c %s /hlab/box.img) < size_gb * 1024 * 1024 * 1024)); then
    truncate -s "${size_gb}G" /hlab/box.img
    e2fsck -fy /hlab/box.img >/dev/null 2>&1 || true
    resize2fs /hlab/box.img >/dev/null
  fi
  mount -o loop /hlab/box.img "${BOX}"
}

# stop_box: Stop the box's sshd and unmount the box's image.
stop_box() {
  stop_pid "${PID_FILE}" sshd
  if mountpoint -q "${BOX}"; then
    umount -l "${BOX}" || true
  fi
}

# install_helpers: Install box-keys and box-shell where sshd runs them, and
# box-shell's addresses of the lab (box-shell.env) next to them.
install_helpers() {
  local script_dir
  script_dir="$(dirname "$0")"
  install -m 755 "${script_dir}/box-keys.sh" "${LIB}/box-keys"
  install -m 755 "${script_dir}/box-shell.sh" "${LIB}/box-shell"
  {
    printf 'public_net="%s"\n' "${PUBLIC_NET}"
    printf 'floating_net="%s"\n' "${FLOATING_NET}"
    printf 'client6="%s"\n' "${CLIENT6}"
    printf 'lab6="%s"\n' "${LAB6}"
    printf 'ipv6_prefix="%s"\n' "${IPV6_PREFIX}"
  } >"${LIB}/box-shell.env"
}

# write_sshd_config: Write the configuration of the one sshd for ports 22 and
# 23; every account is chrooted and runs box-shell.
write_sshd_config() {
  cat >/hlab/box-sshd.conf <<CFG
Port 22
Port 23
ListenAddress ${SERVICES}
ListenAddress [${SERVICES6}]
HostKey /hlab/box-hostkey
PidFile ${PID_FILE}
PasswordAuthentication yes
KbdInteractiveAuthentication no
UsePAM no
StrictModes no
AuthorizedKeysFile none
AuthorizedKeysCommand ${LIB}/box-keys %u %C
AuthorizedKeysCommandUser root
ChrootDirectory ${ROOTS}/%u
ForceCommand ${LIB}/box-shell
Subsystem sftp internal-sftp
DisableForwarding yes
AllowTcpForwarding no
X11Forwarding no
PermitTunnel no
MaxSessions 64
MaxStartups 64:30:100
PerSourcePenalties no
Include /hlab/box-deny.conf
CFG
}

# make_user_dir <uid> <data dir>: Make the account's directory where it is
# missing, with every directory it creates (the box's own data/<box> too) owned
# by the account's IDs; print it, normalized.
make_user_dir() {
  local uid="$1" data_dir="$2" data dir
  local missing=()
  data="$(realpath -ms -- "${data_dir}")"
  dir="${data}"
  while [[ ! -d "${dir}" ]]; do
    missing+=("${dir}")
    dir="$(dirname "${dir}")"
  done
  mkdir -p -- "${data}"
  if ((${#missing[@]} > 0)); then
    chown -- "${uid}:${uid}" "${missing[@]}"
  fi
  echo "${data}"
}

# set_account <user> <data dir> <quota bytes> <uid|box user>: Create or
# refresh an account and its root; read its password from stdin (it does not
# show in the process list; an empty one locks the password). A box user gets
# the UID hlabd gives (the files on box.img outlive the container and its
# /etc/passwd); a sub-account shares its box user's IDs, so the box user keeps
# full access to all its files.
set_account() {
  local user="$1" data_dir="$2" quota="$3" owner="$4"
  local root="${ROOTS}/${user}" password data uid dir
  IFS= read -r password || true
  lock_account "${user}"
  if ! id "${user}" >/dev/null 2>&1; then
    if [[ "${owner}" =~ ^[0-9]+$ ]]; then
      groupadd -o -g "${owner}" "${user}" 2>/dev/null || true
      useradd -M -d /home -s /bin/sh -o -u "${owner}" -g "${owner}" "${user}"
    else
      useradd -M -d /home -s /bin/sh -o -u "$(id -u "${owner}")" \
        -g "$(id -g "${owner}")" "${user}"
    fi
  fi
  uid="$(id -u "${user}")"
  if [[ -z "${password}" ]]; then
    usermod -p '*' "${user}"
  else
    printf '%s:%s\n' "${user}" "${password}" | chpasswd
  fi
  data="$(make_user_dir "${uid}" "${data_dir}")"
  mkdir -p /hlab/box-accounts
  echo "${data}" >"/hlab/box-accounts/${user}"
  if ! mountpoint -q "${root}/home" 2>/dev/null; then
    mkdir -p "${root}"/{usr,home,etc,dev}
    chmod 755 "${root}"
    mount --bind /usr "${root}/usr" \
      && mount -o remount,bind,ro,nosuid,nodev "${root}/usr"
    for dir in bin sbin lib lib64; do
      [[ -e "${root}/${dir}" ]] || ln -s "usr/${dir}" "${root}/${dir}"
    done
    [[ -e "${root}/dev/null" ]] || mknod -m 666 "${root}/dev/null" c 1 3
    mount --bind "${data}" "${root}/home"
  fi
  # sftp-server looks its user up: only this account is known inside the root.
  getent passwd "${user}" >"${root}/etc/passwd"
  getent group "$(id -g "${user}")" >"${root}/etc/group"
  # A new account starts closed until hlabd sets its access settings; an
  # existing one keeps them.
  [[ -f "${root}/etc/hlab-access" ]] || set_access "${user}" 0 0 1 "${quota}"
}

# lock_access_settings: Take the lock of the access settings, so one change
# happens at a time (box.sh runs in parallel for several accounts); held until
# box.sh ends.
# Globals:
#   access_lock_held (read, set)
lock_access_settings() {
  if ((access_lock_held)); then
    return 0
  fi
  exec 8>/run/box-access.lock
  flock 8
  access_lock_held=1
}

# set_access <user> <ssh 0|1> <external 0|1> <readonly 0|1> <quota bytes>:
# Write an account's access settings and let sshd reread its configuration.
set_access() {
  local user="$1" ssh="$2" external="$3" read_only="$4" quota="$5"
  lock_access_settings
  printf 'ssh=%s\nexternal=%s\nreadonly=%s\nquota=%s\n' \
    "${ssh}" "${external}" "${read_only}" "${quota}" \
    >"${ROOTS}/${user}/etc/hlab-access"
  write_deny_config
  signal_pid "${PID_FILE}" sshd HUP
}

# write_deny_config: Write the accounts sshd refuses before login: ssh
# disabled, or internal only and from outside.
write_deny_config() {
  local off=() internal=() file user
  lock_access_settings
  for file in "${ROOTS}"/*/etc/hlab-access; do
    [[ -f "${file}" ]] || continue
    user="${file#"${ROOTS}/"}"
    user="${user%%/*}"
    grep -qx 'ssh=1' "${file}" || off+=("${user}")
    grep -qx 'external=1' "${file}" || internal+=("${user}")
  done
  # ssh disabled shuts port 23 only (SFTP on 22 is always on); "outside" are
  # the Internet clients and the lab itself. sshd takes the first DenyUsers
  # that matches, so the case of both comes first.
  local outside="${CLIENT_NET}.0/24,${CLIENT6}::/48,${LAB6}::/48"
  local both=("${off[@]}" "${internal[@]}")
  {
    if ((${#both[@]} > 0)); then
      printf 'Match LocalPort 23 Address %s\n    DenyUsers %s\n' \
        "${outside}" "${both[*]}"
    fi
    if ((${#off[@]} > 0)); then
      printf 'Match LocalPort 23\n    DenyUsers %s\n' "${off[*]}"
    fi
    if ((${#internal[@]} > 0)); then
      printf 'Match Address %s\n    DenyUsers %s\n' \
        "${outside}" "${internal[*]}"
    fi
  } >/hlab/box-deny.conf.tmp
  # Replaced whole: sshd never reads half a file.
  mv /hlab/box-deny.conf.tmp /hlab/box-deny.conf
}

# lock_account <user>: Take the account's lock, so one change of it happens at
# a time (held until box.sh ends).
lock_account() {
  local user="$1"
  mkdir -p /run/box-locks
  exec 9>"/run/box-locks/${user}"
  flock 9
}

# unmount_below <dir>: Unmount everything mounted below a directory, deepest
# first; the directory's own mounts stay.
unmount_below() {
  local dir="$1" target
  while read -r target; do
    [[ "${target}" == "${dir}" ]] \
      || umount -l "$(printf '%b' "${target}")" 2>/dev/null || true
  done < <(findmnt -rn -o TARGET -R "${dir}" 2>/dev/null | tac)
}

# unmount_home <user>: Unmount the account's /home and everything below it,
# however often mounted.
unmount_home() {
  local user="$1"
  local root="${ROOTS}/${user}"
  unmount_below "${root}/home"
  while mountpoint -q "${root}/home" 2>/dev/null; do
    umount -l "${root}/home" || break
  done
}

# set_home <user> <data dir>: Move an account to another directory.
set_home() {
  local user="$1" data_dir="$2"
  local root="${ROOTS}/${user}" data
  lock_account "${user}"
  data="$(make_user_dir "$(id -u "${user}")" "${data_dir}")"
  unmount_home "${user}"
  mount --bind "${data}" "${root}/home"
  echo "${data}" >"/hlab/box-accounts/${user}"
}

# set_zfs_view <user> <snapshot dir|->: Show the snapshots read-only at
# /home/.zfs/snapshot (or hide them).
set_zfs_view() {
  local user="$1" snapshots="$2"
  local user_home="${ROOTS}/${user}/home"
  local zfs_dir="${user_home}/.zfs"
  local view="${zfs_dir}/snapshot"
  local owner
  lock_account "${user}"
  owner="$(id -u "${user}"):$(id -g "${user}")"
  # Every mount below /home is removed first.
  unmount_below "${user_home}"
  if [[ "${snapshots}" != "-" ]]; then
    mkdir -p "${snapshots}"
    # .zfs lies in the account's directory and belongs to it; a .zfs or
    # .zfs/snapshot the user made that is no real directory (a file, a
    # symlink) is removed, so the mount never follows a link out of the box.
    [[ -d "${zfs_dir}" && ! -L "${zfs_dir}" ]] || rm -f -- "${zfs_dir}"
    [[ -d "${view}" && ! -L "${view}" ]] || rm -f -- "${view}"
    mkdir -p -- "${view}"
    chown -h -- "${owner}" "${zfs_dir}" "${view}"
    mount --bind "${snapshots}" "${view}" \
      && mount -o remount,bind,ro "${view}"
  fi
}

# remove_account <user>: End the account's sessions and remove the account and
# its root (not its data).
remove_account() {
  local user="$1"
  local root="${ROOTS}/${user}"
  lock_account "${user}"
  # Only this account's sessions: sub-accounts share the box user's UID, so no
  # pkill -u.
  pkill -f "^sshd(-session)?: ${user}([@ ]|$)" 2>/dev/null || true
  unmount_home "${user}"
  umount -l "${root}/usr" 2>/dev/null || true
  # --one-file-system: should anything still be mounted there, the account's
  # data is not deleted through it.
  rm -rf --one-file-system "${root}" "/hlab/box-accounts/${user}" \
    "/hlab/box-keys/${user}"
  userdel -f "${user}" 2>/dev/null || true
  write_deny_config
}

# add_key <user> <public key>: Add a key of the lab's own to an account.
add_key() {
  local user="$1" public_key="$2"
  printf '%s\n' "${public_key}" >"/hlab/box-keys/${user}"
  chmod 644 "/hlab/box-keys/${user}"
}

# write_authorized_keys <user> <keys>: Write .ssh/authorized_keys in the
# account's directory, owned by the account.
write_authorized_keys() {
  local user="$1" keys="$2" ssh_dir owner
  ssh_dir="$(cat "/hlab/box-accounts/${user}")/.ssh"
  owner="$(id -u "${user}"):$(id -g "${user}")"
  # A .ssh or authorized_keys the user made as a symlink is replaced, not
  # followed.
  [[ -d "${ssh_dir}" && ! -L "${ssh_dir}" ]] || rm -f -- "${ssh_dir}"
  mkdir -p -- "${ssh_dir}"
  rm -f -- "${ssh_dir}/authorized_keys"
  printf '%s\n' "${keys}" >"${ssh_dir}/authorized_keys"
  chown -h -- "${owner}" "${ssh_dir}" "${ssh_dir}/authorized_keys"
}

# take_snapshot <data dir> <snapshot dir>: Copy the data aside first (a
# half-done copy never shows under .zfs), then move it into place. rsync -a as
# root keeps every file's owner.
take_snapshot() {
  local data="$1" dest="$2" work rc=0
  work="${BOX}/snapshots/.work/$(basename "$(dirname "${dest}")")"
  work="${work}-$(basename "${dest}")"
  mkdir -p "$(dirname "${dest}")" "${BOX}/snapshots/.work"
  rm -rf "${work}"
  rsync -aHAX --exclude=/.zfs "${data}/" "${work}/" || rc=$?
  # 24: files vanished meanwhile, which a snapshot of a live box allows.
  if ((rc != 0 && rc != 24)); then
    rm -rf "${work}"
    echo "box.sh: snapshot incomplete (rsync ${rc})" >&2
    return "${rc}"
  fi
  mv "${work}" "${dest}"
}

# roll_back <snapshot dir> <data dir>: Make the data what the snapshot holds
# (rsync -a as root keeps every file's owner).
roll_back() {
  local snapshot="$1" data="$2"
  rsync -aHAX --delete --exclude=/.zfs "${snapshot}/" "${data}/"
}

# remove_tree <dir>: Remove a directory tree on the box.
remove_tree() {
  local dir="$1"
  rm -rf "${dir}"
}

# print_bytes_used <dir>: Print the bytes used below the directory (nothing
# when it is missing).
print_bytes_used() {
  local dir="$1"
  du -sb "${dir}" 2>/dev/null | cut -f1
}

# list_folders <dir>: Print the directories in it (none when it is missing).
list_folders() {
  local dir="$1"
  [[ -d "${dir}" ]] || return 0
  find "${dir}" -mindepth 1 -maxdepth 1 -type d ! -name .zfs -printf '%f\n' \
    | sort
}

# main <verb> <arg>...: Run one verb.
main() {
  local verb="${1:-}"
  shift || true
  case "${verb}" in
    start) start_box "$@" ;;
    stop) stop_box ;;
    account) set_account "$@" ;;
    access) set_access "$@" ;;
    home) set_home "$@" ;;
    zfs) set_zfs_view "$@" ;;
    remove) remove_account "$@" ;;
    key) add_key "$@" ;;
    authorized-keys) write_authorized_keys "$@" ;;
    snapshot) take_snapshot "$@" ;;
    rollback) roll_back "$@" ;;
    rmtree) remove_tree "$@" ;;
    usage) print_bytes_used "$@" ;;
    folders) list_folders "$@" ;;
    *)
      echo "box.sh: unknown command: ${verb}" >&2
      exit 2
      ;;
  esac
}

main "$@"
