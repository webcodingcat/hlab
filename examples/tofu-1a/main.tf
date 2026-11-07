# The smallest OpenTofu configuration against HLab: it reads a server type
# from the catalog and manages an SSH key. Run it after `eval "$(hlab env)"`;
# the provider takes HCLOUD_ENDPOINT and HCLOUD_TOKEN from the environment.

terraform {
  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "1.70.2"
    }
  }
}

variable "public_key" {
  description = "The public SSH key to upload, such as the content of ~/.ssh/id_ed25519.pub."
  type        = string
}

data "hcloud_server_type" "small" {
  name = "cx23"
}

resource "hcloud_ssh_key" "example" {
  name       = "hlab-example"
  public_key = var.public_key
}

output "server_type" {
  value = "${data.hcloud_server_type.small.name}: ${data.hcloud_server_type.small.cores} cores, ${data.hcloud_server_type.small.memory} GB"
}

output "ssh_key_fingerprint" {
  value = hcloud_ssh_key.example.fingerprint
}
