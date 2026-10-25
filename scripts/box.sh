#!/usr/bin/env bash
# The Storage Box stand-in: one sshd on ports 22 (SFTP only) and 23 (SFTP and a few commands), every account
# (box user u<id>, sub-account u<id>-sub<n>) locked into its own root where only its directory is visible, as /home.
# Called by hlabd (hlabd/fabric/storagebox.py), which keeps the boxes and accounts; nothing here decides anything.
#
# Layout: /hlab/box (size-limited ext4 image) holds data/<box>/ and snapshots/<box>/<name>/; every account gets a
# root /run/box-roots/<user> (root-owned, as sshd's ChrootDirectory demands) with /usr read-only and its directory
# bound to /home. OpenSSH's sftp-server renames by link()+unlink() like the Storage Box: a rename never overwrites.
set -euo pipefail

svc=198.18.0.1
svc6=2001:db8:fffe::1
box=/hlab/box
roots=/run/box-roots
lib=/usr/local/lib/lab

start() { # start <size gb>
    local size="$1"
    mkdir -p "${box}" /run/sshd "${roots}" "${lib}" /hlab/box-keys
    # The loop device belongs to the Docker host's kernel: create missing /dev/loopN nodes, unmount on stop.
    if [[ "${size}" -gt 0 ]] && ! mountpoint -q "${box}"; then
        for n in $(seq 0 31); do [[ -e /dev/loop${n} ]] || mknod -m 0660 "/dev/loop${n}" b 7 "${n}"; done
        [[ -f /hlab/box.img ]] || { truncate -s "${size}G" /hlab/box.img; mkfs.ext4 -q -F -m 0 /hlab/box.img; }
        if (( $(stat -c %s /hlab/box.img) < size * 1024 * 1024 * 1024 )); then   # size_gb grew: the box grows
            truncate -s "${size}G" /hlab/box.img
            e2fsck -fy /hlab/box.img >/dev/null 2>&1 || true
            resize2fs /hlab/box.img >/dev/null
        fi
        mount -o loop /hlab/box.img "${box}"
    fi
    mkdir -p "${box}/data" "${box}/snapshots"
    [[ -f /hlab/box-hostkey ]] || ssh-keygen -q -t ed25519 -N "" -f /hlab/box-hostkey
    write_shell
    # Keys: the lab's own (/hlab/box-keys) and, like on the Storage Box, .ssh/authorized_keys in the account's home.
    cat > "${lib}/box-keys" <<'SH'
#!/bin/sh
cat "/hlab/box-keys/$1" 2>/dev/null
# the user's own keys, as on the Storage Box
head -c 65536 "$(cat "/hlab/box-accounts/$1" 2>/dev/null)/.ssh/authorized_keys" 2>/dev/null
exit 0
SH
    chmod 755 "${lib}/box-keys"
    cat > /hlab/box-sshd.conf <<CFG
Port 22
Port 23
ListenAddress ${svc}
ListenAddress [${svc6}]
HostKey /hlab/box-hostkey
PidFile /run/box-sshd.pid
PasswordAuthentication yes
KbdInteractiveAuthentication no
UsePAM no
StrictModes no
AuthorizedKeysFile none
AuthorizedKeysCommand ${lib}/box-keys %u
AuthorizedKeysCommandUser root
ChrootDirectory ${roots}/%u
ForceCommand ${lib}/box-shell
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
    deny_config
    stop
    /usr/sbin/sshd -f /hlab/box-sshd.conf
}

stop() {
    local p
    if [[ -f /run/box-sshd.pid ]]; then
        p="$(cat /run/box-sshd.pid)"
        [[ "$(cat "/proc/${p}/comm" 2>/dev/null)" == sshd* ]] && kill "${p}" 2>/dev/null || true
        rm -f /run/box-sshd.pid
    fi
}

write_shell() {
    # Runs inside the account's root: /usr is there, the account's data is /home, its settings /etc/hlab-access.
    cat > "${lib}/box-shell" <<'SH'
#!/bin/sh
. /etc/hlab-access   # ssh, external, readonly, quota (bytes)
deny() { echo "$1" >&2; exit 1; }
[ "${ssh}" = 1 ] || deny "SSH is disabled for this account"
if [ "${external}" != 1 ]; then  # only from Hetzner's networks: servers and Floating IPs, not the Internet
    ip="${SSH_CLIENT%% *}"
    case "${ip}" in
        198.51.100.*|203.0.113.*) ;;   # cloud networks do not reach the box (as on Hetzner, it is public)
        2001:db8:ffff:*|2001:db8:fffe:*) deny "not reachable from outside" ;;
        2001:db8:*) ;;
        *) deny "not reachable from outside" ;;
    esac
