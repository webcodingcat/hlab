#!/usr/bin/env bash
# OpenTofu + hcloud provider against a running lab: apply, a second plan must
# show no changes, the servers must be reachable with the key, the firewall
# must let SSH through and drop port 80, destroy. Usage:
#   tests/tofu-smoke/run.sh [lab name]   (default: demo)
set -euo pipefail
export MSYS_NO_PATHCONV=1

# This directory as Docker sees it (a Windows path under Git Bash).
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && (pwd -W 2>/dev/null || pwd))"
readonly HERE
# The newest OpenTofu 1.x.
readonly TOFU_IMAGE=ghcr.io/opentofu/opentofu:1

# tofu <arguments>: Run OpenTofu in the lab's network with this directory as
# its working directory.
# Globals:
#   HLAB_NETWORK (read)
tofu() {
  docker run --rm -i --network "${HLAB_NETWORK}" \
    -e HCLOUD_ENDPOINT -e HCLOUD_TOKEN -e HETZNER_ENDPOINT -e TF_LOG \
    -e TF_IN_AUTOMATION=1 -e TF_VAR_ssh_public_key -v "${HERE}:/work" \
    -w /work "${TOFU_IMAGE}" "$@"
}

# check_plan_empty: Fail unless a plan after the apply shows no changes; on
# failure show the plan.
check_plan_empty() {
  local rc=0
  tofu plan -input=false -detailed-exitcode >/dev/null || rc=$?
  if ((rc != 0)); then
    echo "FAIL: second plan is not empty (exit ${rc})" >&2
    tofu plan -input=false
    exit 1
  fi
  echo "OK: second plan empty"
}

# check_servers <container>: Wait until each server answers over SSH, then
# print its hostname, the file from its user data and its private address.
check_servers() {
  local container="$1" server
  # The command expands on the server.
  # shellcheck disable=SC2016
  local report='cloud-init status --wait >/dev/null; echo "$(hostname):'
  # shellcheck disable=SC2016
  report+=' $(cat /etc/smoke), private $(ip -br -4 a | grep ^en | tr -s " ")"'
  for server in smoke-a smoke-b; do
    for _ in $(seq 1 60); do
      docker exec "${container}" timeout 3 hlab ssh "${server}" true \
        2>/dev/null && break
      sleep 2
    done
    docker exec "${container}" hlab ssh "${server}" "${report}"
  done
}

# check_firewall <container>: Check the firewall from main.tf: SSH from the
# operator's addresses (v4 and v6) passes, port 80 is dropped (no answer at
# all, where an open server would refuse at once).
check_firewall() {
  local container="$1" servers family ip target out
  servers="$(tofu output -json servers)"
  for family in ipv4 ipv6; do
    ip="$(echo "${servers}" \
      | docker exec -i "${container}" jq -r ".a.${family}")"
    target="${ip}"
    [[ "${family}" == ipv6 ]] && target="[${ip}]"
    # As nobody, like hlab ssh (which made /run/hlab-ssh above): a guest's
    # answers never reach a root process.
    if ! docker exec "${container}" setpriv --reuid=65534 --regid=65534 \
      --clear-groups -- env HOME=/run/hlab-ssh \
      ssh -q -i /run/hlab-ssh/id_ed25519 -o StrictHostKeyChecking=no \
      -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 \
      "root@${ip}" true; then
      echo "FAIL: SSH over ${family} (${ip}) blocked" >&2
      exit 1
    fi
    echo "OK: SSH over ${family} (${ip}) allowed by the firewall"
    # Dropped means no answer at all: the connect times out (socat: "Operation
    # now in progress"), it is neither refused nor unreachable, and it does
    # not succeed.
    out="$(docker exec "${container}" timeout 4 socat -T2 - \
      "TCP:${target}:80,connect-timeout=3" </dev/null 2>&1 || true)"
    if [[ "${out}" != *"Operation now in progress"* &&
      "${out}" != *"timed out"* ]]; then
      echo "FAIL: port 80 over ${family} not dropped: ${out:-no error}" >&2
      exit 1
    fi
    echo "OK: port 80 over ${family} dropped"
  done
}

# main [lab name]: Run the smoke test against the lab's container.
# Globals:
#   HLAB_NETWORK, HCLOUD_ENDPOINT, HCLOUD_TOKEN, HETZNER_ENDPOINT,
#   TF_VAR_ssh_public_key (set)
main() {
  local container="hlab-${1:-demo}"
  eval "$(docker exec "${container}" hlab env)"
  # The smoke key: the lab's own key pair (the test logs in with it from
  # inside the lab).
  TF_VAR_ssh_public_key="$(docker exec "${container}" cat /hlab/id_ed25519.pub)"
  export TF_VAR_ssh_public_key

  tofu init -input=false >/dev/null
  tofu apply -input=false -auto-approve
  check_plan_empty
  check_servers "${container}"
  check_firewall "${container}"
  tofu destroy -input=false -auto-approve >/dev/null
  echo "OK: destroyed"
  docker exec "${container}" hlab status
}

main "$@"
