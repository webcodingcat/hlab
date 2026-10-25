#!/usr/bin/env bash
# OpenTofu + hcloud provider against a running lab: apply, a second plan must
# show no changes, the servers must be reachable with the key, destroy. Usage:
# tests/tofu-smoke/run.sh [lab name]   (default: demo)
set -euo pipefail
export MSYS_NO_PATHCONV=1
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && (pwd -W 2>/dev/null || pwd))"
lab="hlab-${1:-demo}"
eval "$(docker exec "${lab}" hlab env)"
tofu() {
  # the newest OpenTofu 1.x
  docker run --rm -i --network "${HLAB_NETWORK}" \
    -e HCLOUD_ENDPOINT -e HCLOUD_TOKEN -e HETZNER_ENDPOINT -e TF_LOG \
    -e TF_IN_AUTOMATION=1 -e TF_VAR_ssh_public_key -v "${here}:/work" \
    -w /work ghcr.io/opentofu/opentofu:1 "$@"
}
export HCLOUD_ENDPOINT HCLOUD_TOKEN HETZNER_ENDPOINT
# The smoke key: the lab's own key pair (the test logs in with it from inside
# the lab).
TF_VAR_ssh_public_key="$(docker exec "${lab}" cat /hlab/id_ed25519.pub)"
export TF_VAR_ssh_public_key

tofu init -input=false >/dev/null
tofu apply -input=false -auto-approve
set +e
tofu plan -input=false -detailed-exitcode >/dev/null
rc=$?
set -e
[[ ${rc} -eq 0 ]] || {
  echo "FAIL: second plan is not empty (exit ${rc})"
  tofu plan -input=false
  exit 1
}
echo "OK: second plan empty"
for n in smoke-a smoke-b; do
  for _ in $(seq 1 60); do
    docker exec "${lab}" timeout 3 hlab ssh "${n}" true 2>/dev/null && break
    sleep 2
  done
  docker exec "${lab}" hlab ssh "${n}" \
    'cloud-init status --wait >/dev/null; echo "$(hostname): $(cat /etc/smoke), private $(ip -br -4 a | grep ^en | tr -s " ")"'
done
# The firewall from main.tf: SSH from the operator's addresses (v4 and v6)
# passes, port 80 is dropped (no answer at all, where an open server would
# refuse at once).
servers="$(tofu output -json servers)"
for fam in ipv4 ipv6; do
  ip="$(echo "${servers}" | docker exec -i "${lab}" jq -r ".a.${fam}")"
  target="${ip}"
  [[ "${fam}" == ipv6 ]] && target="[${ip}]"
  # as nobody, like hlab ssh (which made /run/hlab-ssh above): a guest's answers
  # never reach a root process
  if ! docker exec "${lab}" setpriv --reuid=65534 --regid=65534 \
    --clear-groups -- env HOME=/run/hlab-ssh \
    ssh -q -i /run/hlab-ssh/id_ed25519 -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 "root@${ip}" true; then
    echo "FAIL: SSH over ${fam} (${ip}) blocked"
    exit 1
  fi
  echo "OK: SSH over ${fam} (${ip}) allowed by the firewall"
  # dropped means no answer at all: the connect times out (socat: "Operation now
  # in progress"), it is neither refused nor unreachable, and it does not
  # succeed
  out="$(docker exec "${lab}" timeout 4 socat -T2 - \
    "TCP:${target}:80,connect-timeout=3" </dev/null 2>&1 || true)"
  if [[ "${out}" != *"Operation now in progress"* &&
    "${out}" != *"timed out"* ]]; then
    echo "FAIL: port 80 over ${fam} not dropped: ${out:-no error}"
    exit 1
  fi
  echo "OK: port 80 over ${fam} dropped"
done
tofu destroy -input=false -auto-approve >/dev/null
echo "OK: destroyed"
docker exec "${lab}" hlab status
