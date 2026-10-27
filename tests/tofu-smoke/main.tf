# Two servers in a private network behind a firewall, with a Floating IP.

resource "hcloud_ssh_key" "admin" {
  name       = "smoke-admin"
  public_key = var.ssh_public_key
}

resource "hcloud_network" "net" {
  name     = "smoke"
  ip_range = "10.0.0.0/16"
  labels   = { cluster = "smoke" }
}

resource "hcloud_network_subnet" "net" {
  network_id   = hcloud_network.net.id
  type         = "cloud"
  network_zone = "eu-central"
  ip_range     = "10.0.1.0/24"
}

resource "hcloud_firewall" "web" {
  name = "smoke-web"

  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = "443"
    source_ips = ["0.0.0.0/0", "::/0"]
  }

  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = "22"
    source_ips = ["192.0.2.0/24", "2001:db8:fffe::/48"]
  }

  rule {
    direction  = "in"
    protocol   = "icmp"
    source_ips = ["0.0.0.0/0", "::/0"]
  }

  apply_to {
    label_selector = "cluster=smoke"
  }
}

resource "hcloud_placement_group" "spread" {
  name   = "smoke-spread"
  type   = "spread"
  labels = { cluster = "smoke" }
}

resource "hcloud_server" "node" {
  for_each = { a = "fsn1", b = "nbg1" }

  name        = "smoke-${each.key}"
  server_type = "cx23"
  image       = "debian-13"
  location    = each.value
  ssh_keys    = [hcloud_ssh_key.admin.id]
  labels      = { cluster = "smoke" }
  user_data   = "#cloud-config\nwrite_files:\n  - path: /etc/smoke\n    content: ok\n"

  placement_group_id = hcloud_placement_group.spread.id

  network {
    network_id = hcloud_network.net.id
    # Without it the provider plans a change on every run (also against the
    # real API).
    alias_ips = []
  }

  depends_on = [hcloud_network_subnet.net]
}

resource "hcloud_floating_ip" "entry" {
  type          = "ipv4"
  home_location = "fsn1"
  name          = "smoke-entry"
}

resource "hcloud_floating_ip_assignment" "entry" {
  floating_ip_id = hcloud_floating_ip.entry.id
  server_id      = hcloud_server.node["a"].id
}

resource "hcloud_snapshot" "node_a" {
  server_id   = hcloud_server.node["a"].id
  description = "smoke snapshot"
  labels      = { cluster = "smoke" }
}
