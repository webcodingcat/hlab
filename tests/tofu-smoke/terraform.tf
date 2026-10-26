# Smoke test: the hcloud provider against the lab's fake API. Run inside the
# lab's network namespace:
#   tests/tofu-smoke/run.sh   (apply, plan must show no changes, destroy)
terraform {
  # OpenTofu 1.x, as run.sh and bin/hlab pull it (ghcr.io/opentofu/opentofu:1)
  required_version = "~> 1.8"
  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "~> 1.54"
    }
  }
}
