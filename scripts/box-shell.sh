#!/usr/bin/env bash
# box-shell: the ForceCommand of every Storage Box account (see box.sh). It
# runs inside the account's root: /usr is there (bash with it; /bin is a link
# to usr/bin), the account's data is /home, its settings /etc/hlab-access, the
# lab's addresses box-shell.env next to this script (box.sh installs both).
# The settings come from the two files sourced below:
# shellcheck disable=SC2154

# public_net, floating_net, client6, lab6, ipv6_prefix
# shellcheck source=/dev/null
source /usr/local/lib/lab/box-shell.env
# ssh, external, readonly, quota (bytes)
# shellcheck source=/dev/null
source /etc/hlab-access

# deny <message>...: Refuse the session with the message.
deny() {
  echo "$*" >&2
  exit 1
}

# check_writes: Refuse a command that writes on a read-only account.
# Globals:
#   readonly (read)
check_writes() {
  if [[ "${readonly}" == 1 ]]; then
    deny "read-only account"
  fi
}

# check_rsync <arg>...: Allow rsync as the server side of a transfer. Sending
# only reads, unless options make the server side write (remove what it sent,
# a log file). A daemon with its own configuration could run any program, so
# it is never allowed.
check_rsync() {
  local arg
  if [[ "$2" != --server ]]; then
    deny "command not allowed"
  fi
  if [[ "$3" != --sender ]]; then
    check_writes
  fi
  for arg in "$@"; do
    case "${arg}" in
      --daemon* | --config* | --dparam*) deny "command not allowed" ;;
      --remove-* | --log-file* | -M* | --remote-option*) check_writes ;;
    esac
  done
}

# check_scp <arg>...: Allow scp as the server side. Its own options (up to --
# or the first operand) say the direction: -t writes, -f only reads.
check_scp() {
  local mode="" arg
  shift
  for arg in "$@"; do
    case "${arg}" in
      -- | [!-]*) break ;;
    esac
    case "${arg}" in
      -*t*) mode="${mode}t" ;;
    esac
    case "${arg}" in
      -*f*) mode="${mode}f" ;;
    esac
  done
  case "${mode}" in
    *t*) check_writes ;;
    *f*) ;;
    *) deny "command not allowed" ;;
  esac
}

# print_df: Print the account's usage like df, against its box's quota.
# Globals:
#   quota (read)
print_df() {
  local used
  used="$(du -sb /home 2>/dev/null | cut -f1)"
  echo "Filesystem 1B-blocks Used Available Use% Mounted on"
  echo "storagebox ${quota} ${used} $((quota - used))" \
    "$((used * 100 / quota))% /home"
}

# check_access <port>: Refuse the session unless the account's settings allow
# the port and the client's address.
# Globals:
#   ssh, external, public_net, floating_net, client6, lab6, ipv6_prefix,
#   SSH_CLIENT (read)
check_access() {
  local port="$1" client_ip
  # Port 22 (SFTP) is always on; port 23 is the SSH support that ssh_enabled
  # switches.
  if [[ "${port}" != 22 && "${ssh}" != 1 ]]; then
    deny "SSH is disabled for this account"
  fi
  # Only from Hetzner's networks: servers and Floating IPs, not the Internet.
  # Cloud networks do not reach the box: like Hetzner's, it has public
  # addresses only.
  if [[ "${external}" != 1 ]]; then
    client_ip="${SSH_CLIENT%% *}"
    case "${client_ip}" in
      "${public_net}".* | "${floating_net}".*) ;;
      "${client6}":* | "${lab6}":*) deny "not reachable from outside" ;;
      "${ipv6_prefix}":*) ;;
      *) deny "not reachable from outside" ;;
    esac
  fi
}

# main: Run the session's command (SSH_ORIGINAL_COMMAND) if the account may.
# Globals:
#   readonly, SSH_CONNECTION, SSH_ORIGINAL_COMMAND (read)
main() {
  local port cmd
  port="$(echo "${SSH_CONNECTION}" | cut -d' ' -f4)"
  check_access "${port}"
  cd /home || exit 1
  cmd="${SSH_ORIGINAL_COMMAND:-}"
  case "${cmd}" in
    *sftp-server* | internal-sftp)
      if [[ "${readonly}" == 1 ]]; then
        exec /usr/lib/openssh/sftp-server -R -d /home
      fi
      exec /usr/lib/openssh/sftp-server -d /home
      ;;
  esac
  if [[ "${port}" != 23 ]]; then
    deny "This service allows sftp connections only."
  fi
  if [[ -z "${cmd}" ]]; then
    deny "the lab's Storage Box runs single commands only" \
      "(no interactive shell)"
  fi
  case "${cmd}" in
    *[\;\&\|\`\$\<\>]*) deny "command not allowed" ;;
  esac
  # The command's words become the arguments (no globbing).
  set -f
  # shellcheck disable=SC2086
  set -- ${cmd}
  case "$1" in
    ls | du | cat | head | tail | sha256sum | sha1sum | sha512sum | md5sum | \
      stat | readlink | realpath) ;;
    mkdir | rm | rmdir | mv | cp | touch | ln | chmod | dd) check_writes ;;
    rsync) check_rsync "$@" ;;
    scp) check_scp "$@" ;;
    # Hetzner's versions (BORG_REMOTE_PATH=borg-1.4): one borg here. borg
    # serve takes locks, even to read.
    borg | borg-1.1 | borg-1.2 | borg-1.4)
      if [[ "$2" != serve ]]; then
        deny "command not allowed"
      fi
      check_writes
      shift
      set -- borg "$@"
      ;;
    df)
      print_df
      exit 0
      ;;
    *) deny "command not allowed" ;;
  esac
  # Paths need no check: the account's root holds only its own directory.
  exec "$@"
}

main
