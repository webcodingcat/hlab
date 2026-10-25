# The rest of the APIs through the provider: primary IP, volume, reverse DNS, Storage Box (endpoint from
# HETZNER_ENDPOINT).

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

resource "hcloud_storage_box" "backup" {
  name             = "smoke-box"
  storage_box_type = "bx11"
  location         = "fsn1"
  password         = "Smoke-Secret-123"
  access_settings = {
    ssh_enabled          = true
    reachable_externally = true
  }
}

resource "hcloud_storage_box_subaccount" "cluster" {
  storage_box_id = hcloud_storage_box.backup.id
  home_directory = "cluster"
  password       = "Smoke-Sub-123"
  access_settings = {
    ssh_enabled = true
  }
}

resource "hcloud_storage_box_snapshot" "first" {
  storage_box_id = hcloud_storage_box.backup.id
  description    = "smoke"
}
