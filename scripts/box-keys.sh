#!/usr/bin/env bash
# box-keys <user> <connection (%C)>: sshd's AuthorizedKeysCommand for the
# Storage Box (see box.sh). Print the account's keys: the lab keys from
# /hlab/box-keys and, as on Hetzner, .ssh/authorized_keys in the account's
# home. As on Hetzner, port 23 takes keys in OpenSSH format, and port 22 takes
# only keys in RFC4716 format (ssh-keygen -e).

# print_rfc4716_keys <keys file>: Print every RFC4716 key in the file in
# OpenSSH format, each block converted in a file of its own.
print_rfc4716_keys() {
  local keys_file="$1" blocks_dir block
  blocks_dir="$(mktemp -d)"
  awk -v dir="${blocks_dir}" '
    /^---- BEGIN SSH2 PUBLIC KEY ----/ { count++; block = dir "/" count }
    block { print > block }
    /^---- END SSH2 PUBLIC KEY ----/ { close(block); block = "" }' \
    "${keys_file}" 2>/dev/null
  for block in "${blocks_dir}"/*; do
    if [[ -f "${block}" ]]; then
      ssh-keygen -i -m RFC4716 -f "${block}" 2>/dev/null
    fi
  done
  rm -rf "${blocks_dir}"
}

# main <user> <connection>: Print the account's keys for the connection's
# port.
main() {
  local user="$1" port home keys_file
  # %C: local address, local port, client address, client port
  port="$(echo "$*" | awk '{print $3}')"
  home="$(cat "/hlab/box-accounts/${user}" 2>/dev/null)"
  keys_file="${home}/.ssh/authorized_keys"
  cat "/hlab/box-keys/${user}" 2>/dev/null
  if [[ "${port}" != 22 ]]; then
    cat "${keys_file}" 2>/dev/null
  else
    print_rfc4716_keys "${keys_file}"
  fi
  exit 0
}

main "$@"
