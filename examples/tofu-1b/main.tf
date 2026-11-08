# A server with a Primary IPv4 of its own and an IPv6 that goes with it,
# reachable by SSH from the host. Run it after `eval "$(hlab env)"`; the
# provider takes HCLOUD_ENDPOINT and HCLOUD_TOKEN from the environment.

terraform {
  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "1.70.2"
    }
  }
}

variable "public_key" {
  description = "The public SSH key to log in with, such as the content of ~/.ssh/id_ed25519.pub."
  type        = string
}

resource "hcloud_ssh_key" "admin" {
  name       = "hlab-example-admin"
  public_key = var.public_key
}

# kept when the server goes: auto_delete is false
resource "hcloud_primary_ip" "web" {
  name        = "hlab-example-web"
  type        = "ipv4"
  location    = "fsn1"
  auto_delete = false
}

resource "hcloud_server" "web" {
  name        = "hlab-example-web"
  server_type = "cpx22"
  image       = "debian-13"
  location    = "fsn1"
  ssh_keys    = [hcloud_ssh_key.admin.id]

  public_net {
    ipv4_enabled = true
    ipv4         = hcloud_primary_ip.web.id
    ipv6_enabled = true
  }
}

output "ssh" {
  value = "ssh root@${hcloud_server.web.ipv4_address}"
}

output "ipv6" {
  value = hcloud_server.web.ipv6_address
}
