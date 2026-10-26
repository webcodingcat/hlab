output "servers" {
  description = "Public and private addresses of the servers smoke-a and smoke-b, by key."
  value = {
    for key, server in hcloud_server.node : key => {
      ipv4    = server.ipv4_address
      ipv6    = server.ipv6_address
      private = one(server.network).ip
    }
  }
}

output "floating_ip" {
  description = "Address of the Floating IP assigned to smoke-a."
  value       = hcloud_floating_ip.entry.ip_address
}
