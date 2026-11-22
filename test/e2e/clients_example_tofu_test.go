//go:build e2e

package e2e

import (
	"fmt"
	"testing"
)

func init() {
	registerRow(clientRow{Req: "CLI-04", Name: "tofu", Slots: 2, Run: testCLI04Tofu})
}

// testCLI04Tofu: in its own project, one main.tf with `tofu apply` builds
// the example environment; both servers answer SSH from host and WSL2 on
// their public IPv4 and once through the LB's TCP service; `tofu destroy`
// deletes it, every list of the project is empty and no VM of its servers
// remains.
func testCLI04Tofu(t *testing.T, _ *rowCtx) {
	sharedTofu(t)
	e := newProjectEnv(t, "cli04-tofu")
	ex := newExample("tofu")
	d := e.newTofuDir(t, "cli04-example")
	d.timeout = tofuServerTimeout
	d.writeMain(t, fmt.Sprintf(`locals {
  servers = [%q, %q]
  records = [%q, %q]
}

resource "hcloud_network" "net" {
  name     = %q
  ip_range = %q
}

resource "hcloud_network_subnet" "subnet" {
  network_id   = hcloud_network.net.id
  type         = "cloud"
  network_zone = "eu-central"
  ip_range     = %q
}

resource "hcloud_firewall" "ssh" {
  name = %q

  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = "22"
    source_ips = ["0.0.0.0/0", "::/0"]
  }
}

resource "hcloud_server" "s" {
  count        = 2
  name         = local.servers[count.index]
  server_type  = %q
  image        = %q
  location     = %q
  ssh_keys     = [%q]
  firewall_ids = [hcloud_firewall.ssh.id]

  network {
    network_id = hcloud_network.net.id
  }

  depends_on = [hcloud_network_subnet.subnet]
}

resource "hcloud_load_balancer" "lb" {
  name               = %q
  load_balancer_type = %q
  location           = %q
}

resource "hcloud_load_balancer_network" "lb" {
  load_balancer_id = hcloud_load_balancer.lb.id
  subnet_id        = hcloud_network_subnet.subnet.id
}

resource "hcloud_load_balancer_service" "ssh" {
  load_balancer_id = hcloud_load_balancer.lb.id
  protocol         = "tcp"
  listen_port      = %d
  destination_port = 22
}

resource "hcloud_load_balancer_target" "s" {
  count            = 2
  type             = "server"
  load_balancer_id = hcloud_load_balancer.lb.id
  server_id        = hcloud_server.s[count.index].id
  use_private_ip   = true
  depends_on       = [hcloud_load_balancer_network.lb]
}

resource "hcloud_volume" "v" {
  name      = %q
  size      = %d
  server_id = hcloud_server.s[0].id
  automount = true
  format    = "ext4"
}

resource "hcloud_floating_ip" "f" {
  name      = %q
  type      = "ipv4"
  server_id = hcloud_server.s[0].id
}

resource "hcloud_zone" "z" {
  name = %q
  mode = "primary"
}

resource "hcloud_zone_rrset" "a" {
  count   = 2
  zone    = hcloud_zone.z.name
  name    = local.records[count.index]
  type    = "A"
  records = [{ value = hcloud_server.s[count.index].ipv4_address }]
}
`, ex.servers[0], ex.servers[1], ex.records[0], ex.records[1],
		ex.network, exampleRange, exampleSubnet, ex.firewall,
		serverType, serverImage, serverLocation, e.sshKey,
		ex.lb, exampleLBType, serverLocation, exampleLBPort,
		ex.volume, exampleVolSize, ex.floatingIP, ex.zone))
	d.run(t, "init", "-no-color")
	d.run(t, "apply", "-auto-approve", "-no-color")

	ids := checkExample(t, e, ex)

	d.run(t, "destroy", "-auto-approve", "-no-color")
	assertProjectEmpty(t, e, ids)
}
