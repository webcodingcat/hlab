provider "hcloud" {
  # endpoint and token come from HCLOUD_ENDPOINT / HCLOUD_TOKEN (`hlab env`)
  poll_interval = "500ms"
}
