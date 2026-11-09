# Two servers in a private network behind a Cloud Firewall, one with a
# Floating IP: the first joins the network in its own resource, the second
# through hcloud_server_network; the provider attaches both after the
# create. The hcloud CLI's smoke test covers networks and firewalls in the
# create request itself. Run it after
# `eval "$(hlab env)"`; the provider takes HCLOUD_ENDPOINT and HCLOUD_TOKEN
# from the environment.

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
  name       = "hlab-example-2a"
  public_key = var.public_key
}

resource "hcloud_network" "app" {
  name     = "hlab-example-app"
  ip_range = "10.0.0.0/16"
}

resource "hcloud_network_subnet" "app" {
  network_id   = hcloud_network.app.id
  type         = "cloud"
  network_zone = "eu-central"
  ip_range     = "10.0.1.0/24"
}

# SSH and ping from anywhere; everything else in is dropped
resource "hcloud_firewall" "app" {
  name = "hlab-example-app"

  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = "22"
    source_ips = ["0.0.0.0/0", "::/0"]
  }

  rule {
    direction  = "in"
    protocol   = "icmp"
    source_ips = ["0.0.0.0/0", "::/0"]
  }
}

resource "hcloud_server" "web" {
  name         = "hlab-example-web"
  server_type  = "cpx22"
  image        = "debian-13"
  location     = "fsn1"
  ssh_keys     = [hcloud_ssh_key.admin.id]
  firewall_ids = [hcloud_firewall.app.id]

  # with ip and alias_ips given: without them the provider plans a change
  # of the block after every apply, against Hetzner too
  network {
    network_id = hcloud_network.app.id
    ip         = "10.0.1.5"
    alias_ips  = []
  }

  # the subnet must be there before the server joins the network
  depends_on = [hcloud_network_subnet.app]
}

resource "hcloud_server" "db" {
  name         = "hlab-example-db"
  server_type  = "cpx22"
  image        = "debian-13"
  location     = "fsn1"
  ssh_keys     = [hcloud_ssh_key.admin.id]
  firewall_ids = [hcloud_firewall.app.id]
}

resource "hcloud_server_network" "db" {
  server_id  = hcloud_server.db.id
  network_id = hcloud_network.app.id
  ip         = "10.0.1.10"
  alias_ips  = ["10.0.1.11"]

  depends_on = [hcloud_network_subnet.app]
}

resource "hcloud_floating_ip" "web" {
  name          = "hlab-example-web"
  type          = "ipv4"
  home_location = "fsn1"
}

resource "hcloud_floating_ip_assignment" "web" {
  floating_ip_id = hcloud_floating_ip.web.id
  server_id      = hcloud_server.web.id
}

output "web" {
  value = "ssh root@${hcloud_server.web.ipv4_address}"
}

output "floating_ip" {
  value = hcloud_floating_ip.web.ip_address
}

output "db_private" {
  value = hcloud_server_network.db.ip
}
