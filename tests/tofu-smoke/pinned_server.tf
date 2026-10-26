# A server on a Primary IP created beforehand, with a volume and reverse DNS.

resource "hcloud_primary_ip" "spare" {
  name        = "smoke-spare"
  type        = "ipv4"
  auto_delete = false
  location    = "fsn1"
}

resource "hcloud_server" "pinned" {
  name        = "smoke-c"
  server_type = "cx23"
  image       = "debian-13"
  location    = "fsn1"
  ssh_keys    = [hcloud_ssh_key.admin.id]

  public_net {
    ipv4_enabled = true
    ipv4         = hcloud_primary_ip.spare.id
    ipv6_enabled = false
  }
}

resource "hcloud_volume" "data" {
  name      = "smoke-data"
  size      = 10
  server_id = hcloud_server.pinned.id
  automount = true
  format    = "ext4"
}

resource "hcloud_rdns" "pinned" {
  server_id  = hcloud_server.pinned.id
  ip_address = hcloud_server.pinned.ipv4_address
  dns_ptr    = "c.smoke.test"
}
