//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

func init() {
	registerRow(clientRow{Req: "CLI-04", Name: "ansible", Slots: 2, Run: testCLI04Ansible})
}

// testCLI04Ansible: in its own project, a create playbook run in WSL2
// builds the example environment; both servers answer SSH from host and
// WSL2 on their public IPv4 and once through the LB's TCP service; a delete
// playbook deletes it, every list of the project is empty and no VM of its
// servers remains.
func testCLI04Ansible(t *testing.T, _ *rowCtx) {
	_, w := sharedAnsible(t)
	e := newProjectEnv(t, "cli04-ansible")
	ex := newExample("ansible")
	row := map[string]any{
		"network": ex.network, "range": exampleRange, "subnet": exampleSubnet, "firewall": ex.firewall,
		"servers": ex.servers[:], "records": ex.records[:], "type": serverType, "image": serverImage,
		"location": serverLocation, "key": e.sshKey, "lb": ex.lb, "lb_type": exampleLBType, "lb_port": exampleLBPort,
		"volume": ex.volume, "size": exampleVolSize, "fip": ex.floatingIP, "zone": ex.zone,
	}

	// The servers are created at once (async) and waited for with a
	// deadline of their own, well below the playbook's.
	create := e.playbookWithin(t, w, "cli04-create", localPlay(t, row, `    - name: network
      hetzner.hcloud.network:
        name: "{{ row.network }}"
        ip_range: "{{ row.range }}"
        state: present
    - name: subnetwork
      hetzner.hcloud.subnetwork:
        network: "{{ row.network }}"
        type: cloud
        network_zone: eu-central
        ip_range: "{{ row.subnet }}"
        state: present
    - name: firewall
      hetzner.hcloud.firewall:
        name: "{{ row.firewall }}"
        rules:
          - direction: in
            protocol: tcp
            port: "22"
            source_ips: ["0.0.0.0/0", "::/0"]
        state: present
    - name: servers
      hetzner.hcloud.server:
        name: "{{ item }}"
        server_type: "{{ row.type }}"
        image: "{{ row.image }}"
        location: "{{ row.location }}"
        ssh_keys: ["{{ row.key }}"]
        firewalls: ["{{ row.firewall }}"]
        private_networks: ["{{ row.network }}"]
        state: present
      loop: "{{ row.servers }}"
      async: 2400
      poll: 0
      register: started
    - name: wait for the servers
      ansible.builtin.async_status:
        jid: "{{ item.ansible_job_id }}"
      loop: "{{ started.results }}"
      register: created
      until: created.finished
      retries: 480
      delay: 5
    - name: load balancer
      hetzner.hcloud.load_balancer:
        name: "{{ row.lb }}"
        load_balancer_type: "{{ row.lb_type }}"
        location: "{{ row.location }}"
        state: present
    - name: load balancer network
      hetzner.hcloud.load_balancer_network:
        load_balancer: "{{ row.lb }}"
        network: "{{ row.network }}"
        state: present
    - name: load balancer service
      hetzner.hcloud.load_balancer_service:
        load_balancer: "{{ row.lb }}"
        protocol: tcp
        listen_port: "{{ row.lb_port }}"
        destination_port: 22
        state: present
    - name: load balancer targets
      hetzner.hcloud.load_balancer_target:
        load_balancer: "{{ row.lb }}"
        type: server
        server: "{{ item }}"
        use_private_ip: true
        state: present
      loop: "{{ row.servers }}"
    - name: volume
      hetzner.hcloud.volume:
        name: "{{ row.volume }}"
        size: "{{ row.size }}"
        server: "{{ row.servers[0] }}"
        automount: true
        format: ext4
        state: present
    - name: floating IP
      hetzner.hcloud.floating_ip:
        name: "{{ row.fip }}"
        type: ipv4
        server: "{{ row.servers[0] }}"
        state: present
    - name: zone
      hetzner.hcloud.zone:
        name: "{{ row.zone }}"
        mode: primary
        state: present
    - name: server addresses
      hetzner.hcloud.server_info:
        name: "{{ item }}"
      loop: "{{ row.servers }}"
      register: info
    - name: A records
      hetzner.hcloud.zone_rrset:
        zone: "{{ row.zone }}"
        name: "{{ row.records[idx] }}"
        type: A
        records:
          - value: "{{ item.hcloud_server_info[0].ipv4_address }}"
        state: present
      loop: "{{ info.results }}"
      loop_control:
        index_var: idx
`), playbookServerTimeout)
	if !strings.Contains(create.Stdout, "failed=0") {
		t.Fatalf("playbook cli04-create did not finish without failures:\n%s", create)
	}

	ids := checkExample(t, e, ex)

	e.ansibleOK(t, w, "cli04-delete", localPlay(t, row, `    - name: zone absent
      hetzner.hcloud.zone:
        name: "{{ row.zone }}"
        state: absent
    - name: floating IP absent
      hetzner.hcloud.floating_ip:
        name: "{{ row.fip }}"
        force: true
        state: absent
    - name: volume absent
      hetzner.hcloud.volume:
        name: "{{ row.volume }}"
        state: absent
    - name: load balancer absent
      hetzner.hcloud.load_balancer:
        name: "{{ row.lb }}"
        state: absent
    - name: servers absent
      hetzner.hcloud.server:
        name: "{{ item }}"
        state: absent
      loop: "{{ row.servers }}"
    - name: firewall absent
      hetzner.hcloud.firewall:
        name: "{{ row.firewall }}"
        state: absent
    - name: network absent
      hetzner.hcloud.network:
        name: "{{ row.network }}"
        state: absent
`))
	assertProjectEmpty(t, e, ids)
}
