#!/usr/bin/env bash
# Functions that hlab.sh and box.sh share; both source this file.

# is_alive <pid file> <comm prefix>: Succeed while the process the file names
# runs. A zombie does not count: it has closed its files and sockets, and only
# its parent can remove it.
is_alive() {
  local pid_file="$1" comm_prefix="$2" stat comm state
  [[ -f "${pid_file}" ]] || return 1
  stat="$(cat "/proc/$(cat "${pid_file}")/stat" 2>/dev/null)" || return 1
  # /proc/<pid>/stat: "<pid> (<comm>) <state> ..."
  comm="${stat#*(}"
  comm="${comm%)*}"
  state="${stat##*) }"
  [[ "${comm}" == "${comm_prefix}"* && "${state:0:1}" != Z ]]
}

# signal_pid <pid file> <comm prefix> <signal>: Send the signal to the process
# only if it is still the one the file names. The files outlive the container
# (/hlab is a volume), and in a new container their PIDs belong to others.
signal_pid() {
  local pid_file="$1" comm_prefix="$2" signal="$3"
  is_alive "${pid_file}" "${comm_prefix}" || return 0
  kill -s "${signal}" "$(cat "${pid_file}")" 2>/dev/null || true
}

# stop_pid <pid file> <comm prefix> [signal]: Kill the process only if it is
# still the one the PID file names, wait until it is gone, and remove the file.
# Waiting matters: a new process started right after binds the same addresses
# and fails while the old one still holds them. One that outlives the signal
# by 5 seconds gets KILL.
stop_pid() {
  local pid_file="$1" comm_prefix="$2" signal="${3:-TERM}" i
  signal_pid "${pid_file}" "${comm_prefix}" "${signal}"
  for ((i = 0; i < 60; i++)); do
    is_alive "${pid_file}" "${comm_prefix}" || break
    if ((i == 50)); then
      signal_pid "${pid_file}" "${comm_prefix}" KILL
    fi
    sleep 0.1
  done
  rm -f "${pid_file}"
}
