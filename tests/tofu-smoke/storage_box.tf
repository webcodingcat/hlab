# A Storage Box with a sub-account and a snapshot (endpoint from
# HETZNER_ENDPOINT).

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