fi
port="$(echo "${SSH_CONNECTION}" | cut -d' ' -f4)"
cd /home || exit 1
cmd="${SSH_ORIGINAL_COMMAND:-}"
ro=""; [ "${readonly}" = 1 ] && ro="-R"
case "${cmd}" in
    *sftp-server*|internal-sftp) exec /usr/lib/openssh/sftp-server ${ro} -d /home ;;
esac
[ "${port}" = 23 ] || deny "This service allows sftp connections only."
[ -n "${cmd}" ] || deny "the lab's Storage Box runs single commands only (no interactive shell)"
case "${cmd}" in *[\;\&\|\`\$\<\>]*) deny "command not allowed" ;; esac
set -f
set -- ${cmd}
writes() { [ "${readonly}" = 1 ] && deny "read-only account"; return 0; }
case "$1" in
    ls|du|cat|head|tail|sha256sum|sha1sum|sha512sum|md5sum|stat|readlink|realpath) ;;
    mkdir|rm|rmdir|mv|cp|touch|ln|chmod|dd) writes ;;
    rsync) [ "$2" = --server ] || deny "command not allowed"   # rsync over SSH: the box is the server side
           [ "$3" = --sender ] || writes
           # sending only reads - unless options make the server side write (remove what it sent, a log file);
           # a daemon with its own configuration would run any program: never
           for a in "$@"; do
               case "${a}" in
                   --daemon*|--config*|--dparam*) deny "command not allowed" ;;
                   --remove-*|--log-file*|-M*|--remote-option*) writes ;;
               esac
           done ;;
    scp) mode=""; first=1   # scp's own options (up to -- or the first operand): -t writes, -f only reads
         for a in "$@"; do
             if [ "${first}" = 1 ]; then first=0; continue; fi
             case "${a}" in --|[!-]*) break ;; esac
             case "${a}" in -*t*) mode="${mode}t" ;; esac
             case "${a}" in -*f*) mode="${mode}f" ;; esac
         done
         case "${mode}" in *t*) writes ;; *f*) ;; *) deny "command not allowed" ;; esac ;;
    borg|borg-1.1|borg-1.2|borg-1.4)                 # Hetzner's versions (BORG_REMOTE_PATH=borg-1.4): one borg here
          [ "$2" = serve ] || deny "command not allowed"
          writes                                     # borg serve takes locks, even to read
          shift; set -- borg "$@" ;;
    df) used="$(du -sb /home 2>/dev/null | cut -f1)"
        printf 'Filesystem 1B-blocks Used Available Use%% Mounted on\nstoragebox %s %s %s %s%% /home\n' \
            "${quota}" "${used}" "$((quota - used))" "$((used * 100 / quota))"
        exit 0 ;;
    *) deny "command not allowed" ;;
esac
exec "$@"   # paths need no check: the account's root holds only its own directory
SH
    chmod 755 "${lib}/box-shell"
}

inside() { # inside <path>: the path with every symlink resolved, if it stays in its box's directory (data/<box>)
    # Users create symlinks in their directories (SFTP symlink); root must never follow one out of the box.
    local rel="${1#"${box}/data/"}" p
    local base="${box}/data/${rel%%/*}"
    p="$(realpath -m -- "$1")"
    if [[ "$1" != "${box}/data/"?* || ( "${p}" != "${base}" && "${p}" != "${base}/"* ) ]]; then
        echo "path leaves the Storage Box: $1" >&2
        exit 1
    fi
    printf '%s\n' "${p}"
}

as_user() { # as_user <uid> <command>...: in a user's own files the lab acts as that user, never as root – a
    # symlink the user swaps in can only lead where the user may write anyway
    local uid="$1"
    shift
    setpriv --reuid="${uid}" --regid="${uid}" --clear-groups -- "$@"
}

user_dir() { # user_dir <uid> <data dir>: the account's directory, made (as the user) where it is missing; prints it
    local uid="$1" data rel base
    data="$(inside "$2")"
    rel="${data#"${box}/data/"}"
    base="${box}/data/${rel%%/*}"
    if [[ ! -d "${base}" ]]; then   # the box's own directory: data/ is root's, nobody else reaches it
        mkdir -p "${base}"
        chown "${uid}:${uid}" "${base}"
    fi
    as_user "${uid}" mkdir -p -- "${data}"
    inside "${data}"
}

account() { # account <user> <data dir> <password|-|@stdin> <quota bytes> <uid|box user>: create or refresh an
    # account and its root; a sub-account shares the box user's IDs, so the box user keeps full access to all its
    # files. The UID comes from hlabd: the files on box.img outlive the container and its /etc/passwd. @stdin
    # reads the password from stdin (it does not show in the process list).
    local user="$1" data pw="$3" root="${roots}/$1" owner="$5" uid
    if [[ "${pw}" == "@stdin" ]]; then IFS= read -r pw || true; fi
    lock_account "${user}"
    if ! id "${user}" >/dev/null 2>&1; then
        if [[ "${owner}" =~ ^[0-9]+$ ]]; then
            groupadd -o -g "${owner}" "${user}" 2>/dev/null || true
            useradd -M -d /home -s /bin/sh -o -u "${owner}" -g "${owner}" "${user}"
        else
            useradd -M -d /home -s /bin/sh -o -u "$(id -u "${owner}")" -g "$(id -g "${owner}")" "${user}"
        fi
    fi
    uid="$(id -u "${user}")"
    if [[ "${pw}" == "-" ]]; then usermod -p '*' "${user}"; else printf '%s:%s\n' "${user}" "${pw}" | chpasswd; fi
    data="$(user_dir "${uid}" "$2")"
    mkdir -p /hlab/box-accounts
    echo "${data}" > "/hlab/box-accounts/${user}"
    if ! mountpoint -q "${root}/home" 2>/dev/null; then
        mkdir -p "${root}"/{usr,home,etc,dev}
        chmod 755 "${root}"
        mount --bind /usr "${root}/usr" && mount -o remount,bind,ro,nosuid,nodev "${root}/usr"
        for d in bin sbin lib lib64; do [[ -e "${root}/${d}" ]] || ln -s "usr/${d}" "${root}/${d}"; done
        [[ -e "${root}/dev/null" ]] || mknod -m 666 "${root}/dev/null" c 1 3
        mount --bind "${data}" "${root}/home"
    fi
    # sftp-server looks its user up: only this account is known inside the root
    getent passwd "${user}" > "${root}/etc/passwd"
    getent group "$(id -g "${user}")" > "${root}/etc/group"
    # a new account starts closed until hlabd sets its access settings; an existing one keeps them
    [[ -f "${root}/etc/hlab-access" ]] || access "${user}" 0 0 1 "$4"
}

lock_deny() { # one change of the access settings at a time (box.sh runs in parallel for several accounts); held
    # until box.sh ends
    [[ -n "${deny_locked:-}" ]] && return 0
    exec 8>/run/box-deny.lock
    flock 8
    deny_locked=1
}

access() { # access <user> <ssh 0|1> <external 0|1> <readonly 0|1> <quota bytes>
    lock_deny
    printf 'ssh=%s\nexternal=%s\nreadonly=%s\nquota=%s\n' "$2" "$3" "$4" "$5" > "${roots}/$1/etc/hlab-access"
    deny_config
    [[ -f /run/box-sshd.pid ]] && kill -HUP "$(cat /run/box-sshd.pid)" 2>/dev/null || true   # sshd rereads it
}

deny_config() { # the accounts sshd refuses before login: ssh disabled, or internal only and from outside
    local off=() internal=() f u
    lock_deny
    for f in "${roots}"/*/etc/hlab-access; do
        [[ -f "${f}" ]] || continue
        u="${f#"${roots}/"}"; u="${u%%/*}"
        grep -qx 'ssh=1' "${f}" || off+=("${u}")
        grep -qx 'external=1' "${f}" || internal+=("${u}")
    done
    {
        [[ ${#off[@]} -gt 0 ]] && echo "DenyUsers ${off[*]}"
        if [[ ${#internal[@]} -gt 0 ]]; then   # outside: the Internet clients and the lab itself
            echo "Match Address 192.0.2.0/24,2001:db8:ffff::/48,2001:db8:fffe::/48"
            echo "    DenyUsers ${internal[*]}"
        fi
        true
    } > /hlab/box-deny.conf.tmp
    mv /hlab/box-deny.conf.tmp /hlab/box-deny.conf   # whole: sshd never reads half a file
}

lock_account() { # lock_account <user>: one change of an account at a time (held until box.sh ends)
    mkdir -p /run/box-locks
    exec 9>"/run/box-locks/$1"
    flock 9
}

unmount_home() { # unmount_home <user>: the account's /home and everything below it, however often mounted
    local root="${roots}/$1" t
    while read -r t; do
        [[ "${t}" == "${root}/home" ]] || umount -l "$(printf '%b' "${t}")" 2>/dev/null || true
    done < <(findmnt -rn -o TARGET -R "${root}/home" 2>/dev/null | tac)
    while mountpoint -q "${root}/home" 2>/dev/null; do umount -l "${root}/home" || break; done
}

home() { # home <user> <data dir>: move an account to another directory
    local root="${roots}/$1" data
    lock_account "$1"
    data="$(user_dir "$(id -u "$1")" "$2")"
    unmount_home "$1"
    mount --bind "${data}" "${root}/home"
    echo "${data}" > "/hlab/box-accounts/$1"
}

zfs() { # zfs <user> <snapshot dir|->: show the snapshots read-only at /home/.zfs/snapshot (or hide them)
    local home="${roots}/$1/home" mnt="${roots}/$1/home/.zfs/snapshot" t
    lock_account "$1"
    while read -r t; do   # every mount below /home
        umount -l "$(printf '%b' "${t}")" 2>/dev/null || true
    done < <(findmnt -rn -o TARGET -R "${home}" 2>/dev/null | tail -n +2 | tac)
    if [[ "$2" != "-" ]]; then
        mkdir -p "$2"
        # in the user's directory, made by the user; a .zfs that is no directory (a file, a symlink) gives way
        [[ -d "${home}/.zfs" && ! -L "${home}/.zfs" ]] || as_user "$(id -u "$1")" rm -f -- "${home}/.zfs"
        as_user "$(id -u "$1")" mkdir -p -- "${mnt}"
        mount --bind "$2" "${mnt}" && mount -o remount,bind,ro "${mnt}"
    fi
}

remove() { # remove <user>
    local root="${roots}/$1"
    # only this account's sessions: sub-accounts share the box user's UID, so no pkill -u
    lock_account "$1"
    pkill -f "^sshd(-session)?: $1([@ ]|$)" 2>/dev/null || true
    unmount_home "$1"
    umount -l "${root}/usr" 2>/dev/null || true
    # --one-file-system: should anything still be mounted there, the account's data is not deleted through it
    rm -rf --one-file-system "${root}" "/hlab/box-accounts/$1" "/hlab/box-keys/$1"
    userdel -f "$1" 2>/dev/null || true
    deny_config
}

cmd="${1:-}"
shift || true
case "${cmd}" in
    start) start "$@" ;;
    stop) stop; mountpoint -q "${box}" && umount -l "${box}" || true ;;
    account) account "$@" ;;
    access) access "$@" ;;
    home) home "$@" ;;
    zfs) zfs "$@" ;;
    remove) remove "$@" ;;
    key) cp "$2" "/hlab/box-keys/$1"; chmod 644 "/hlab/box-keys/$1" ;;   # key <user> <public key file>
    authorized-keys) # authorized-keys <user> <keys>: .ssh/authorized_keys in the account's directory (as the user)
        home="$(inside "$(cat "/hlab/box-accounts/$1")")"
        printf '%s\n' "$2" | as_user "$(id -u "$1")" sh -c \
            'cd "$1" && mkdir -p .ssh && rm -f .ssh/authorized_keys && cat > .ssh/authorized_keys' sh "${home}" ;;
    snapshot) # snapshot <data> <snap> <uid>: copied aside first (a half-done copy never shows under .zfs), as the
        # box's user: a symlink swapped in during the copy cannot make it read the lab's own files
        work="${box}/snapshots/.work/$(basename "$(dirname "$2")")-$(basename "$2")"
        mkdir -p "$(dirname "$2")" "${box}/snapshots/.work"
        rm -rf "${work}"
        mkdir "${work}"
        chown "$3:$3" "${work}"
        rc=0
        as_user "$3" rsync -aHAX --exclude=/.zfs "$1/" "${work}/" || rc=$?
        if [[ ${rc} -ne 0 && ${rc} -ne 24 ]]; then   # 24: files vanished meanwhile; 23 (unreadable files) fails
            rm -rf "${work}"; echo "snapshot incomplete (rsync ${rc}): files the account cannot read" >&2; exit "${rc}"
        fi
        mv "${work}" "$2" ;;
    rollback) # rollback <snapshot dir> <data dir> <uid>: as the box's user, for the same reason
        as_user "$3" rsync -aHAX --delete --exclude=/.zfs "$1/" "$2/" ;;
    rmtree) rm -rf "$1" ;;
    usage) du -sb "$1" 2>/dev/null | cut -f1 ;;                     # usage <dir>: bytes
    folders) # folders <dir>: the directories in it (none when the path leaves the box)
        d="$(inside "$1")" || exit 0
        rel="${d#"${box}/data/"}"
        as_user "$(stat -c %u "${box}/data/${rel%%/*}")" \
            find "${d}" -mindepth 1 -maxdepth 1 -type d ! -name .zfs -printf '%f\n' | sort ;;   # as the box's user
    *) echo "unknown command: ${cmd}" >&2; exit 2 ;;
esac
