#!/usr/bin/env bash
# Functions hlab.sh and box.sh share; sourced by both.

# is_alive <pid file> <comm prefix>: Succeed while the process the file names
# runs.
is_alive() {
  local pid_file="$1" comm_prefix="$2" comm
  [[ -f "${pid_file}" ]] || return 1
  comm="$(cat "/proc/$(cat "${pid_file}")/comm" 2>/dev/null)" || return 1
  [[ "${comm}" == "${comm_prefix}"* ]]
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
# still the one the PID file names; remove the file.
stop_pid() {
  local pid_file="$1" comm_prefix="$2" signal="${3:-TERM}"
  signal_pid "${pid_file}" "${comm_prefix}" "${signal}"
  rm -f "${pid_file}"
}
